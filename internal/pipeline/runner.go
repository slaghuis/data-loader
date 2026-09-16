package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/slaghuis/data-loader/internal/config"
	"github.com/slaghuis/data-loader/internal/logging"
	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/sources"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

type Runner struct {
	repo   *metadata.Repository
	sink   contracts.Sink
	logger *slog.Logger
}

func NewRunner(repo *metadata.Repository, sink contracts.Sink, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{repo: repo, sink: sink, logger: logger}
}

func (r *Runner) Execute(ctx context.Context, load metadata.Load) error {
	// A logger scoped to this load. run_uuid gets added once the run starts.
	log := r.logger.With(
		"load_id", load.ID,
		"source_id", load.SourceID,
		"load_name", load.Name,
		"source_name", load.Source.Name,
	)
	ctx = logging.WithContext(ctx, log)

	// 1. Build source config.
	params, err := config.ResolveParams(load.Source.ParamsJSON)
	if err != nil {
		log.Error("resolve params", "category", "connect", "err", err)
		return err
	}
	src, err := sources.Build(contracts.SourceConfig{
		SourceID:   load.SourceID,
		SourceType: load.Source.Kind,
		Name:       load.Source.Name,
		Params:     params,
	})
	if err != nil {
		log.Error("build source", "category", "connect", "err", err)
		return err
	}
	defer src.Close()

	if err := src.Open(ctx); err != nil {
		log.Error("open source", "category", "connect", "err", err)
		return err
	}
	log.Debug("source opened", "category", "connect")

	// 2. Current watermark.
	wm, err := r.repo.GetWatermark(ctx, load.ID)
	if err != nil {
		log.Error("read watermark", "category", "watermark", "err", err)
		return err
	}
	current := contracts.Watermark{Type: contracts.WatermarkType(load.WatermarkType)}
	if wm != nil {
		current.Value = wm.Value
	}

	// 3. Start a run.
	run, err := r.repo.StartRun(ctx, load.ID, current.Value)
	if err != nil {
		log.Error("start run", "category", "read", "err", err)
		return err
	}
	log = log.With("run_uuid", run.RunUUID)
	ctx = logging.WithContext(ctx, log)
	log.Info("load started",
		"category", "read",
		"mode", load.Mode,
		"watermark_start", current.Value,
	)

	// 4. Prepare handler.
	target := contracts.TargetTable{Schema: load.TargetSchema, Name: load.TargetTable}
	mode := contracts.LoadMode(load.Mode)
	loadTS := time.Now().UTC()

	var rowsWritten int64
	firstBatch := true

	handler := func(ctx context.Context, batch contracts.Batch) error {
		if firstBatch {
			if load.AutoCreateTable {
				if err := r.sink.EnsureTable(ctx, target, batch.Columns); err != nil {
					return fmt.Errorf("ensure target: %w", err)
				}
				log.Debug("target ensured",
					"category", "write",
					"target", fmt.Sprintf("%s.%s", target.Schema, target.Name),
				)
			}
			if mode == contracts.LoadModeReplace {
				if err := r.sink.Truncate(ctx, target); err != nil {
					return fmt.Errorf("truncate target: %w", err)
				}
				log.Info("target truncated", "category", "write")
			}
			firstBatch = false
		}
		n, err := r.sink.WriteBatch(ctx, target, batch, loadTS)
		if err != nil {
			return err
		}
		rowsWritten += n
		log.Debug("batch written",
			"category", "write",
			"batch_rows", len(batch.Rows),
			"rows_written_total", rowsWritten,
		)
		return nil
	}

	// 5. Read.
	loadCfg := contracts.LoadConfig{
		LoadID:          load.ID,
		Name:            load.Name,
		ObjectName:      load.ObjectName,
		Mode:            mode,
		WatermarkColumn: load.WatermarkColumn,
		WatermarkType:   contracts.WatermarkType(load.WatermarkType),
		LastWatermark:   current,
		BatchSize:       load.BatchSize,
	}
	result, err := src.Read(ctx, loadCfg, handler)
	if err != nil {
		_ = r.repo.FinishRun(ctx, run, "failed", result.RowsRead, rowsWritten, current.Value, err.Error())
		log.Error("load failed",
			"category", "read",
			"err", err,
			"rows_read", result.RowsRead,
			"rows_written", rowsWritten,
		)
		return err
	}

	// 6. Watermark.
	newWMValue := result.NewWatermark.Value
	if mode == contracts.LoadModeDelta && newWMValue != "" && newWMValue != current.Value {
		if err := r.repo.UpsertWatermark(ctx, load.ID, newWMValue); err != nil {
			log.Warn("update watermark", "category", "watermark", "err", err)
		} else {
			log.Info("watermark advanced",
				"category", "watermark",
				"watermark_from", current.Value,
				"watermark_to", newWMValue,
			)
		}
	}

	// 7. Finish.
	if err := r.repo.FinishRun(ctx, run, "success", result.RowsRead, rowsWritten, newWMValue, ""); err != nil {
		log.Error("finish run", "category", "read", "err", err)
		return err
	}
	log.Info("load complete",
		"category", "read",
		"rows_read", result.RowsRead,
		"rows_written", rowsWritten,
		"duration_ms", time.Since(run.StartedAt).Milliseconds(),
	)
	return nil
}
