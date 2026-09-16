package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	mssql "github.com/microsoft/go-mssqldb"
	_ "github.com/microsoft/go-mssqldb"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"

	"github.com/yourorg/data-loader/internal/config"
	"github.com/yourorg/data-loader/internal/metadata"
	"github.com/yourorg/data-loader/internal/pipeline"
	"github.com/yourorg/data-loader/internal/scheduler"
	"github.com/yourorg/data-loader/internal/sink"
	// Blank-import to register source modules.
	_ "github.com/yourorg/data-loader/internal/sources/sqlserver"
)

var _ = mssql.Driver{} // ensure driver linkage

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Metadata DB via GORM
	gdb, err := gorm.Open(sqlserver.Open(cfg.MetadataDSN), &gorm.Config{})
	if err != nil {
		log.Fatalf("open metadata db: %v", err)
	}
	if err := metadata.AutoMigrate(gdb); err != nil {
		log.Fatalf("migrate metadata: %v", err)
	}
	repo := metadata.NewRepository(gdb)

	// Sink
	sk := sink.NewMSSQLSink(cfg.SinkDSN)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sk.Open(ctx); err != nil {
		log.Fatalf("open sink: %v", err)
	}
	defer sk.Close()

	// Pipeline & Scheduler
	runner := pipeline.NewRunner(repo, sk)
	sch := scheduler.New(repo, runner)
	if err := sch.Load(ctx); err != nil {
		log.Fatalf("load schedules: %v", err)
	}
	sch.Start()
	log.Println("data-loader started")

	// Wait for shutdown signal
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	log.Println("shutting down...")
	sch.Stop()
}