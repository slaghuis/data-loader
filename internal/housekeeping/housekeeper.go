package housekeeping

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/slaghuis/data-loader/internal/metadata"
)

// Config controls housekeeping behaviour. All durations are in days for retention.
type Config struct {
	CronExpression       string
	LogRetentionDays     int
	LoadRunRetentionDays int
	BatchSize            int
	BatchPause           time.Duration
}

// Housekeeper runs periodic retention over metadata tables.
type Housekeeper struct {
	repo   *metadata.Repository
	cfg    Config
	logger *slog.Logger
	cron   *cron.Cron

	mu      sync.Mutex
	running bool
}

func New(repo *metadata.Repository, cfg Config, logger *slog.Logger) *Housekeeper {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 5000
	}
	return &Housekeeper{
		repo:   repo,
		cfg:    cfg,
		logger: logger,
		cron:   cron.New(),
	}
}

// Start registers the housekeeping job on cron. It does not run immediately;
// call RunNow() if you want an at-startup sweep.
func (h *Housekeeper) Start() error {
	_, err := h.cron.AddFunc(h.cfg.CronExpression, func() { h.tick() })
	if err != nil {
		return err
	}
	h.cron.Start()
	h.logger.Info("housekeeping scheduled",
		"category", "housekeeping",
		"cron", h.cfg.CronExpression,
		"log_retention_days", h.cfg.LogRetentionDays,
		"load_run_retention_days", h.cfg.LoadRunRetentionDays,
	)
	return nil
}

// Stop halts the housekeeping cron. In-flight sweeps finish; the returned
// context waits for them.
func (h *Housekeeper) Stop() context.Context {
	return h.cron.Stop()
}

// RunNow triggers an immediate sweep. Safe to call concurrently with the cron
// schedule; it will skip if a sweep is already in progress.
func (h *Housekeeper) RunNow(ctx context.Context) {
	h.execute(ctx)
}

// tick is invoked by cron on its own goroutine.
func (h *Housekeeper) tick() {
	// Fresh context per invocation — housekeeping should not inherit anything.
	h.execute(context.Background())
}

func (h *Housekeeper) execute(ctx context.Context) {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		h.logger.Warn("skipping housekeeping — previous sweep still in progress",
			"category", "housekeeping")
		return
	}
	h.running = true
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		h.running = false
		h.mu.Unlock()
	}()

	start := time.Now()
	h.logger.Info("housekeeping started", "category", "housekeeping")

	// Log entries.
	if h.cfg.LogRetentionDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -h.cfg.LogRetentionDays)
		deleted, err := h.repo.PruneLogEntries(ctx, cutoff, h.cfg.BatchSize, h.cfg.BatchPause)
		if err != nil {
			h.logger.Error("prune log entries",
				"category", "housekeeping",
				"cutoff", cutoff,
				"deleted", deleted,
				"err", err,
			)
		} else {
			h.logger.Info("pruned log entries",
				"category", "housekeeping",
				"cutoff", cutoff,
				"deleted", deleted,
			)
		}
	}

	// Load runs.
	if h.cfg.LoadRunRetentionDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -h.cfg.LoadRunRetentionDays)
		deleted, err := h.repo.PruneLoadRuns(ctx, cutoff, h.cfg.BatchSize, h.cfg.BatchPause)
		if err != nil {
			h.logger.Error("prune load runs",
				"category", "housekeeping",
				"cutoff", cutoff,
				"deleted", deleted,
				"err", err,
			)
		} else {
			h.logger.Info("pruned load runs",
				"category", "housekeeping",
				"cutoff", cutoff,
				"deleted", deleted,
			)
		}
	}

	h.logger.Info("housekeeping finished",
		"category", "housekeeping",
		"duration_ms", time.Since(start).Milliseconds(),
	)
}