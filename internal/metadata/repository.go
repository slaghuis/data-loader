package metadata

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Repository provides typed access to the metadata store.
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// ---------- Sources & Loads ----------

func (r *Repository) ListEnabledLoads(ctx context.Context) ([]Load, error) {
	var loads []Load
	err := r.db.WithContext(ctx).
		Preload("Source").
		Where("is_enabled = ?", true).
		Find(&loads).Error
	return loads, err
}

func (r *Repository) GetLoad(ctx context.Context, id int64) (*Load, error) {
	var l Load
	if err := r.db.WithContext(ctx).Preload("Source").First(&l, id).Error; err != nil {
		return nil, err
	}
	return &l, nil
}

// ---------- Watermarks ----------

func (r *Repository) GetWatermark(ctx context.Context, loadID int64) (*Watermark, error) {
	var w Watermark
	err := r.db.WithContext(ctx).First(&w, "load_id = ?", loadID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// UpsertWatermark writes a new watermark value, preserving the previous value.
func (r *Repository) UpsertWatermark(ctx context.Context, loadID int64, newValue string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing Watermark
		err := tx.First(&existing, "load_id = ?", loadID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&Watermark{
				LoadID:    loadID,
				Value:     newValue,
				UpdatedAt: time.Now().UTC(),
			}).Error
		}
		if err != nil {
			return err
		}
		existing.PreviousValue = existing.Value
		existing.Value = newValue
		existing.UpdatedAt = time.Now().UTC()
		return tx.Save(&existing).Error
	})
}

// ResetWatermark clears a watermark and records the reason.
func (r *Repository) ResetWatermark(ctx context.Context, loadID int64, reason string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&Watermark{}).
		Where("load_id = ?", loadID).
		Updates(map[string]any{
			"previous_value": gorm.Expr("value"),
			"value":          "",
			"reset_at":       &now,
			"reset_reason":   reason,
			"updated_at":     now,
		}).Error
}

// ---------- LoadRuns ----------

func (r *Repository) StartRun(ctx context.Context, loadID int64, watermarkStart string) (*LoadRun, error) {
	run := &LoadRun{
		RunUUID:        uuid.NewString(),
		LoadID:         loadID,
		Status:         "running",
		StartedAt:      time.Now().UTC(),
		WatermarkStart: watermarkStart,
	}
	if err := r.db.WithContext(ctx).Create(run).Error; err != nil {
		return nil, err
	}
	return run, nil
}

func (r *Repository) FinishRun(ctx context.Context, run *LoadRun, status string, rowsRead, rowsWritten int64, watermarkEnd, errMsg string) error {
	now := time.Now().UTC()
	run.FinishedAt = &now
	run.Status = status
	run.RowsRead = rowsRead
	run.RowsWritten = rowsWritten
	run.WatermarkEnd = watermarkEnd
	run.ErrorMessage = errMsg
	run.DurationMs = now.Sub(run.StartedAt).Milliseconds()
	return r.db.WithContext(ctx).Save(run).Error
}

// ---------- Logs ----------

func (r *Repository) Log(ctx context.Context, e LogEntry) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(&e).Error
}

// ------------ Modbus TCP Tags ----------------
func (r *Repository) ListModbusTags(ctx context.Context, loadID int64) ([]ModbusTag, error) {
	var tags []ModbusTag
	err := r.db.WithContext(ctx).
		Where("load_id = ? AND is_enabled = ?", loadID, true).
		Order("id ASC").
		Find(&tags).Error
	return tags, err
}

// ----------- OPC-UA -------------------
func (r *Repository) ListOPCUANodes(ctx context.Context, loadID int64) ([]OPCUANode, error) {
	var nodes []OPCUANode
	err := r.db.WithContext(ctx).
		Where("load_id = ? AND is_enabled = ?", loadID, true).
		Order("id ASC").
		Find(&nodes).Error
	return nodes, err
}

// -------------- Housekeeping ----------------
// PruneLogEntries deletes log entries older than cutoff in batches.
// Returns the total number of rows deleted.
func (r *Repository) PruneLogEntries(ctx context.Context, cutoff time.Time, batchSize int, pauseBetweenBatches time.Duration) (int64, error) {
	return r.pruneInBatches(ctx, `
		DELETE TOP (@p1) FROM meta_log_entries
		WHERE created_at < @p2
	`, batchSize, cutoff, pauseBetweenBatches)
}

// PruneLoadRuns deletes terminal (success/failed/aborted) load runs older than cutoff.
// Running runs are never pruned — you want to see stuck jobs.
func (r *Repository) PruneLoadRuns(ctx context.Context, cutoff time.Time, batchSize int, pauseBetweenBatches time.Duration) (int64, error) {
	return r.pruneInBatches(ctx, `
		DELETE TOP (@p1) FROM meta_load_runs
		WHERE started_at < @p2
		  AND status IN ('success', 'failed', 'aborted')
	`, batchSize, cutoff, pauseBetweenBatches)
}

// pruneInBatches runs a batched DELETE until zero rows are affected, honouring
// the passed context so shutdown is prompt.
func (r *Repository) pruneInBatches(ctx context.Context, sqlStmt string, batchSize int, cutoff time.Time, pause time.Duration) (int64, error) {
	if batchSize <= 0 {
		batchSize = 5000
	}
	sqlDB, err := r.db.DB()
	if err != nil {
		return 0, err
	}

	var totalDeleted int64
	for {
		if err := ctx.Err(); err != nil {
			return totalDeleted, err
		}

		res, err := sqlDB.ExecContext(ctx, sqlStmt, batchSize, cutoff)
		if err != nil {
			return totalDeleted, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return totalDeleted, err
		}
		totalDeleted += n

		if n < int64(batchSize) {
			// Last batch — nothing more to prune.
			return totalDeleted, nil
		}

		if pause > 0 {
			select {
			case <-ctx.Done():
				return totalDeleted, ctx.Err()
			case <-time.After(pause):
			}
		}
	}
}
