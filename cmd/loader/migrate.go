package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/slaghuis/data-loader/internal/migrate"
)

func runMigrate(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: loader migrate <up|down|status>")
		return 2
	}
	cmd := strings.ToLower(args[0])

	dsn := os.Getenv("LOADER_METADATA_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "LOADER_METADATA_DSN is required")
		return 1
	}

	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open metadata db: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ping metadata db: %v\n", err)
		return 1
	}

	r := migrate.New(db)
	switch cmd {
	case "up":
		if err := r.Up(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "migrate up: %v\n", err)
			return 1
		}
		ver, detail, err := r.Status(ctx)
		if err != nil {
			slog.Info("migrate up complete")
			return 0
		}
		fmt.Printf("migrate up complete\n%s\n", detail)
		_ = ver
		return 0
	case "down":
		if err := r.Down(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "migrate down: %v\n", err)
			return 1
		}
		fmt.Println("migrate down complete")
		return 0
	case "status":
		_, detail, err := r.Status(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "migrate status: %v\n", err)
			return 1
		}
		fmt.Println(detail)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown migrate command %q (want up|down|status)\n", cmd)
		return 2
	}
}
