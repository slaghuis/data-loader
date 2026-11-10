package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/slaghuis/data-loader/internal/logging"
	"github.com/slaghuis/data-loader/internal/sources"
	"github.com/slaghuis/data-loader/internal/sqlident"
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
	// Sibling backfill cursors are pre-seeded and carry WatermarkCeiling (ADR-022).
	if load.Mode == contracts.LoadModeDelta &&
		load.WatermarkColumn != "" &&
		load.WatermarkType != contracts.WatermarkNone &&
		load.LastWatermark.Value == "" &&
		load.WatermarkCeiling == "" {

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

	if load.WatermarkCeiling != "" {
		cmp, err := contracts.CompareWatermark(load.WatermarkType, load.LastWatermark.Value, load.WatermarkCeiling)
		if err != nil {
			return result, err
		}
		if cmp >= 0 {
			result.NewWatermark = contracts.Watermark{Type: load.WatermarkType, Value: load.WatermarkCeiling}
			result.FinishedAt = time.Now().UTC()
			return result, nil
		}
	}

	query, args, newWM, limited, err := s.buildQuery(ctx, load)
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
	defer func() { _ = rows.Close() }()

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

	batchSize := load.BatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}
	batch := contracts.Batch{Columns: columns, Rows: make([]contracts.Row, 0, batchSize)}

	var rowsRead int64
	var lastWM string
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
		if limited {
			if v, ok := row[load.WatermarkColumn]; ok {
				lastWM = formatWatermark(v, load.WatermarkType)
			} else {
				for k, v := range row {
					if strings.EqualFold(k, load.WatermarkColumn) {
						lastWM = formatWatermark(v, load.WatermarkType)
						break
					}
				}
			}
		}
		batch.Rows = append(batch.Rows, row)
		rowsRead++

		if len(batch.Rows) >= batchSize {
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
	result.NewWatermark = newWM
	if limited {
		if lastWM != "" {
			result.NewWatermark = contracts.Watermark{Type: load.WatermarkType, Value: lastWM}
		}
	}
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

// buildQuery builds a Postgres SELECT with $1/$2 placeholders and proper quoting.
// For delta loads it snapshots MAX(watermark) before reading so mid-load inserts
// with a higher watermark are deferred to the next run (D-18).
// When WatermarkCeiling is set (ADR-022), the upper bound is clamped and LIMIT
// batch_size is applied (limited=true).
func (s *Source) buildQuery(ctx context.Context, load contracts.LoadConfig) (string, []any, contracts.Watermark, bool, error) {
	obj, err := sqlident.QuotePostgresQualified(load.ObjectName)
	if err != nil {
		return "", nil, contracts.Watermark{}, false, err
	}
	base := fmt.Sprintf("SELECT * FROM %s", obj)

	if load.Mode == contracts.LoadModeAppend || load.Mode == contracts.LoadModeReplace {
		return base, nil, contracts.Watermark{Type: contracts.WatermarkNone}, false, nil
	}

	if load.WatermarkColumn == "" || load.WatermarkType == contracts.WatermarkNone {
		return "", nil, contracts.Watermark{}, false, fmt.Errorf("delta load requires watermark column and type")
	}
	if err := sqlident.Validate(load.WatermarkColumn); err != nil {
		return "", nil, contracts.Watermark{}, false, fmt.Errorf("watermark column: %w", err)
	}
	wm := sqlident.QuotePostgres(load.WatermarkColumn)

	upper, err := s.currentMaxWatermark(ctx, load.ObjectName, load.WatermarkColumn, load.WatermarkType)
	if err != nil {
		return "", nil, contracts.Watermark{}, false, err
	}
	if load.WatermarkCeiling != "" {
		clamped, err := contracts.MinWatermark(load.WatermarkType, upper.Value, load.WatermarkCeiling)
		if err != nil {
			return "", nil, contracts.Watermark{}, false, err
		}
		upper.Value = clamped
	}

	last, err := parseWatermark(load.LastWatermark)
	if err != nil {
		return "", nil, contracts.Watermark{}, false, err
	}
	upperArg, err := parseWatermark(upper)
	if err != nil {
		return "", nil, contracts.Watermark{}, false, err
	}

	batchSize := load.BatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}

	if load.WatermarkCeiling != "" {
		q := fmt.Sprintf(`%s WHERE %s > $1 AND %s <= $2 ORDER BY %s ASC LIMIT $3`, base, wm, wm, wm)
		return q, []any{last, upperArg, batchSize}, upper, true, nil
	}

	q := fmt.Sprintf(`%s WHERE %s > $1 AND %s <= $2 ORDER BY %s ASC`, base, wm, wm, wm)
	return q, []any{last, upperArg}, upper, false, nil
}

