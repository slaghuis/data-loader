package metadata

import (
	"context"
	"crypto/sha256"
    "encoding/hex"
	"errors"
	"time"
    "fmt"
    "sort"

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

// MetadataFingerprint returns a stable hash over all metadata that affects
// scheduling and execution. Cheap enough to call every minute.
func (r *Repository) MetadataFingerprint(ctx context.Context) (string, error) {
	type sourceFP struct {
		ID         int64
		Kind       string
		ParamsJSON string
		IsEnabled  bool
	}
	type loadFP struct {
		ID              int64
		SourceID        int64
		CronExpression  string
		Mode            string
		WatermarkColumn string
		WatermarkType   string
		ObjectName      string
		TargetSchema    string
		TargetTable     string
		BatchSize       int
		AutoCreateTable bool
		IsEnabled       bool
	}

	var sources []sourceFP
	if err := r.db.WithContext(ctx).
		Model(&Source{}).
		Select("id, kind, params_json, is_enabled").
		Order("id").
		Scan(&sources).Error; err != nil {
		return "", fmt.Errorf("fingerprint sources: %w", err)
	}

	var loads []loadFP
	if err := r.db.WithContext(ctx).
		Model(&Load{}).
		Select("id, source_id, cron_expression, mode, watermark_column, watermark_type, object_name, target_schema, target_table, batch_size, auto_create_table, is_enabled").
		Order("id").
		Scan(&loads).Error; err != nil {
		return "", fmt.Errorf("fingerprint loads: %w", err)
	}

	// Tag/node lists change often but only affect polling behaviour, not scheduling.
	// We still include their row counts and per-load enabled counts so the fingerprint
	// covers "tag added" and "tag disabled" events.
	type tagFP struct {
		LoadID   int64
		Enabled  int
		Total    int
	}
	var modbusRows []tagFP
	if err := r.db.WithContext(ctx).
		Raw(`SELECT load_id AS load_id,
		            SUM(CASE WHEN is_enabled = 1 THEN 1 ELSE 0 END) AS enabled,
		            COUNT(*) AS total
		     FROM meta_modbus_tags
		     GROUP BY load_id
		     ORDER BY load_id`).
		Scan(&modbusRows).Error; err != nil {
		return "", fmt.Errorf("fingerprint modbus tags: %w", err)
	}
	var opcuaRows []tagFP
	if err := r.db.WithContext(ctx).
		Raw(`SELECT load_id AS load_id,
		            SUM(CASE WHEN is_enabled = 1 THEN 1 ELSE 0 END) AS enabled,
		            COUNT(*) AS total
		     FROM meta_opcua_nodes
		     GROUP BY load_id
		     ORDER BY load_id`).
		Scan(&opcuaRows).Error; err != nil {
		return "", fmt.Errorf("fingerprint opcua nodes: %w", err)
	}

	// Sort defensively (SQL ORDER BY is authoritative but doesn't hurt).
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	sort.Slice(loads,   func(i, j int) bool { return loads[i].ID < loads[j].ID })
	sort.Slice(modbusRows, func(i, j int) bool { return modbusRows[i].LoadID < modbusRows[j].LoadID })
	sort.Slice(opcuaRows,  func(i, j int) bool { return opcuaRows[i].LoadID < opcuaRows[j].LoadID })

	h := sha256.New()
	fmt.Fprintf(h, "SOURCES:\n")
	for _, s := range sources {
		fmt.Fprintf(h, "%d|%s|%s|%v\n", s.ID, s.Kind, s.ParamsJSON, s.IsEnabled)
	}
	fmt.Fprintf(h, "LOADS:\n")
	for _, l := range loads {
		fmt.Fprintf(h, "%d|%d|%s|%s|%s|%s|%s|%s|%s|%d|%v|%v\n",
			l.ID, l.SourceID, l.CronExpression, l.Mode,
			l.WatermarkColumn, l.WatermarkType,
			l.ObjectName, l.TargetSchema, l.TargetTable,
			l.BatchSize, l.AutoCreateTable, l.IsEnabled)
	}
	fmt.Fprintf(h, "MODBUS:\n")
	for _, t := range modbusRows {
		fmt.Fprintf(h, "%d|%d|%d\n", t.LoadID, t.Enabled, t.Total)
	}
	fmt.Fprintf(h, "OPCUA:\n")
	for _, t := range opcuaRows {
		fmt.Fprintf(h, "%d|%d|%d\n", t.LoadID, t.Enabled, t.Total)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
