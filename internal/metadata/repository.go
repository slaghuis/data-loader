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