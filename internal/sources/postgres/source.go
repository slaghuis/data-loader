package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"strcov"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/slaghuis/data-loader/internal/logging"
	"github.com/slaghuis/data-loader/internal/sources"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

const kind = "postgres"

func init() {
	sources.Register(kind, New)
}

type Source struct {
	cfg contracts.SourceConfig
	db  *sql.DB
}

func New(cfg contracts.SourceConfig) (contracts.Source, error) {
	return &Source{cfg: cfg}, nil
}

func (s *Source) Kind() string { return kind }

func (s *Source) Open(ctx context.Context) error {
	dsn, err := buildDSN(s.cfg.Params)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open postgres source: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping postgres source: %w", err)
	}
	s.db = db
	return nil
}

func (s *Source) Ping(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("source not open")
	}
	return s.db.PingContext(ctx)
}

func (s *Source) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Source) Read(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
	log := logging.FromContext(ctx)
	start := time.Now().UTC()
	result := contracts.ReadResult{StartedAt: start}

    // First-run initialization for delta loads: seed the watermark from MAX()
    // without reading any rows. Prevents accidental full back-load of large tables.
    if load.Mode == contracts.LoadModeDelta &&
        load.WatermarkColumn != "" &&
        load.WatermarkType != contracts.WatermarkNone &&
        load.LastWatermark.Value == "" {

        upper, err := s.currentMaxWatermark(ctx, load.ObjectName, load.WatermarkColumn, load.WatermarkType)
        if err != nil {
            return result, fmt.Errorf("initialize watermark: %w", err)
        }
        log.Info("first-run: seeding watermark, no rows loaded",
            "category", "watermark",
            "watermark_to", upper.Value,
        )
        result.NewWatermark = upper
        result.WatermarkInit = true
        result.FinishedAt = time.Now().UTC()
        return result, nil
    }
	
	query, args, err := buildQuery(load)
	if err != nil {
		return result, err
	}
	log.Debug("executing source query",
		"category", "read",
		"query", query,
		"args_count", len(args),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("execute source query: %w", err)
	}
	defer rows.Close()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return result, fmt.Errorf("column types: %w", err)
	}
	columns := make([]contracts.Column, len(colTypes))
	colNames := make([]string, len(colTypes))
	for i, ct := range colTypes {
		nullable, _ := ct.Nullable()
		columns[i] = contracts.Column{
			Name:     ct.Name(),
			DataType: mapDriverType(ct.DatabaseTypeName()),
			Nullable: nullable,
		}
		colNames[i] = ct.Name()
	}

	batch := contracts.Batch{Columns: columns, Rows: make([]contracts.Row, 0, load.BatchSize)}
	newWatermark := load.LastWatermark
	watermarkColIdx := -1
	if load.Mode == contracts.LoadModeDelta && load.WatermarkColumn != "" {
		for i, n := range colNames {
			if strings.EqualFold(n, load.WatermarkColumn) {
				watermarkColIdx = i
				break
			}
		}
	}

	var rowsRead int64
	for rows.Next() {
		values := make([]any, len(colTypes))
		ptrs := make([]any, len(colTypes))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return result, fmt.Errorf("scan row: %w", err)
		}

		row := make(contracts.Row, len(colNames))
		for i, n := range colNames {
    		row[n] = normalizeValue(values[i], columns[i].DataType)
		}
		batch.Rows = append(batch.Rows, row)
		rowsRead++

		if watermarkColIdx >= 0 {
			if v := values[watermarkColIdx]; v != nil {
				newWatermark.Value = formatWatermark(v, load.WatermarkType)
				newWatermark.Type = load.WatermarkType
			}
		}

		if len(batch.Rows) >= load.BatchSize {
			if err := handler(ctx, batch); err != nil {
				return result, err
			}
			batch.Rows = batch.Rows[:0]
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate rows: %w", err)
	}

	if len(batch.Rows) > 0 {
		if err := handler(ctx, batch); err != nil {
			return result, err
		}
	}

	result.RowsRead = rowsRead
	result.NewWatermark = newWatermark
	result.FinishedAt = time.Now().UTC()
	return result, nil
}

// ---- helpers ----

