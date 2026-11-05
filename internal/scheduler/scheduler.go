package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/pipeline"
	"github.com/slaghuis/data-loader/internal/runstate"
)

type scheduledLoad struct {
	load    metadata.Load
	entryID cron.EntryID
}

// RunAborter marks orphaned or timed-out runs as aborted (D-09).
type RunAborter interface {
	AbortRunningRuns(ctx context.Context, reason string) (int64, error)
	ListEnabledLoads(ctx context.Context) ([]metadata.Load, error)
	MetadataFingerprint(ctx context.Context) (string, error)
}

// Scheduler registers cron entries for enabled loads and runs them via Runner.
type Scheduler struct {
	cron            *cron.Cron
	repo            RunAborter
	runner          *pipeline.Runner
	state           *runstate.Registry
	logger          *slog.Logger
	shutdownTimeout time.Duration

	runCtx    context.Context
	runCancel context.CancelFunc

	mu          sync.Mutex
	scheduled   map[int64]*scheduledLoad
	running     map[int64]bool
	fingerprint string
	wg          sync.WaitGroup
}

// New builds a scheduler. shutdownTimeout bounds Stop's wait for in-flight runs.
func New(repo RunAborter, runner *pipeline.Runner, state *runstate.Registry, logger *slog.Logger, shutdownTimeout time.Duration) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	if shutdownTimeout <= 0 {
		shutdownTimeout = 60 * time.Second
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	return &Scheduler{
		cron:            cron.New(),
		repo:            repo,
		runner:          runner,
		state:           state,
		logger:          logger,
		shutdownTimeout: shutdownTimeout,
		runCtx:          runCtx,
		runCancel:       runCancel,
		scheduled:       map[int64]*scheduledLoad{},
		running:         map[int64]bool{},
	}
}

// Load registers all enabled loads at startup and aborts orphaned running rows.
func (s *Scheduler) Load(ctx context.Context) error {
	n, err := s.repo.AbortRunningRuns(ctx, "aborted: orphaned running row at startup")
	if err != nil {
		return err
	}
	if n > 0 {
		s.logger.Warn("aborted orphaned running load runs",
			"category", "schedule",
			"count", n,
		)
	}
	return s.Reload(ctx)
}

// Reload synchronises cron entries with the current metadata state.
func (s *Scheduler) Reload(ctx context.Context) error {
	fp, err := s.repo.MetadataFingerprint(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	unchanged := fp == s.fingerprint
	s.mu.Unlock()
	if unchanged {
		s.logger.Debug("reload skipped (no changes)", "category", "schedule")
		return nil
	}

	desired, err := s.repo.ListEnabledLoads(ctx)
	if err != nil {
		return err
	}

	desiredByID := make(map[int64]metadata.Load, len(desired))
	for _, ld := range desired {
		desiredByID[ld.ID] = ld
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var added, removed, updated []int64

	for id, existing := range s.scheduled {
		if _, ok := desiredByID[id]; !ok {
			s.cron.Remove(existing.entryID)
			delete(s.scheduled, id)
			removed = append(removed, id)
		}
	}

	for id, ld := range desiredByID {
		ld := ld
		if existing, ok := s.scheduled[id]; ok {
			if existing.load.CronExpression != ld.CronExpression {
				s.cron.Remove(existing.entryID)
				entryID, err := s.cron.AddFunc(ld.CronExpression, func() { s.trigger(ld.ID) })
				if err != nil {
					s.logger.Error("update schedule",
						"category", "schedule",
						"load_id", id,
						"load_name", ld.Name,
						"cron", ld.CronExpression,
						"err", err,
					)
					continue
				}
				s.scheduled[id] = &scheduledLoad{load: ld, entryID: entryID}
				updated = append(updated, id)
			} else {
				existing.load = ld
			}
			continue
		}

		entryID, err := s.cron.AddFunc(ld.CronExpression, func() { s.trigger(ld.ID) })
		if err != nil {
			s.logger.Error("register schedule",
				"category", "schedule",
				"load_id", id,
				"load_name", ld.Name,
				"cron", ld.CronExpression,
				"err", err,
			)
			continue
		}
		s.scheduled[id] = &scheduledLoad{load: ld, entryID: entryID}
		added = append(added, id)
	}

	for _, ld := range desired {
		s.state.Register(runstate.LoadState{
			LoadID:         ld.ID,
			LoadName:       ld.Name,
			SourceName:     ld.Source.Name,
			SourceKind:     ld.Source.Kind,
			CronExpression: ld.CronExpression,
		})
	}

	s.fingerprint = fp

	s.logger.Info("metadata reload complete",
		"category", "schedule",
		"total_loads", len(s.scheduled),
		"added", added,
		"removed", removed,
		"updated", updated,
	)
	return nil
}

func (s *Scheduler) trigger(loadID int64) {
	s.mu.Lock()
	entry, ok := s.scheduled[loadID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if s.running[loadID] {
		s.mu.Unlock()
		s.logger.Warn("skipping overlapping run",
			"category", "schedule",
			"load_id", loadID,
			"load_name", entry.load.Name,
		)
		return
	}
	s.running[loadID] = true
	load := entry.load
	s.mu.Unlock()

	s.wg.Add(1)
	defer func() {
		s.mu.Lock()
		delete(s.running, loadID)
		s.mu.Unlock()
		s.wg.Done()
	}()

	if err := s.runner.Execute(s.runCtx, load); err != nil {
		_ = err
	}
}

// Start begins the cron loop.
func (s *Scheduler) Start() { s.cron.Start() }

// Stop cancels in-flight run contexts, waits up to shutdownTimeout, then aborts
// any runs still marked running in metadata (D-09).
func (s *Scheduler) Stop() {
	_ = s.cron.Stop()
	s.runCancel()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(s.shutdownTimeout):
		s.logger.Warn("shutdown timeout waiting for in-flight runs",
			"category", "schedule",
			"timeout", s.shutdownTimeout.String(),
		)
	}

	n, err := s.repo.AbortRunningRuns(context.Background(), "aborted: shutdown timeout")
	if err != nil {
		s.logger.Error("abort running runs on stop", "category", "schedule", "err", err)
		return
	}
	if n > 0 {
		s.logger.Warn("aborted running load runs after stop",
			"category", "schedule",
			"count", n,
		)
	}
}
