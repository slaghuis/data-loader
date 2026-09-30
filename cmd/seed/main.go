package main

import (
    "context"
    "flag"
    "fmt"
    "log/slog"
    "os"
    "os/signal"
    "syscall"
    "time"

    "gorm.io/driver/sqlserver"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"

    "github.com/slaghuis/data-loader/internal/seed"
)

func main() {
    var file string
    flag.StringVar(&file, "file", "seeds/seed.yaml", "path to seed YAML file")
    flag.Parse()

    log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

    dsn := os.Getenv("LOADER_METADATA_DSN")
    if dsn == "" {
        log.Error("LOADER_METADATA_DSN is not set")
        os.Exit(2)
    }

    f, err := seed.Load(file)
    if err != nil {
        log.Error("load seed file", "file", file, "err", err)
        os.Exit(1)
    }
    log.Info("seed file parsed",
        "file", file,
        "sources", len(f.Sources),
        "loads", len(f.Loads),
    )

    db, err := gorm.Open(sqlserver.Open(dsn), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Warn),
    })
    if err != nil {
        log.Error("open metadata db", "err", err)
        os.Exit(1)
    }
    sqlDB, err := db.DB()
    if err != nil {
        log.Error("db handle", "err", err)
        os.Exit(1)
    }
    defer sqlDB.Close()

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    ctx, cancelTimeout := context.WithTimeout(ctx, 60*time.Second)
    defer cancelTimeout()

    rep, err := seed.Apply(ctx, db, f)
    if err != nil {
        log.Error("apply seed", "err", err)
        os.Exit(1)
    }

    log.Info("seed applied",
        "sources_created", rep.SourcesCreated,
        "sources_updated", rep.SourcesUpdated,
        "loads_created", rep.LoadsCreated,
        "loads_updated", rep.LoadsUpdated,
        "modbus_tags_written", rep.ModbusTags,
        "opcua_nodes_written", rep.OPCUANodes,
    )
    fmt.Fprintln(os.Stderr, "OK")
}
