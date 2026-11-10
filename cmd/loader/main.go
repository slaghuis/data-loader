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

	"github.com/slaghuis/data-loader/internal/backfill"
	"github.com/slaghuis/data-loader/internal/config"
	"github.com/slaghuis/data-loader/internal/health"
	"github.com/slaghuis/data-loader/internal/housekeeping"
	"github.com/slaghuis/data-loader/internal/logging"
	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/metrics"
	"github.com/slaghuis/data-loader/internal/pipeline"
	"github.com/slaghuis/data-loader/internal/reload"
	"github.com/slaghuis/data-loader/internal/runstate"
	"github.com/slaghuis/data-loader/internal/scheduler"
	"github.com/slaghuis/data-loader/internal/sink"
	"github.com/slaghuis/data-loader/internal/sources"

	_ "github.com/slaghuis/data-loader/internal/sources/modbus"
	_ "github.com/slaghuis/data-loader/internal/sources/opcua"
	_ "github.com/slaghuis/data-loader/internal/sources/postgres"
	_ "github.com/slaghuis/data-loader/internal/sources/sqlserver"
)

// version is injected at link time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(runMigrate(os.Args[2:]))
	}
	os.Exit(runServer())
}

func runServer() int {
	cfg, err := config.FromEnv()
	if err != nil {
		slog.Error("config load failed", "err", err)
		return 1
	}

	// Bootstrap a console-only logger first so we can log startup issues
	// before the DB is up.
	bootLogger := logging.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(bootLogger)

	// Metadata DB — schema is managed by `loader migrate up` (ADR-012), not AutoMigrate.
	gdb, err := gorm.Open(sqlserver.Open(cfg.MetadataDSN), &gorm.Config{})
	if err != nil {
		slog.Error("open metadata db", "err", err)
		return 1
	}
	repo := metadata.NewRepository(gdb)

	// Upgrade the default logger to also persist to the DB.
	baseHandler := slog.Default().Handler()
	dbHandler := logging.NewDBHandler(baseHandler, repo, 512)
	metrics.RegisterDBLogDropped(dbHandler.Dropped)
	appLogger := slog.New(dbHandler)
	slog.SetDefault(appLogger)

	appLogger.Info("data-loader starting",
		"log_level", cfg.LogLevel,
		"log_format", cfg.LogFormat,
		"version", version,
	)

	// Sink
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sk := sink.NewMSSQLSink(cfg.SinkDSN)
	if err := sk.Open(ctx); err != nil {
		appLogger.Error("open sink", "err", err)
		return 1
	}
	defer func() {
		if err := sk.Close(); err != nil {
			appLogger.Error("close sink", "err", err)
		}
	}()

	// Run state registry
	state := runstate.New()

	// Source sessions: ephemeral (default) or pooled (ADR-021).
	var pool *pipeline.SourcePool
	var sessions pipeline.SourceSession
	if cfg.SourcePoolEnabled {
		pool = pipeline.NewSourcePool(sources.Build, pipeline.PoolConfig{
			IdleTimeout: time.Duration(cfg.SourcePoolIdleSec) * time.Second,
			MaxAge:      time.Duration(cfg.SourcePoolMaxAgeSec) * time.Second,
		}, appLogger)
		sessions = pool
		appLogger.Info("source connection pool enabled",
			"category", "connect",
			"idle_sec", cfg.SourcePoolIdleSec,
			"max_age_sec", cfg.SourcePoolMaxAgeSec,
		)
	} else {
		sessions = pipeline.NewEphemeral(sources.Build)
	}
	defer func() { _ = sessions.Close() }()

	// Pipeline & Scheduler (repo satisfies TelemetryCatalog for modbus/opcua).
	runner := pipeline.NewRunner(repo, repo, sk, state, appLogger, sessions)
	bf := backfill.NewCoordinator(repo, &backfill.SourceMinResolver{
		Build:     sources.Build,
		Telemetry: repo,
	}, appLogger)
	runner.SetBackfillHook(bf)
	if err := bf.Sync(ctx); err != nil {
		appLogger.Error("backfill sync", "err", err)
		return 1
	}
	sch := scheduler.New(repo, runner, state, appLogger,
		time.Duration(cfg.ShutdownTimeoutSec)*time.Second)
	if err := sch.Load(ctx); err != nil {
		appLogger.Error("load schedules", "err", err)
		return 1
	}
	sch.Start()
	appLogger.Info("data-loader started")

	// Health server
	var hs *health.Server
	if cfg.HealthEnabled {
		var extras []health.ReadyCheck
		if pool != nil {
			extras = append(extras, health.ReadyCheck{
				Name:  "source_pool",
				Check: pool.PingAll,
			})
		}
		hs = health.New(cfg.HealthAddr, state, gdb, sk.DB(), version, appLogger, extras...)
		if err := hs.Start(); err != nil {
			appLogger.Error("start health server", "err", err)
			return 1
		}
	}

	// Metadata reload coordinator: backfill sync → scheduler; invalidate pooled sources.
	var reloader *reload.Coordinator
	if cfg.ReloadEnabled {
		seq := &reload.Sequence{Steps: []reload.Reloader{bf, sch}}
		var reloadTarget reload.Reloader = seq
		if pool != nil {
			reloadTarget = &reload.Fanout{
				Primary: seq,
				After:   []func(){pool.InvalidateAll},
			}
		}
		reloader = reload.New(reloadTarget, reload.Config{
			Interval:  time.Duration(cfg.ReloadIntervalSec) * time.Second,
			HandleHUP: cfg.ReloadOnSIGHUP,
		}, appLogger)
		reloader.Start()
	}

	// Housekeeping
	var hk *housekeeping.Housekeeper
	if cfg.HousekeepingEnabled {
		hk = housekeeping.New(repo, housekeeping.Config{
			CronExpression:       cfg.HousekeepingCron,
			LogRetentionDays:     cfg.LogRetentionDays,
			LoadRunRetentionDays: cfg.LoadRunRetentionDays,
			BatchSize:            cfg.HousekeepingBatchSize,
			BatchPause:           time.Duration(cfg.HousekeepingBatchPauseMs) * time.Millisecond,
		}, appLogger)

		if err := hk.Start(); err != nil {
			appLogger.Error("start housekeeping", "err", err)
			return 1
		}
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	appLogger.Info("shutdown signal received")

	if reloader != nil {
		reloader.Stop()
	}
	sch.Stop()
	if hk != nil {
		stopCtx := hk.Stop()
		select {
		case <-stopCtx.Done():
		case <-time.After(30 * time.Second):
			appLogger.Warn("housekeeping did not stop in time")
		}
	}
	if hs != nil {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		_ = hs.Stop(shutdownCtx)
		cancelShutdown()
	}
	dbHandler.Close(5 * time.Second)
	appLogger.Info("data-loader stopped")
	return 0
}
