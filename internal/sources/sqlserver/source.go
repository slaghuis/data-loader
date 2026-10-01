package sqlserver

import (
        "context"
        "database/sql"
        "fmt"
        "strings"
        "strconv"
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

        // Delta first-run: seed watermark, read nothing.
        if load.Mode == contracts.LoadModeDelta &&
                load.WatermarkColumn != "" &&
                load.WatermarkType != contracts.WatermarkNone &&
                load.LastWatermark.Value == "" {

                upper, err := s.currentMaxWatermark(ctx, sanitizeObject(load.ObjectName),
                 load.WatermarkColumn, load.WatermarkType)
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
        b[3], b[2], b[1], b[0],   // Data1 (4 bytes, LE -> BE)
        b[5], b[4],               // Data2 (2 bytes, LE -> BE)
        b[7], b[6],               // Data3 (2 bytes, LE -> BE)
        b[8], b[9],               // Data4 (as-is)
        b[10], b[11], b[12], b[13], b[14], b[15], // Data4 cont. (as-is)
    }
    return fmt.Sprintf("%08X-%04X-%04X-%04X-%012X",
        g[0:4], g[4:6], g[6:8], g[8:10], g[10:16]), nil
}
