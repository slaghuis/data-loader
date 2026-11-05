package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/slaghuis/data-loader/internal/sources"
	"github.com/slaghuis/data-loader/internal/sqlident"
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
//
//	server, port, database, user, password, encrypt (optional), trustservercertificate (optional)
func New(cfg contracts.SourceConfig) (contracts.Source, error) {
	return &Source{cfg: cfg}, nil
}

func (s *Source) Kind() string { return Kind }

func (s *Source) Open(ctx context.Context) error {
	dsn, err := buildDSN(s.cfg.Params)
	if err != nil {
		return err
	}
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

	// Delta first-run: seed watermark, read nothing.
	if load.Mode == contracts.LoadModeDelta &&
		load.WatermarkColumn != "" &&
		load.WatermarkType != contracts.WatermarkNone &&
		load.LastWatermark.Value == "" {

		obj, err := quoteObject(load.ObjectName)
		if err != nil {
			return result, err
		}
		upper, err := s.currentMaxWatermark(ctx, obj, load.WatermarkColumn, load.WatermarkType)
		if err != nil {
			return result, fmt.Errorf("initialize watermark: %w", err)
		}
		result.NewWatermark = upper
		result.WatermarkInit = true
		result.FinishedAt = time.Now().UTC()
		return result, nil
	}

	query, args, newWM, err := s.buildQuery(ctx, load)
	if err != nil {
		return result, err
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("query source: %w", err)
	}
	defer func() { _ = rows.Close() }()

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
			row[c.Name] = normalizeValue(holders[i], c.DataType)
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
	obj, err := quoteObject(load.ObjectName)
	if err != nil {
		return "", nil, contracts.Watermark{}, err
	}

	// Full-load modes: no watermark filtering.
	if load.Mode == contracts.LoadModeAppend || load.Mode == contracts.LoadModeReplace {
		q := fmt.Sprintf("SELECT * FROM %s", obj)
		return q, nil, contracts.Watermark{Type: contracts.WatermarkNone}, nil
	}

	// Delta mode requires a watermark column.
	if load.WatermarkColumn == "" || load.WatermarkType == contracts.WatermarkNone {
		return "", nil, contracts.Watermark{}, fmt.Errorf("delta load requires watermark column and type")
	}
	if err := sqlident.Validate(load.WatermarkColumn); err != nil {
		return "", nil, contracts.Watermark{}, fmt.Errorf("watermark column: %w", err)
	}
	wmCol := sqlident.QuoteMSSQL(load.WatermarkColumn)

	// Determine upper bound BEFORE reading data.
	upper, err := s.currentMaxWatermark(ctx, obj, load.WatermarkColumn, load.WatermarkType)
	if err != nil {
		return "", nil, contracts.Watermark{}, err
	}

	// First-run is handled in Read before buildQuery is called.
	q := fmt.Sprintf(
		"SELECT * FROM %s WHERE %s > @p1 AND %s <= @p2 ORDER BY %s",
		obj, wmCol, wmCol, wmCol,
	)
	return q, []any{load.LastWatermark.Value, upper.Value}, upper, nil
}

// currentMaxWatermark returns MAX(watermark_column) as a string.
// Numeric values are cast in SQL and scanned as strings so values ≥ 1e6 and
// integers above 2^53 keep a stable decimal representation (D-03).
func (s *Source) currentMaxWatermark(ctx context.Context, obj, col string, wmType contracts.WatermarkType) (contracts.Watermark, error) {
	if err := sqlident.Validate(col); err != nil {
		return contracts.Watermark{}, fmt.Errorf("watermark column: %w", err)
	}
	quoted := sqlident.QuoteMSSQL(col)

	switch wmType {
	case contracts.WatermarkDatetime:
		q := fmt.Sprintf("SELECT MAX(%s) FROM %s", quoted, obj)
		var t sql.NullTime
		if err := s.db.QueryRowContext(ctx, q).Scan(&t); err != nil {
			return contracts.Watermark{}, err
		}
		if !t.Valid {
			return contracts.Watermark{Type: wmType, Value: ""}, nil
		}
		return contracts.Watermark{Type: wmType, Value: t.Time.UTC().Format(time.RFC3339Nano)}, nil
	case contracts.WatermarkNumeric:
		q := fmt.Sprintf("SELECT CONVERT(varchar(40), MAX(%s)) FROM %s", quoted, obj)
		var raw sql.NullString
		if err := s.db.QueryRowContext(ctx, q).Scan(&raw); err != nil {
			return contracts.Watermark{}, err
		}
		if !raw.Valid {
			return contracts.Watermark{Type: wmType, Value: ""}, nil
		}
		return contracts.Watermark{Type: wmType, Value: strings.TrimSpace(raw.String)}, nil
	default:
		q := fmt.Sprintf("SELECT CONVERT(varchar(400), MAX(%s)) FROM %s", quoted, obj)
		var raw sql.NullString
		if err := s.db.QueryRowContext(ctx, q).Scan(&raw); err != nil {
			return contracts.Watermark{}, err
		}
		return contracts.Watermark{Type: wmType, Value: raw.String}, nil
	}
}

