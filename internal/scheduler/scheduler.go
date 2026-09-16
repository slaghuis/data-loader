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

type Scheduler struct {
	cron    *cron.Cron
	repo    *metadata.Repository
	runner  *pipeline.Runner
	logger  *slog.Logger
	state *runstate.Registry
	entries map[int64]cron.EntryID
	running map[int64]bool
	mu      sync.Mutex
}

func New(repo *metadata.Repository, runner *pipeline.Runner, state *runstate.Registry, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		cron:    cron.New(),
		repo:    repo,
		runner:  runner,
		logger:  logger,
		state: state,
		entries: map[int64]cron.EntryID{},
		running: map[int64]bool{},
	}
}

func (s *Scheduler) Load(ctx context.Context) error {
	loads, err := s.repo.ListEnabledLoads(ctx)
	if err != nil {
		return err
	}
	for _, ld := range loads {
		ld := ld
		id, err := s.cron.AddFunc(ld.CronExpression, func() { s.trigger(ld) })
		if err != nil {
			s.logger.Error("register schedule",
				"category", "schedule",
				"load_id", ld.ID,
				"load_name", ld.Name,
				"cron", ld.CronExpression,
				"err", err,
			)
			return err
		}
		s.entries[ld.ID] = id
		s.logger.Info("schedule registered",
			"category", "schedule",
			"load_id", ld.ID,
			"load_name", ld.Name,
			"cron", ld.CronExpression,
		)
		s.state.Register(runstate.LoadState{
    		LoadID:         ld.ID,
    		LoadName:       ld.Name,
    		SourceName:     ld.Source.Name,
    		SourceKind:     ld.Source.Kind,
    		CronExpression: ld.CronExpression,
		})
	}
	return nil
}

func (s *Scheduler) trigger(load metadata.Load) {
	s.mu.Lock()
	if s.running[load.ID] {
		s.mu.Unlock()
		s.logger.Warn("skipping overlapping run",
			"category", "schedule",
			"load_id", load.ID,
			"load_name", load.Name,
		)
		return
	}
	s.running[load.ID] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.running, load.ID)
		s.mu.Unlock()
	}()

	if err := s.runner.Execute(context.Background(), load); err != nil {
		// The runner already logged specifics; nothing to add here.
		_ = err
	}
}

func (s *Scheduler) Start() { s.cron.Start() }
func (s *Scheduler) Stop()  { s.cron.Stop() }
