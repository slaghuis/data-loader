package migrate_test

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/slaghuis/data-loader/migrations"
)

func TestEmbeddedMigrations_ListSources(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var sqlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	if len(sqlFiles) < 2 {
		t.Fatalf("expected at least 2 sql migrations, got %v", sqlFiles)
	}

	// Provider can parse the embedded FS without a live database for ListSources.
	// NewProvider requires a DB; use goose.CollectMigrations via parsing files.
	for _, name := range sqlFiles {
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		if !strings.Contains(body, "-- +goose Up") {
			t.Fatalf("%s missing Up directive", name)
		}
		if !strings.Contains(body, "-- +goose Down") {
			t.Fatalf("%s missing Down directive", name)
		}
		if !strings.Contains(body, "-- +goose StatementBegin") {
			t.Fatalf("%s missing StatementBegin (required for T-SQL batches)", name)
		}
		if path.Ext(name) != ".sql" {
			t.Fatalf("unexpected name %s", name)
		}
	}

	// Sanity: dialect constant exists for MSSQL (compile-time coupling to goose).
	if goose.DialectMSSQL == "" {
		t.Fatal("DialectMSSQL empty")
	}
}

func TestBaselineAndFKMigrationNames(t *testing.T) {
	want := map[string]bool{
		"00001_baseline_automigrate_schema.sql":       false,
		"00002_foreign_keys_and_run_status_index.sql": false,
		"00003_backfill_columns.sql":                  false,
	}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := want[e.Name()]; ok {
			want[e.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing embedded migration %s", name)
		}
	}
}