// ---------- helpers ----------

// buildDSN constructs a sqlserver:// URL with proper escaping (D-08).
func buildDSN(p map[string]string) (string, error) {
	server := p["server"]
	database := p["database"]
	if server == "" || database == "" {
		return "", fmt.Errorf("sqlserver source requires 'server' and 'database' params")
	}
	port := p["port"]
	if port == "" {
		port = "1433"
	}
	u := &url.URL{
		Scheme: "sqlserver",
		Host:   server + ":" + port,
	}
	user := p["user"]
	password := p["password"]
	if user != "" {
		if password != "" {
			u.User = url.UserPassword(user, password)
		} else {
			u.User = url.User(user)
		}
	}
	q := u.Query()
	q.Set("database", database)
	if v, ok := p["encrypt"]; ok && v != "" {
		q.Set("encrypt", v)
	}
	if v, ok := p["trustservercertificate"]; ok && v != "" {
		q.Set("TrustServerCertificate", v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func quoteObject(name string) (string, error) {
	return sqlident.QuoteMSSQLQualified(name)
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
	case "UNIQUEIDENTIFIER":
		return "guid"
	default:
		return "string"
	}
}

// normalizeValue coerces driver-returned values into canonical Go types
// aligned with contracts.Column.DataType, so sinks don't have to know
// about driver quirks (e.g. go-mssqldb returning DECIMAL as []byte).
func normalizeValue(v any, canonical string) any {
	if v == nil {
		return nil
	}
	switch canonical {
	case "decimal":
		// go-mssqldb returns DECIMAL/NUMERIC/MONEY as []byte with ASCII digits.
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
		// Fall through: leave numeric types (int64, float64) as-is.
		return v

	case "string":
		// Some string-like SQL Server types (UNIQUEIDENTIFIER, XML, etc.)
		// come back as []byte. Normalize to string.
		if b, ok := v.([]byte); ok {
			return string(b)
		}
		return v

	case "bytes":
		// Keep binary as []byte.
		return v

	case "datetime":
		// time.Time already; nothing to do.
		return v

	case "guid":
		if b, ok := v.([]byte); ok {
			s, err := formatMSSQLGUID(b)
			if err != nil {
				return string(b) // fall back; sink will show garbage but we don't lose the row
			}
			return s
		}
		if s, ok := v.(string); ok {
			return s
		}
		return v

	default:
		return v
	}
}

// formatMSSQLGUID converts the 16-byte UNIQUEIDENTIFIER representation returned
// by go-mssqldb into the canonical hyphenated form (e.g.
// "A7027B07-49BC-4766-A283-4B776DFA74DA").
//
// SQL Server stores GUIDs with the first three groups in little-endian byte
// order and the last two groups in big-endian order. The driver returns the
// raw storage bytes, so we must swap groups 1–3 before formatting.
func formatMSSQLGUID(b []byte) (string, error) {
	if len(b) != 16 {
		return "", fmt.Errorf("uniqueidentifier: expected 16 bytes, got %d", len(b))
	}
	// Swap into standard GUID byte order.
	g := [16]byte{
		b[3], b[2], b[1], b[0], // Data1 (4 bytes, LE -> BE)
		b[5], b[4], // Data2 (2 bytes, LE -> BE)
		b[7], b[6], // Data3 (2 bytes, LE -> BE)
		b[8], b[9], // Data4 (as-is)
		b[10], b[11], b[12], b[13], b[14], b[15], // Data4 cont. (as-is)
	}
	return fmt.Sprintf("%08X-%04X-%04X-%04X-%012X",
		g[0:4], g[4:6], g[6:8], g[8:10], g[10:16]), nil
}
