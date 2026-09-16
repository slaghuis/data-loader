package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/slaghuis/data-loader/internal/config"
	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/sources"
	"github.com/slaghuis/data-loader/internal/sources/sqlserver"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

type Runner struct {
	repo *metadata.Repository
	sink contracts.Sink
}

func NewRunner(repo *metadata.Repository, sink contracts.Sink) *Runner {
	return &Runner{repo: repo, sink: sink}
}

// Execute runs a single load end-to-end.
func (r *Runner) Execute(ctx context.Context, load metadata.Load) error {
	// 1. Build source config from metadata.
	params := map[string]string{}
	if err := json.Unmarshal([]byte(load.Source.ParamsJSON), &params); err != nil {
		return fmt.Errorf("parse source params: %w", err)
	}
	params = config.ResolveSecrets(params)

	srcCfg := contracts.SourceConfig{
		SourceID:   load.Source.ID,
		SourceType: load.Source.Kind,
		Name:       load.Source.Name,
		Params:     params,
	}

	src, err := sources.Build(srcCfg)
	if err != nil {
		return err
	}

	// 2. Load current watermark.
	wm, err := r.repo.GetWatermark(ctx, load.ID)
	if err != nil {
		return err
	}
	var lastWM contracts.Watermark
	lastWM.Type = contracts.WatermarkType(load.WatermarkType)
	if wm != nil {
		lastWM.Value = wm.Value
	}

	// 3. Start run record.
	run, err := r.repo.StartRun(ctx, load.ID, lastWM.Value)
	if err != nil {
		return err
	}

	r.logInfo(ctx, run.RunUUID, load.ID, load.Source.ID, "connect",
		fmt.Sprintf("Opening source %q", load.Source.Name))

	// 4. Connect to source.
	if err := src.Open(ctx); err != nil {
		return r.fail(ctx, run, load, "connect", err)
	}
	defer src.Close()

	// 5. Build the source load config.
	loadCfg := contracts.LoadConfig{
		LoadID:          load.ID,
		Name:            load.Name,
		ObjectName:      load.ObjectName,
		Mode:            contracts.LoadMode(load.Mode),
		WatermarkColumn: load.WatermarkColumn,
		WatermarkType:   contracts.WatermarkType(load.WatermarkType),
		LastWatermark:   lastWM,
		BatchSize:       load.BatchSize,
	}

	// 6. Handle replace mode: truncate before load.
	target := contracts.TargetTable{Schema: load.TargetSchema, Name: load.TargetTable}

	// 7. Stream rows.
	var rowsWritten int64
	var firstBatch = true
	loadTS := time.Now().UTC()

	handler := func(ctx context.Context, batch contracts.Batch) error {
		if firstBatch {
			if load.AutoCreateTable {
				if err := r.sink.EnsureTable(ctx, target, batch.Columns); err != nil {
					return fmt.Errorf("ensure target table: %w", err)
				}
			}
			if loadCfg.Mode == contracts.LoadModeReplace {
				if err := r.sink.Truncate(ctx, target); err != nil {
					return fmt.Errorf("truncate target: %w", err)
				}
			}
			firstBatch = false
		}
		n, err := r.sink.WriteBatch(ctx, target, batch, loadTS)
		if err != nil {
			return err
		}
		rowsWritten += n
		return nil
	}

	r.logInfo(ctx, run.RunUUID, load.ID, load.Source.ID, "read",
		fmt.Sprintf("Reading %s (mode=%s)", load.ObjectName, load.Mode))

	readResult, err := src.Read(ctx, loadCfg, handler)
	if err != nil {
		// Handle first-run watermark initialization specially.
		if wm, ok := sqlserver.IsFirstRunInit(err); ok {
			if err := r.repo.UpsertWatermark(ctx, load.ID, wm.Value); err != nil {
				return r.fail(ctx, run, load, "watermark", err)
			}
			r.logInfo(ctx, run.RunUUID, load.ID, load.Source.ID, "watermark",
				fmt.Sprintf("Initialized watermark to %q (no rows loaded on first run)", wm.Value))
			return r.repo.FinishRun(ctx, run, "success", 0, 0, wm.Value, "")
		}
		return r.fail(ctx, run, load, "read", err)
	}

	// 8. Persist new watermark (delta only).
	newWMValue := ""
	if loadCfg.Mode == contracts.LoadModeDelta && readResult.NewWatermark.Value != "" {
		if err := r.repo.UpsertWatermark(ctx, load.ID, readResult.NewWatermark.Value); err != nil {
			return r.fail(ctx, run, load, "watermark", err)
		}
		newWMValue = readResult.NewWatermark.Value
	}

	r.logInfo(ctx, run.RunUUID, load.ID, load.Source.ID, "write",
		fmt.Sprintf("Wrote %d rows to %s.%s", rowsWritten, target.Schema, target.Name))

	return r.repo.FinishRun(ctx, run, "success", readResult.RowsRead, rowsWritten, newWMValue, "")
}

func (r *Runner) fail(ctx context.Context, run *metadata.LoadRun, load metadata.Load, category string, cause error) error {
	msg := cause.Error()
	r.logError(ctx, run.RunUUID, load.ID, load.Source.ID, category, msg)
	_ = r.repo.FinishRun(ctx, run, "failed", 0, 0, "", msg)
	return cause
}

func (r *Runner) logInfo(ctx context.Context, runUUID string, loadID, sourceID int64, category, msg string) {
	_ = r.repo.Log(ctx, metadata.LogEntry{
		Level:    "info",
		RunUUID:  runUUID,
		LoadID:   &loadID,
		SourceID: &sourceID,
		Category: category,
		Message:  msg,
	})
}

func (r *Runner) logError(ctx context.Context, runUUID string, loadID, sourceID int64, category, msg string) {
	_ = r.repo.Log(ctx, metadata.LogEntry{
		Level:    "error",
		RunUUID:  runUUID,
		LoadID:   &loadID,
		SourceID: &sourceID,
		Category: category,
		Message:  msg,
	})
}