// buildDSN constructs a Postgres URL from param map.
// Recognized params: host, port, database, user, password, sslmode, search_path, application_name.
func buildDSN(params map[string]string) (string, error) {
	host := params["host"]
	database := params["database"]
	if host == "" || database == "" {
		return "", fmt.Errorf("postgres source requires 'host' and 'database' params")
	}
	port := params["port"]
	if port == "" {
		port = "5432"
	}
	user := params["user"]
	password := params["password"]

	u := &url.URL{
		Scheme: "postgres",
		Host:   host + ":" + port,
		Path:   "/" + database,
	}
	if user != "" {
		if password != "" {
			u.User = url.UserPassword(user, password)
		} else {
			u.User = url.User(user)
		}
	}

	q := u.Query()
	if v, ok := params["sslmode"]; ok && v != "" {
		q.Set("sslmode", v)
	} else {
		q.Set("sslmode", "disable")
	}
	if v, ok := params["search_path"]; ok && v != "" {
		q.Set("search_path", v)
	}
	if v, ok := params["application_name"]; ok && v != "" {
		q.Set("application_name", v)
	} else {
		q.Set("application_name", "data-loader")
	}
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// buildQuery builds a Postgres SELECT with $1 placeholders and proper quoting.
func buildQuery(load contracts.LoadConfig) (string, []any, error) {
	obj := quoteQualifiedIdent(load.ObjectName)
	base := fmt.Sprintf("SELECT * FROM %s", obj)

	if load.Mode == contracts.LoadModeDelta && load.WatermarkColumn != "" && load.LastWatermark.Value != "" {
		typed, err := parseWatermark(load.LastWatermark)
		if err != nil {
			return "", nil, err
		}
		q := fmt.Sprintf(`%s WHERE %s > $1 ORDER BY %s ASC`,
			base,
			quoteIdent(load.WatermarkColumn),
			quoteIdent(load.WatermarkColumn),
		)
		return q, []any{typed}, nil
	}
	if load.Mode == contracts.LoadModeDelta && load.WatermarkColumn != "" {
		q := fmt.Sprintf(`%s ORDER BY %s ASC`, base, quoteIdent(load.WatermarkColumn))
		return q, nil, nil
	}
	return base, nil, nil
}

// quoteIdent double-quotes a single Postgres identifier, escaping embedded quotes.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteQualifiedIdent handles "schema.table" or just "table".
// If already contains a double quote, it is passed through untouched
// (assume the user knows what they're doing).
func quoteQualifiedIdent(name string) string {
	if strings.Contains(name, `"`) {
		return name
	}
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = quoteIdent(p)
	}
	return strings.Join(parts, ".")
}

func parseWatermark(w contracts.Watermark) (any, error) {
	switch w.Type {
	case contracts.WatermarkDatetime:
		return time.Parse(time.RFC3339Nano, w.Value)
	case contracts.WatermarkNumeric:
		return w.Value, nil
	default:
		return w.Value, nil
	}
}

func formatWatermark(v any, t contracts.WatermarkType) string {
	switch val := v.(type) {
	case time.Time:
		return val.UTC().Format(time.RFC3339Nano)
	case []byte:
		return string(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// normalizeValue coerces driver-returned values into canonical Go types aligned
// with contracts.Column.DataType, so sinks don't have to know pgx quirks.
func normalizeValue(v any, canonical string) any {
    if v == nil {
        return nil
    }
    switch canonical {
    case "decimal":
        // pgx returns NUMERIC/DECIMAL/MONEY as []byte with ASCII digits.
        switch x := v.(type) {
        case []byte:
            return string(x)
        case string:
            return x
        case float64:
            return strconv.FormatFloat(x, 'f', -1, 64)
        case int64:
            return strconv.FormatInt(x, 10)
        }
        return v

    case "string":
        // UUID, JSON, JSONB, and some text types come back as []byte.
        if b, ok := v.([]byte); ok {
            return string(b)
        }
        return v

    case "bytes":
        return v

    case "datetime":
        if t, ok := v.(time.Time); ok {
            return t.UTC()
        }
        return v

    default:
        return v
    }
}

// mapDriverType maps Postgres type names (as reported by pgx) to our canonical types.
func mapDriverType(dbType string) string {
    switch strings.ToUpper(dbType) {
    case "INT8", "BIGINT":
        return "int64"
    case "INT4", "INTEGER", "INT2", "SMALLINT":
        return "int32"
    case "FLOAT4", "REAL":
        return "float32"
    case "FLOAT8", "DOUBLE PRECISION":
        return "float64"
    case "NUMERIC", "DECIMAL", "MONEY":
        return "decimal"
    case "BOOL", "BOOLEAN":
        return "bool"
    case "TIMESTAMP", "TIMESTAMPTZ", "DATE", "TIME", "TIMETZ":
        return "datetime"
    case "BYTEA":
        return "bytes"
    case "UUID", "JSON", "JSONB", "TEXT", "VARCHAR", "BPCHAR", "CHAR", "NAME", "CITEXT":
        return "string"
    default:
        return "string"
    }
}

// currentMaxWatermark queries MAX(watermark_column) as a string.
func (s *Source) currentMaxWatermark(ctx context.Context, obj, col string, wmType contracts.WatermarkType) (contracts.Watermark, error) {
    q := fmt.Sprintf("SELECT MAX(%s) FROM %s", quoteIdent(col), quoteQualifiedIdent(obj))
    switch wmType {
    case contracts.WatermarkDatetime:
        var t sql.NullTime
        if err := s.db.QueryRowContext(ctx, q).Scan(&t); err != nil {
            return contracts.Watermark{}, err
        }
        if !t.Valid {
            return contracts.Watermark{Type: wmType, Value: ""}, nil
        }
        return contracts.Watermark{Type: wmType, Value: t.Time.UTC().Format(time.RFC3339Nano)}, nil
    case contracts.WatermarkNumeric:
        var f sql.NullFloat64
        if err := s.db.QueryRowContext(ctx, q).Scan(&f); err != nil {
            return contracts.Watermark{}, err
        }
        if !f.Valid {
            return contracts.Watermark{Type: wmType, Value: ""}, nil
        }
        return contracts.Watermark{Type: wmType, Value: strconv.FormatFloat(f.Float64, 'f', -1, 64)}, nil
    default:
        var raw sql.NullString
        if err := s.db.QueryRowContext(ctx, q).Scan(&raw); err != nil {
            return contracts.Watermark{}, err
        }
        return contracts.Watermark{Type: wmType, Value: raw.String}, nil
    }
}
