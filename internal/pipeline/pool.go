package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/slaghuis/data-loader/pkg/contracts"
)

// SourcePool caches opened sources keyed by source_id (ADR-021).
type SourcePool struct {
	build  SourceBuilder
	idle   time.Duration
	maxAge time.Duration
	logger *slog.Logger

	mu      sync.Mutex
	entries map[int64]*poolEntry
}

type poolEntry struct {
	mu       sync.Mutex // held for the duration of a lease
	src      contracts.Source
	fp       string
	openedAt time.Time
	lastUsed time.Time
}

// PoolConfig tunes idle eviction and hard max age.
type PoolConfig struct {
	IdleTimeout time.Duration // close after this idle; 0 means 5m
	MaxAge      time.Duration // force reconnect after this age; 0 means disabled
}

// NewSourcePool builds a pool. build must be non-nil.
func NewSourcePool(build SourceBuilder, cfg PoolConfig, logger *slog.Logger) *SourcePool {
	if build == nil {
		panic("SourcePool requires a SourceBuilder")
	}
	if logger == nil {
		logger = slog.Default()
	}
	idle := cfg.IdleTimeout
	if idle <= 0 {
		idle = 5 * time.Minute
	}
	return &SourcePool{
		build:   build,
		idle:    idle,
		maxAge:  cfg.MaxAge,
		logger:  logger,
		entries: map[int64]*poolEntry{},
	}
}

func (p *SourcePool) Obtain(ctx context.Context, cfg contracts.SourceConfig) (contracts.Source, func(bool), error) {
	fp := ParamsFingerprint(cfg.SourceType, cfg.Params)

	p.mu.Lock()
	ent, ok := p.entries[cfg.SourceID]
	if !ok {
		ent = &poolEntry{}
		p.entries[cfg.SourceID] = ent
	}
	p.mu.Unlock()

	ent.mu.Lock()

	now := time.Now()
	if ent.src != nil && !p.reusable(ent, fp, now) {
		p.closeEntry(ent)
	}

	if ent.src == nil {
		src, err := p.build(cfg)
		if err != nil {
			ent.mu.Unlock()
			return nil, nil, err
		}
		if err := src.Open(ctx); err != nil {
			_ = src.Close()
			ent.mu.Unlock()
			return nil, nil, err
		}
		ent.src = src
		ent.fp = fp
		ent.openedAt = now
		ent.lastUsed = now
		p.logger.Debug("source pool opened",
			"category", "connect",
			"source_id", cfg.SourceID,
			"kind", cfg.SourceType,
		)
	}

	src := ent.src
	released := false
	release := func(invalidate bool) {
		if released {
			return
		}
		released = true
		if invalidate {
			p.closeEntry(ent)
			p.logger.Debug("source pool invalidated",
				"category", "connect",
				"source_id", cfg.SourceID,
			)
		} else {
			ent.lastUsed = time.Now()
		}
		ent.mu.Unlock()
	}
	return src, release, nil
}

func (p *SourcePool) reusable(ent *poolEntry, fp string, now time.Time) bool {
	if ent.fp != fp {
		return false
	}
	if p.idle > 0 && now.Sub(ent.lastUsed) > p.idle {
		return false
	}
	if p.maxAge > 0 && now.Sub(ent.openedAt) > p.maxAge {
		return false
	}
	return true
}

func (p *SourcePool) closeEntry(ent *poolEntry) {
	if ent.src == nil {
		return
	}
	_ = ent.src.Close()
	ent.src = nil
	ent.fp = ""
}

// Invalidate removes and closes the pooled connection for sourceID.
func (p *SourcePool) Invalidate(sourceID int64) {
	p.mu.Lock()
	ent := p.entries[sourceID]
	p.mu.Unlock()
	if ent == nil {
		return
	}
	ent.mu.Lock()
	defer ent.mu.Unlock()
	p.closeEntry(ent)
}

// InvalidateAll closes every pooled connection (e.g. after metadata reload).
func (p *SourcePool) InvalidateAll() {
	p.mu.Lock()
	ids := make([]int64, 0, len(p.entries))
	for id := range p.entries {
		ids = append(ids, id)
	}
	p.mu.Unlock()
	for _, id := range ids {
		p.Invalidate(id)
	}
}

// Close implements SourceSession.
func (p *SourcePool) Close() error {
	p.InvalidateAll()
	return nil
}

// PingAll pings every live pooled source.
func (p *SourcePool) PingAll(ctx context.Context) error {
	p.mu.Lock()
	ents := make([]*poolEntry, 0, len(p.entries))
	ids := make([]int64, 0, len(p.entries))
	for id, ent := range p.entries {
		ents = append(ents, ent)
		ids = append(ids, id)
	}
	p.mu.Unlock()

	for i, ent := range ents {
		ent.mu.Lock()
		var err error
		if ent.src != nil {
			err = ent.src.Ping(ctx)
		}
		ent.mu.Unlock()
		if err != nil {
			return fmt.Errorf("source_id %d: %w", ids[i], err)
		}
	}
	return nil
}

// Len returns how many source slots exist (including empty slots). For tests.
func (p *SourcePool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// LiveCount returns how many entries currently hold an open source. For tests.
func (p *SourcePool) LiveCount() int {
	p.mu.Lock()
	ents := make([]*poolEntry, 0, len(p.entries))
	for _, e := range p.entries {
		ents = append(ents, e)
	}
	p.mu.Unlock()
	n := 0
	for _, e := range ents {
		e.mu.Lock()
		if e.src != nil {
			n++
		}
		e.mu.Unlock()
	}
	return n
}
