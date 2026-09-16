package scheduler

import (
	"context"
	"sync"

	"github.com/robfig/cron/v3"
	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/pipeline"
)

type Scheduler struct {
	cron    *cron.Cron
	repo    *metadata.Repository
	runner  *pipeline.Runner
	entries map[int64]cron.EntryID
	running map[int64]bool
	mu      sync.Mutex
}

func New(repo *metadata.Repository, runner *pipeline.Runner) *Scheduler {
	return &Scheduler{
		cron:    cron.New(),
		repo:    repo,
		runner:  runner,
		entries: map[int64]cron.EntryID{},
		running: map[int64]bool{},
	}
}

// Load reads all enabled loads and registers their schedules.
func (s *Scheduler) Load(ctx context.Context) error {
	loads, err := s.repo.ListEnabledLoads(ctx)
	if err != nil {
		return err
	}
	for _, ld := range loads {
		ld := ld // capture
		id, err := s.cron.AddFunc(ld.CronExpression, func() {
			s.trigger(ld)
		})
		if err != nil {
			return err
		}
		s.entries[ld.ID] = id
	}
	return nil
}

func (s *Scheduler) trigger(load metadata.Load) {
	s.mu.Lock()
	if s.running[load.ID] {
		s.mu.Unlock()
		return // skip overlapping run
	}
	s.running[load.ID] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.running, load.ID)
		s.mu.Unlock()
	}()

	ctx := context.Background()
	_ = s.runner.Execute(ctx, load)
}

func (s *Scheduler) Start() { s.cron.Start() }
func (s *Scheduler) Stop()  { s.cron.Stop() }
