package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/slaghuis/data-loader/pkg/contracts"
)

// replaceSession tracks an optional AtomicReplacer staging swap (ADR-020).
type replaceSession struct {
	atomic    contracts.AtomicReplacer
	live      contracts.TargetTable
	writeTo   contracts.TargetTable // live or staging
	staging   contracts.TargetTable
	active    bool
	committed bool
	legacy    bool // truncate-before-read fallback when sink lacks AtomicReplacer
}

func newReplaceSession(sink contracts.Sink, live contracts.TargetTable) *replaceSession {
	s := &replaceSession{live: live, writeTo: live}
	if ar, ok := sink.(contracts.AtomicReplacer); ok {
		s.atomic = ar
	} else {
		s.legacy = true
	}
	return s
}

// prepareExisting begins staging when the live table already exists so a
// zero-row source still clears it on commit (D-05 + ADR-020).
func (s *replaceSession) prepareExisting(ctx context.Context, sink contracts.Sink, log *slog.Logger) error {
	if s.legacy {
		exists, err := sink.TableExists(ctx, s.live)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if err := sink.Truncate(ctx, s.live); err != nil {
			return fmt.Errorf("truncate target: %w", err)
		}
		log.Info("target truncated (legacy replace)", "category", "write")
		return nil
	}

	exists, err := sink.TableExists(ctx, s.live)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	stg, err := s.atomic.BeginReplace(ctx, s.live, nil)
	if err != nil {
		return fmt.Errorf("begin replace: %w", err)
	}
	s.staging = stg
	s.writeTo = stg
	s.active = true
	log.Info("replace staging prepared",
		"category", "write",
		"staging", fmt.Sprintf("%s.%s", stg.Schema, stg.Name),
	)
	return nil
}

// ensureFirstBatch creates staging (or EnsureTable) when the first batch arrives.
func (s *replaceSession) ensureFirstBatch(ctx context.Context, sink contracts.Sink, autoCreate bool, cols []contracts.Column, log *slog.Logger) error {
	if s.legacy {
		if autoCreate {
			if err := sink.EnsureTable(ctx, s.live, cols); err != nil {
				return fmt.Errorf("ensure target: %w", err)
			}
			log.Debug("target ensured",
				"category", "write",
				"target", fmt.Sprintf("%s.%s", s.live.Schema, s.live.Name),
			)
		}
		return nil
	}

	if s.active {
		return nil
	}
	stg, err := s.atomic.BeginReplace(ctx, s.live, cols)
	if err != nil {
		return fmt.Errorf("begin replace: %w", err)
	}
	s.staging = stg
	s.writeTo = stg
	s.active = true
	log.Info("replace staging prepared",
		"category", "write",
		"staging", fmt.Sprintf("%s.%s", stg.Schema, stg.Name),
	)
	return nil
}

func (s *replaceSession) commit(ctx context.Context, log *slog.Logger) error {
	if s.legacy || !s.active {
		return nil
	}
	if err := s.atomic.CommitReplace(ctx, s.live, s.staging); err != nil {
		return fmt.Errorf("commit replace: %w", err)
	}
	s.committed = true
	log.Info("replace committed",
		"category", "write",
		"target", fmt.Sprintf("%s.%s", s.live.Schema, s.live.Name),
	)
	return nil
}

func (s *replaceSession) abort(ctx context.Context) {
	if s == nil || s.legacy || !s.active || s.committed || s.atomic == nil {
		return
	}
	_ = s.atomic.AbortReplace(ctx, s.staging)
}
