package sink

/*
Note on bulk copy: the exact driverConn interface may differ slightly by go-mssqldb version. If your version exposes bulk through mssql.CopyIn prepared statement style instead, we'll adapt this — I'll flag it when we test. The interface stays the same either way.
*/

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

// LoadTimestampColumn is appended to every target table.
const LoadTimestampColumn = "yb_load_timestamp"

type MSSQLSink struct {
	dsn string
	db  *sql.DB
}

func NewMSSQLSink(dsn string) *MSSQLSink {
	return &MSSQLSink{dsn: dsn}
}

func (s *MSSQLSink) Open(ctx context.Context) error {
	db, err := sql.Open("sqlserver", s.dsn)
	if err != nil {
		return fmt.Errorf("open sink: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sink: %w", err)
	}
	s.db = db
	return nil
}

func (s *MSSQLSink) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *MSSQLSink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// EnsureTable creates the target table if it does not exist.
// The yb_load_timestamp column is appended automatically.
func (s *MSSQLSink) EnsureTable(ctx context.Context, target contracts.TargetTable, columns []contracts.Column) error {
	exists, err := s.tableExists(ctx, target)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	var cols []string
	for _, c := range columns {
		cols = append(cols, fmt.Sprintf("[%s] %s %s",
			c.Name,
			mapToSQLType(c.DataType),
			nullability(c.Nullable),
		))
	}
	cols = append(cols, fmt.Sprintf("[%s] datetime2 NOT NULL", LoadTimestampColumn))

	stmt := fmt.Sprintf("CREATE TABLE [%s].[%s] (\n  %s\n);",
		target.Schema, target.Name, strings.Join(cols, ",\n  "))

	_, err = s.db.ExecContext(ctx, stmt)
	return err
}

func (s *MSSQLSink) Truncate(ctx context.Context, target contracts.TargetTable) error {
	stmt := fmt.Sprintf("TRUNCATE TABLE [%s].[%s]", target.Schema, target.Name)
	_, err := s.db.ExecContext(ctx, stmt)
	return err
}

// WriteBatch inserts rows using SQL Server bulk copy for efficiency.
func (s *MSSQLSink) WriteBatch(ctx context.Context, target contracts.TargetTable, batch contracts.Batch, loadTS time.Time) (int64, error) {
	if len(batch.Rows) == 0 {
		return 0, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	// Column list in the exact order we will pass values.
	colNames := make([]string, 0, len(batch.Columns)+1)
	for _, c := range batch.Columns {
		colNames = append(colNames, c.Name)
	}
	colNames = append(colNames, LoadTimestampColumn)

	fullName := fmt.Sprintf("[%s].[%s]", target.Schema, target.Name)

	var rowsWritten int64
	err = conn.Raw(func(driverConn any) error {
		bulkCopy, ok := driverConn.(interface {
			CreateBulk(table string, columns []string) *mssql.Bulk
		})
		if !ok {
			return fmt.Errorf("driver does not support bulk copy")
		}
		bulk := bulkCopy.CreateBulk(fullName, colNames)
		bulk.Options.KeepNulls = true

		for _, row := range batch.Rows {
			vals := make([]any, 0, len(colNames))
			for _, c := range batch.Columns {
				vals = append(vals, row[c.Name])
			}
			vals = append(vals, loadTS)
			if err := bulk.AddRow(vals); err != nil {
				return err
			}
			rowsWritten++
		}
		_, err := bulk.Done()
		return err
	})
	if err != nil {
		return 0, err
	}
	return rowsWritten, nil
}

// ---------- helpers ----------

func (s *MSSQLSink) tableExists(ctx context.Context, t contracts.TargetTable) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2
	`, t.Schema, t.Name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// mapToSQLType maps canonical types (from Source.Column.DataType) to T-SQL types.
func mapToSQLType(canonical string) string {
	switch strings.ToLower(canonical) {
	case "string":
		return "nvarchar(max)"
	case "int32":
		return "int"
	case "int64":
		return "bigint"
	case "float32":
		return "real"
	case "float64":
		return "float"
	case "bool":
		return "bit"
	case "datetime":
		return "datetime2"
	case "bytes":
		return "varbinary(max)"
	case "decimal":
		return "decimal(38,10)"
	default:
		return "nvarchar(max)"
	}
}

func nullability(nullable bool) string {
	if nullable {
		return "NULL"
	}
	return "NOT NULL"
}
