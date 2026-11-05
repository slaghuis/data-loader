package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"

	"github.com/slaghuis/data-loader/migrations"
)

const baselineVersion int64 = 1

// Runner applies embedded goose migrations against a metadata SQL Server database.
type Runner struct {
	db   *sql.DB
	fsys fs.FS
}

// New opens a runner over the embedded migrations FS.
func New(db *sql.DB) *Runner {
	return &Runner{db: db, fsys: migrations.FS}
}

// NewWithFS is for tests that inject an alternate filesystem.
func NewWithFS(db *sql.DB, fsys fs.FS) *Runner {
	return &Runner{db: db, fsys: fsys}
}

func (r *Runner) provider() (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectMSSQL, r.db, r.fsys)
}

// Up stamps an existing AutoMigrate database as baseline version 1 when needed,
// then applies every pending migration.
func (r *Runner) Up(ctx context.Context) error {
	if err := r.baselineIfNeeded(ctx); err != nil {
		return err
	}
	p, err := r.provider()
	if err != nil {
		return fmt.Errorf("migrate provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Down rolls back a single migration.
func (r *Runner) Down(ctx context.Context) error {
	p, err := r.provider()
	if err != nil {
		return fmt.Errorf("migrate provider: %w", err)
	}
	if _, err := p.Down(ctx); err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// Status returns the current goose DB version and a human-readable summary.
func (r *Runner) Status(ctx context.Context) (version int64, detail string, err error) {
	p, err := r.provider()
	if err != nil {
		return 0, "", fmt.Errorf("migrate provider: %w", err)
	}
	version, err = p.GetDBVersion(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("migrate status: %w", err)
	}
	results, err := p.Status(ctx)
	if err != nil {
		return version, "", fmt.Errorf("migrate status list: %w", err)
	}
	detail = fmt.Sprintf("version=%d migrations=%d", version, len(results))
	for _, res := range results {
		state := "pending"
		if res.State == goose.StateApplied {
			state = "applied"
		}
		detail += fmt.Sprintf("\n  %d %s [%s]", res.Source.Version, res.Source.Path, state)
	}
	return version, detail, nil
}

// baselineIfNeeded detects a pre-goose AutoMigrate database (meta_sources present,
// goose version still 0) and records version 1 without re-running 00001 DDL.
// Fresh databases skip this and let Up create the schema via 00001.
func (r *Runner) baselineIfNeeded(ctx context.Context) error {
	hasMeta, err := tableExists(ctx, r.db, "meta_sources")
	if err != nil {
		return err
	}
	if !hasMeta {
		return nil
	}

	p, err := r.provider()
	if err != nil {
		return err
	}
	version, err := p.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read goose version: %w", err)
	}
	if version >= baselineVersion {
		return nil
	}

	// ApplyVersion(true) records the version as applied. For an existing
	// AutoMigrate schema we still prefer not to re-execute 00001; however
	// goose's ApplyVersion runs the migration SQL. 00001 is idempotent
	// (IF OBJECT_ID … IS NULL), so ApplyVersion is safe and stamps version 1.
	if _, err := p.ApplyVersion(ctx, baselineVersion, true); err != nil {
		return fmt.Errorf("stamp baseline version %d: %w", baselineVersion, err)
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = 'dbo' AND TABLE_NAME = @p1
	`, name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("detect table %s: %w", name, err)
	}
	return n > 0, nil
}
