package scheduler

import (
	"context"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/pipeline"
	"github.com/slaghuis/data-loader/internal/runstate"
)

type scheduledLoad struct {
	load    metadata.Load
	entryID cron.EntryID
}

type Scheduler struct {
	cron    *cron.Cron
	repo    *metadata.Repository
	runner  *pipeline.Runner
	state   *runstate.Registry
	logger  *slog.Logger

	mu          sync.Mutex
	scheduled   map[int64]*scheduledLoad // by load ID
	running     map[int64]bool
	fingerprint string
}

func New(repo *metadata.Repository, runner *pipeline.Runner, state *runstate.Registry, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		cron:      cron.New(),
		repo:      repo,
		runner:    runner,
		state:     state,
		logger:    logger,
		scheduled: map[int64]*scheduledLoad{},
		running:   map[int64]bool{},
	}
}

// Load registers all enabled loads at startup.
func (s *Scheduler) Load(ctx context.Context) error {
	return s.Reload(ctx)
}

// Reload synchronises cron entries with the current metadata state.
// Safe to call at any time; a no-op if nothing has changed.
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

	// 1. Remove entries no longer present or now disabled.
	for id, existing := range s.scheduled {
		if _, ok := desiredByID[id]; !ok {
			s.cron.Remove(existing.entryID)
			delete(s.scheduled, id)
			removed = append(removed, id)
		}
	}

	// 2. Add or update.
	for id, ld := range desiredByID {
		ld := ld // capture
		if existing, ok := s.scheduled[id]; ok {
			// Update in place if cron expression changed. Other fields (mode, watermark,
			// object_name, target_*) take effect naturally on the next run because the
			// runner re-reads the load each tick.
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
				// Keep the entryID; refresh the cached load so trigger uses the latest.
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

	// 3. Refresh the runstate registry to match the current desired set.
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

// trigger fires a load by ID; it looks up the *current* load to survive updates
// that happen between scheduling and firing.
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

	defer func() {
		s.mu.Lock()
		delete(s.running, loadID)
		s.mu.Unlock()
	}()

	if err := s.runner.Execute(context.Background(), load); err != nil {
		_ = err // runner already logged
	}
}

func (s *Scheduler) Start() { s.cron.Start() }
func (s *Scheduler) Stop()  { s.cron.Stop() }
