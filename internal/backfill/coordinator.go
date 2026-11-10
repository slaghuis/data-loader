package backfill

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

// Coordinator manages sibling lifecycle for opt-in historic backfill (ADR-022).
type Coordinator struct {
	store  Store
	min    MinResolver // optional; required when backfill_stop is empty
	logger *slog.Logger
}

// NewCoordinator builds a Coordinator. min may be nil when every parent supplies backfill_stop.
func NewCoordinator(store Store, min MinResolver, logger *slog.Logger) *Coordinator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Coordinator{store: store, min: min, logger: logger}
}

// Reload implements reload.Reloader — reconcile siblings with parent flags.
func (c *Coordinator) Reload(ctx context.Context) error {
	return c.Sync(ctx)
}

// Sync creates, activates, or leaves siblings according to parent state.
func (c *Coordinator) Sync(ctx context.Context) error {
	parents, err := c.store.ListBackfillParents(ctx)
	if err != nil {
		return fmt.Errorf("list backfill parents: %w", err)
	}
	for i := range parents {
		if err := c.syncParent(ctx, &parents[i]); err != nil {
			c.logger.Error("backfill sync parent",
				"category", "watermark",
				"load_id", parents[i].ID,
				"load_name", parents[i].Name,
				"err", err,
			)
			// Continue other parents; one bad config must not block reload.
		}
	}
	return nil
}

func (c *Coordinator) syncParent(ctx context.Context, parent *metadata.Load) error {
	if err := ValidateParent(*parent, parent.Source.Kind); err != nil {
		return err
	}
	sibling, err := c.store.GetSiblingByParent(ctx, parent.ID)
	if err != nil {
		return err
	}
	if sibling == nil {
		return c.createSibling(ctx, parent)
	}
	return c.activateIfReady(ctx, parent, sibling)
}

func (c *Coordinator) createSibling(ctx context.Context, parent *metadata.Load) error {
	cronExpr, err := OffsetCron(parent.CronExpression)
	if err != nil {
		return fmt.Errorf("offset cron: %w", err)
	}
	parentID := parent.ID
	sib := &metadata.Load{
		SourceID:         parent.SourceID,
		Name:             SiblingName(parent.Name),
		ObjectName:       parent.ObjectName,
		TargetSchema:     parent.TargetSchema,
		TargetTable:      parent.TargetTable,
		Mode:             parent.Mode,
		WatermarkColumn:  parent.WatermarkColumn,
		WatermarkType:    parent.WatermarkType,
		BatchSize:        parent.BatchSize,
		AutoCreateTable:  parent.AutoCreateTable,
		CronExpression:   cronExpr,
		IsEnabled:        false,
		BackfillOfLoadID: &parentID,
	}
	if err := c.store.CreateLoad(ctx, sib); err != nil {
		return fmt.Errorf("create sibling: %w", err)
	}
	c.logger.Info("backfill sibling created",
		"category", "watermark",
		"parent_load_id", parent.ID,
		"sibling_load_id", sib.ID,
		"sibling_name", sib.Name,
	)
	return c.activateIfReady(ctx, parent, sib)
}

func (c *Coordinator) activateIfReady(ctx context.Context, parent *metadata.Load, sibling *metadata.Load) error {
	if sibling.BackfillCeiling != "" && sibling.IsEnabled {
		return nil
	}
	wm, err := c.store.GetWatermark(ctx, parent.ID)
	if err != nil {
		return err
	}
	if wm == nil || strings.TrimSpace(wm.Value) == "" {
		c.logger.Debug("backfill waiting for parent watermark",
			"category", "watermark",
			"parent_load_id", parent.ID,
		)
		return nil
	}
	stop := strings.TrimSpace(parent.BackfillStop)
	if stop == "" {
		if c.min == nil {
			return fmt.Errorf("backfill_stop empty and no MinResolver configured")
		}
		stop, err = c.min.ResolveMin(ctx, *parent)
		if err != nil {
			return fmt.Errorf("resolve backfill_stop MIN: %w", err)
		}
		parent.BackfillStop = stop
		if err := c.store.SaveLoad(ctx, parent); err != nil {
			return fmt.Errorf("persist resolved backfill_stop: %w", err)
		}
	}
	typ := contracts.WatermarkType(parent.WatermarkType)
	if cmp, err := contracts.CompareWatermark(typ, stop, wm.Value); err != nil {
		return err
	} else if cmp >= 0 {
		return fmt.Errorf("backfill_stop %q is not below parent watermark %q", stop, wm.Value)
	}

	sibling.BackfillCeiling = wm.Value
	sibling.IsEnabled = true
	if err := c.store.SaveLoad(ctx, sibling); err != nil {
		return fmt.Errorf("activate sibling: %w", err)
	}
	if err := c.store.UpsertWatermark(ctx, sibling.ID, stop); err != nil {
		return fmt.Errorf("seed sibling watermark: %w", err)
	}
	c.logger.Info("backfill sibling activated",
		"category", "watermark",
		"parent_load_id", parent.ID,
		"sibling_load_id", sibling.ID,
		"backfill_stop", stop,
		"backfill_ceiling", sibling.BackfillCeiling,
	)
	return nil
}

// AfterSuccessfulRun completes a sibling when its cursor reaches the ceiling,
// and retries Sync for parents that just finished first-run init.
func (c *Coordinator) AfterSuccessfulRun(ctx context.Context, load metadata.Load) error {
	if load.BackfillOfLoadID != nil {
		return c.maybeComplete(ctx, load)
	}
	if load.BackfillRequired {
		return c.Sync(ctx)
	}
	return nil
}

func (c *Coordinator) maybeComplete(ctx context.Context, sibling metadata.Load) error {
	ceiling := strings.TrimSpace(sibling.BackfillCeiling)
	if ceiling == "" {
		return nil
	}
	wm, err := c.store.GetWatermark(ctx, sibling.ID)
	if err != nil {
		return err
	}
	if wm == nil {
		return nil
	}
	typ := contracts.WatermarkType(sibling.WatermarkType)
	cmp, err := contracts.CompareWatermark(typ, wm.Value, ceiling)
	if err != nil {
		return err
	}
	if cmp < 0 {
		return nil
	}

	sibling.IsEnabled = false
	if err := c.store.SaveLoad(ctx, &sibling); err != nil {
		return fmt.Errorf("disable completed sibling: %w", err)
	}

	parent, err := c.store.GetLoad(ctx, *sibling.BackfillOfLoadID)
	if err != nil {
		return fmt.Errorf("load parent for completion: %w", err)
	}
	now := time.Now().UTC()
	parent.BackfillRequired = false
	parent.BackfillCompletedAt = &now
	if err := c.store.SaveLoad(ctx, parent); err != nil {
		return fmt.Errorf("mark parent backfill complete: %w", err)
	}
	c.logger.Info("backfill complete",
		"category", "watermark",
		"parent_load_id", parent.ID,
		"sibling_load_id", sibling.ID,
		"backfill_ceiling", ceiling,
	)
	return nil
}