// CurrentMinWatermark returns MIN(watermark_column) as a string (ADR-022 empty stop).
func (s *Source) CurrentMinWatermark(ctx context.Context, objectName, column string, wmType contracts.WatermarkType) (contracts.Watermark, error) {
	return s.currentMinWatermark(ctx, objectName, column, wmType)
}

func (s *Source) currentMinWatermark(ctx context.Context, obj, col string, wmType contracts.WatermarkType) (contracts.Watermark, error) {
	if err := sqlident.Validate(col); err != nil {
		return contracts.Watermark{}, fmt.Errorf("watermark column: %w", err)
	}
	quotedObj, err := sqlident.QuotePostgresQualified(obj)
	if err != nil {
		return contracts.Watermark{}, err
	}
	quotedCol := sqlident.QuotePostgres(col)
	switch wmType {
	case contracts.WatermarkDatetime:
		q := fmt.Sprintf("SELECT MIN(%s) FROM %s", quotedCol, quotedObj)
		var t sql.NullTime
		if err := s.db.QueryRowContext(ctx, q).Scan(&t); err != nil {
			return contracts.Watermark{}, err
		}
		if !t.Valid {
			return contracts.Watermark{Type: wmType, Value: ""}, nil
		}
		return contracts.Watermark{Type: wmType, Value: t.Time.UTC().Format(time.RFC3339Nano)}, nil
	case contracts.WatermarkNumeric:
		q := fmt.Sprintf("SELECT MIN(%s)::text FROM %s", quotedCol, quotedObj)
		var raw sql.NullString
		if err := s.db.QueryRowContext(ctx, q).Scan(&raw); err != nil {
			return contracts.Watermark{}, err
		}
		if !raw.Valid {
			return contracts.Watermark{Type: wmType, Value: ""}, nil
		}
		return contracts.Watermark{Type: wmType, Value: strings.TrimSpace(raw.String)}, nil
	default:
		q := fmt.Sprintf("SELECT MIN(%s)::text FROM %s", quotedCol, quotedObj)
		var raw sql.NullString
		if err := s.db.QueryRowContext(ctx, q).Scan(&raw); err != nil {
			return contracts.Watermark{}, err
		}
		return contracts.Watermark{Type: wmType, Value: raw.String}, nil
	}
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
	if v == nil {
		return ""
	}
	switch t {
	case contracts.WatermarkDatetime:
		if tm, ok := v.(time.Time); ok {
			return tm.UTC().Format(time.RFC3339Nano)
		}
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
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
	if err := sqlident.Validate(col); err != nil {
		return contracts.Watermark{}, fmt.Errorf("watermark column: %w", err)
	}
	quotedObj, err := sqlident.QuotePostgresQualified(obj)
	if err != nil {
		return contracts.Watermark{}, err
	}
	quotedCol := sqlident.QuotePostgres(col)
	switch wmType {
	case contracts.WatermarkDatetime:
		q := fmt.Sprintf("SELECT MAX(%s) FROM %s", quotedCol, quotedObj)
		var t sql.NullTime
		if err := s.db.QueryRowContext(ctx, q).Scan(&t); err != nil {
			return contracts.Watermark{}, err
		}
		if !t.Valid {
			return contracts.Watermark{Type: wmType, Value: ""}, nil
		}
		return contracts.Watermark{Type: wmType, Value: t.Time.UTC().Format(time.RFC3339Nano)}, nil
	case contracts.WatermarkNumeric:
		// Cast in SQL so large integers stay exact (aligns with D-03; full D-18 upper-bound is separate).
		q := fmt.Sprintf("SELECT MAX(%s)::text FROM %s", quotedCol, quotedObj)
		var raw sql.NullString
		if err := s.db.QueryRowContext(ctx, q).Scan(&raw); err != nil {
			return contracts.Watermark{}, err
		}
		if !raw.Valid {
			return contracts.Watermark{Type: wmType, Value: ""}, nil
		}
		return contracts.Watermark{Type: wmType, Value: strings.TrimSpace(raw.String)}, nil
	default:
		q := fmt.Sprintf("SELECT MAX(%s)::text FROM %s", quotedCol, quotedObj)
		var raw sql.NullString
		if err := s.db.QueryRowContext(ctx, q).Scan(&raw); err != nil {
			return contracts.Watermark{}, err
		}
		return contracts.Watermark{Type: wmType, Value: raw.String}, nil
	}
}
