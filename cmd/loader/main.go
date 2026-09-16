package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"

	"github.com/slaghuis/data-loader/internal/config"
	"github.com/slaghuis/data-loader/internal/logging"
	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/pipeline"
	"github.com/slaghuis/data-loader/internal/scheduler"
	"github.com/slaghuis/data-loader/internal/sink"

	_ "github.com/slaghuis/data-loader/internal/sources/sqlserver"
	_ "github.com/slaghuis/data-loader/internal/sources/postgres"
	modbussrc "github.com/slaghuis/data-loader/internal/sources/modbus"
	opcuasrc  "github.com/slaghuis/data-loader/internal/sources/opcua"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	// Bootstrap a console-only logger first so we can log startup issues
	// before the DB is up.
	bootLogger := logging.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(bootLogger)

	// Metadata DB
	gdb, err := gorm.Open(sqlserver.Open(cfg.MetadataDSN), &gorm.Config{})
	if err != nil {
		slog.Error("open metadata db", "err", err)
		os.Exit(1)
	}
	if err := metadata.AutoMigrate(gdb); err != nil {
		slog.Error("migrate metadata", "err", err)
		os.Exit(1)
	}
	repo := metadata.NewRepository(gdb)

	modbussrc.SetTagRepository(repo)
	opcuasrc.SetNodeRepository(repo)

	// Upgrade the default logger to also persist to the DB.
	baseHandler := slog.Default().Handler()
	dbHandler := logging.NewDBHandler(baseHandler, repo, 512)
	appLogger := slog.New(dbHandler)
	slog.SetDefault(appLogger)

	appLogger.Info("data-loader starting",
		"log_level", cfg.LogLevel,
		"log_format", cfg.LogFormat,
	)

	// Sink
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sk := sink.NewMSSQLSink(cfg.SinkDSN)
	if err := sk.Open(ctx); err != nil {
		appLogger.Error("open sink", "err", err)
		os.Exit(1)
	}
	defer sk.Close()

	// Pipeline & Scheduler
	runner := pipeline.NewRunner(repo, sk, appLogger)
	sch := scheduler.New(repo, runner, appLogger)
	if err := sch.Load(ctx); err != nil {
		appLogger.Error("load schedules", "err", err)
		os.Exit(1)
	}
	sch.Start()
	appLogger.Info("data-loader started")

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	appLogger.Info("shutdown signal received")

	sch.Stop()
	dbHandler.Close(5 * time.Second)
	appLogger.Info("data-loader stopped")
}
