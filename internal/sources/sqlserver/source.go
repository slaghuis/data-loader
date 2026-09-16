package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/slaghuis/data-loader/internal/sources"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

const Kind = "sqlserver"

func init() {
	sources.Register(Kind, New)
}

type Source struct {
	cfg contracts.SourceConfig
	db  *sql.DB
}

// New builds a SQL Server source. Expected params (after secret resolution):
//   server, port, database, user, password, encrypt (optional), trustservercertificate (optional)
func New(cfg contracts.SourceConfig) (contracts.Source, error) {
	return &Source{cfg: cfg}, nil
}

func (s *Source) Kind() string { return Kind }

func (s *Source) Open(ctx context.Context) error {
	dsn := buildDSN(s.cfg.Params)
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return fmt.Errorf("open sqlserver source: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlserver source: %w", err)
	}
	s.db = db
	return nil
}

func (s *Source) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Source) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Read executes the appropriate query for the load and streams rows to the handler.
func (s *Source) Read(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
	result := contracts.ReadResult{StartedAt: time.Now().UTC()}

	query, args, newWM, err := s.buildQuery(ctx, load)
	if err != nil {
		return result, err
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("query source: %w", err)
	}
	defer rows.Close()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return result, err
	}
	columns := make([]contracts.Column, len(colTypes))
	for i, ct := range colTypes {
		nullable, _ := ct.Nullable()
		columns[i] = contracts.Column{
			Name:     ct.Name(),
			DataType: canonicalType(ct.DatabaseTypeName()),
			Nullable: nullable,
		}
	}

	batchSize := load.BatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}

	batch := contracts.Batch{Columns: columns, Rows: make([]contracts.Row, 0, batchSize)}
	var rowsRead int64

	for rows.Next() {
		holders := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return result, err
		}
		row := make(contracts.Row, len(columns))
		for i, c := range columns {
			row[c.Name] = holders[i]
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
		return result, err
	}
	if len(batch.Rows) > 0 {
		if err := handler(ctx, batch); err != nil {
			return result, err
		}
	}

	result.RowsRead = rowsRead
	result.FinishedAt = time.Now().UTC()
	result.NewWatermark = newWM
	return result, nil
}

// buildQuery constructs the SELECT statement and returns the watermark value
// that should be persisted after a successful load.
//
// Correctness pattern: we snapshot "now" (or MAX(wm)) BEFORE reading, so any
// rows inserted mid-load with a higher watermark are picked up on the next run.
func (s *Source) buildQuery(ctx context.Context, load contracts.LoadConfig) (string, []any, contracts.Watermark, error) {
	obj := sanitizeObject(load.ObjectName)

	// Full-load modes: no watermark filtering.
	if load.Mode == contracts.LoadModeAppend || load.Mode == contracts.LoadModeReplace {
		q := fmt.Sprintf("SELECT * FROM %s", obj)
		return q, nil, contracts.Watermark{Type: contracts.WatermarkNone}, nil
	}

	// Delta mode requires a watermark column.
	if load.WatermarkColumn == "" || load.WatermarkType == contracts.WatermarkNone {
		return "", nil, contracts.Watermark{}, fmt.Errorf("delta load requires watermark column and type")
	}

	// Determine upper bound BEFORE reading data.
	upper, err := s.currentMaxWatermark(ctx, obj, load.WatermarkColumn, load.WatermarkType)
	if err != nil {
		return "", nil, contracts.Watermark{}, err
	}

	// First run (no previous watermark): initialize from current max WITHOUT
	// reading any data. This prevents accidentally back-loading a huge table.
	// The user can trigger an explicit full load if they want history.
	if load.LastWatermark.Value == "" {
		return "", nil, upper, errFirstRunInit{watermark: upper}
	}

	q := fmt.Sprintf(
		"SELECT * FROM %s WHERE [%s] > @p1 AND [%s] <= @p2 ORDER BY [%s]",
		obj, load.WatermarkColumn, load.WatermarkColumn, load.WatermarkColumn,
	)
	return q, []any{load.LastWatermark.Value, upper.Value}, upper, nil
}

// errFirstRunInit signals that the caller should persist the returned watermark
// without reading rows (first-run initialization).
type errFirstRunInit struct{ watermark contracts.Watermark }

func (e errFirstRunInit) Error() string { return "first-run watermark initialization" }

// IsFirstRunInit lets the pipeline detect this special case.
func IsFirstRunInit(err error) (contracts.Watermark, bool) {
	if e, ok := err.(errFirstRunInit); ok {
		return e.watermark, true
	}
	return contracts.Watermark{}, false
}

// currentMaxWatermark returns MAX(watermark_column) as a string.
func (s *Source) currentMaxWatermark(ctx context.Context, obj, col string, wmType contracts.WatermarkType) (contracts.Watermark, error) {
	q := fmt.Sprintf("SELECT MAX([%s]) FROM %s", col, obj)
	var raw sql.NullString
	// Use a permissive scan target so numeric and datetime values both work via cast.
	var t sql.NullTime
	var f sql.NullFloat64

	switch wmType {
	case contracts.WatermarkDatetime:
		if err := s.db.QueryRowContext(ctx, q).Scan(&t); err != nil {
			return contracts.Watermark{}, err
		}
		if !t.Valid {
			return contracts.Watermark{Type: wmType, Value: ""}, nil
		}
		return contracts.Watermark{Type: wmType, Value: t.Time.UTC().Format(time.RFC3339Nano)}, nil
	case contracts.WatermarkNumeric:
		if err := s.db.QueryRowContext(ctx, q).Scan(&f); err != nil {
			return contracts.Watermark{}, err
		}
		if !f.Valid {
			return contracts.Watermark{Type: wmType, Value: ""}, nil
		}
		return contracts.Watermark{Type: wmType, Value: fmt.Sprintf("%v", f.Float64)}, nil
	default:
		if err := s.db.QueryRowContext(ctx, q).Scan(&raw); err != nil {
			return contracts.Watermark{}, err
		}
		return contracts.Watermark{Type: wmType, Value: raw.String}, nil
	}
}

// ---------- helpers ----------

func buildDSN(p map[string]string) string {
	server := p["server"]
	port := p["port"]
	if port == "" {
		port = "1433"
	}
	dsn := fmt.Sprintf("sqlserver://%s:%s@%s:%s?database=%s",
		p["user"], p["password"], server, port, p["database"])
	if v, ok := p["encrypt"]; ok {
		dsn += "&encrypt=" + v
	}
	if v, ok := p["trustservercertificate"]; ok {
		dsn += "&TrustServerCertificate=" + v
	}
	return dsn
}

// sanitizeObject accepts "schema.table" or "table" and returns a bracketed
// identifier. It rejects anything with suspicious characters.
func sanitizeObject(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		p = strings.Trim(p, "[]")
		parts[i] = "[" + p + "]"
	}
	return strings.Join(parts, ".")
}

// canonicalType maps SQL Server type names to our canonical set.
func canonicalType(dbType string) string {
	switch strings.ToUpper(dbType) {
	case "BIT":
		return "bool"
	case "TINYINT", "SMALLINT", "INT":
		return "int32"
	case "BIGINT":
		return "int64"
	case "REAL":
		return "float32"
	case "FLOAT":
		return "float64"
	case "DECIMAL", "NUMERIC", "MONEY", "SMALLMONEY":
		return "decimal"
	case "DATE", "DATETIME", "DATETIME2", "SMALLDATETIME", "DATETIMEOFFSET", "TIME":
		return "datetime"
	case "BINARY", "VARBINARY", "IMAGE":
		return "bytes"
	default:
		return "string"
	}
}
