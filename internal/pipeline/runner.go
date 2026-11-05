package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/slaghuis/data-loader/internal/config"
	"github.com/slaghuis/data-loader/internal/logging"
	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/metrics"
	"github.com/slaghuis/data-loader/internal/runstate"
	"github.com/slaghuis/data-loader/internal/sources"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

// Runner executes a single load against a Source and Sink.
type Runner struct {
	repo      RunStore
	telemetry contracts.TelemetryCatalog
	sink      contracts.Sink
	state     *runstate.Registry
	logger    *slog.Logger
	sessions  SourceSession
}

// NewRunner wires the production source registry as the SourceBuilder.
// telemetry may be nil when no modbus/opcua loads are used.
// sessions may be nil (open-per-run) or a SourcePool when LOADER_SOURCE_POOL is on (ADR-021).
func NewRunner(repo RunStore, telemetry contracts.TelemetryCatalog, sink contracts.Sink, state *runstate.Registry, logger *slog.Logger, sessions SourceSession) *Runner {
	return NewRunnerWithBuilder(repo, telemetry, sink, state, logger, sources.Build, sessions)
}

// NewRunnerWithBuilder allows tests to inject a SourceBuilder and optional SourceSession.
func NewRunnerWithBuilder(repo RunStore, telemetry contracts.TelemetryCatalog, sink contracts.Sink, state *runstate.Registry, logger *slog.Logger, build SourceBuilder, sessions SourceSession) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	if build == nil {
		build = sources.Build
	}
	if sessions == nil {
		sessions = NewEphemeral(build)
	}
	return &Runner{repo: repo, telemetry: telemetry, sink: sink, state: state, logger: logger, sessions: sessions}
}

// Execute runs one load end-to-end.
func (r *Runner) Execute(ctx context.Context, load metadata.Load) error {
	log := r.logger.With(
		"load_id", load.ID,
		"source_id", load.SourceID,
		"load_name", load.Name,
		"source_name", load.Source.Name,
	)
	ctx = logging.WithContext(ctx, log)

	if load.Source.Kind == "modbus" || load.Source.Kind == "opcua" {
		if load.Mode != string(contracts.LoadModeAppend) {
			log.Warn("forcing append mode for point-based source",
				"category", "read", "configured_mode", load.Mode)
			load.Mode = string(contracts.LoadModeAppend)
		}
		load.WatermarkColumn = ""
		load.WatermarkType = string(contracts.WatermarkNone)
	}

	params, err := config.ResolveParams(load.Source.ParamsJSON)
	if err != nil {
		log.Error("resolve params", "category", "connect", "err", err)
		return err
	}
	srcCfg := contracts.SourceConfig{
		SourceID:   load.SourceID,
		SourceType: load.Source.Kind,
		Name:       load.Source.Name,
		Params:     params,
		Telemetry:  r.telemetry,
	}
	src, release, err := r.sessions.Obtain(ctx, srcCfg)
	if err != nil {
		log.Error("obtain source", "category", "connect", "err", err)
		return err
	}
	invalidate := false
	defer func() { release(invalidate) }()
	log.Debug("source ready", "category", "connect")

	wm, err := r.repo.GetWatermark(ctx, load.ID)
	if err != nil {
		log.Error("read watermark", "category", "watermark", "err", err)
		return err
	}
	current := contracts.Watermark{Type: contracts.WatermarkType(load.WatermarkType)}
	if wm != nil {
		current.Value = wm.Value
	}

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
	r.state.MarkRunning(load.ID, run.RunUUID, run.StartedAt)

	target := contracts.TargetTable{Schema: load.TargetSchema, Name: load.TargetTable}
	mode := contracts.LoadMode(load.Mode)
	loadTS := time.Now().UTC()
	targetLabel := fmt.Sprintf("%s.%s", target.Schema, target.Name)

	var repl *replaceSession
	if mode == contracts.LoadModeReplace {
		repl = newReplaceSession(r.sink, target)
		if err := repl.prepareExisting(ctx, r.sink, log); err != nil {
			_ = r.repo.FinishRun(ctx, run, "failed", 0, 0, current.Value, err.Error())
			r.state.MarkFinished(load.ID, "failed", time.Now().UTC(), 0, 0, err.Error())
			log.Error("prepare replace", "category", "write", "err", err)
			return err
		}
		defer repl.abort(ctx)
	}

	var rowsWritten int64
	firstBatch := true
	writeTarget := target
	if repl != nil {
		writeTarget = repl.writeTo
	}

	handler := func(ctx context.Context, batch contracts.Batch) error {
		if firstBatch {
			if mode == contracts.LoadModeReplace {
				if err := repl.ensureFirstBatch(ctx, r.sink, load.AutoCreateTable, batch.Columns, log); err != nil {
					return err
				}
				writeTarget = repl.writeTo
			} else if load.AutoCreateTable {
				if err := r.sink.EnsureTable(ctx, target, batch.Columns); err != nil {
					return fmt.Errorf("ensure target: %w", err)
				}
				log.Debug("target ensured",
					"category", "write",
					"target", targetLabel,
				)
			}
			firstBatch = false
		}
		n, err := r.sink.WriteBatch(ctx, writeTarget, batch, loadTS)
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
		invalidate = true // drop pooled connection after read failures (ADR-021)
		_ = r.repo.FinishRun(ctx, run, "failed", result.RowsRead, rowsWritten, current.Value, err.Error())
		r.state.MarkFinished(load.ID, "failed", time.Now().UTC(), result.RowsRead, rowsWritten, err.Error())
		metrics.ObserveRun(load.Name, "failed", targetLabel, result.RowsRead, rowsWritten, run.StartedAt)
		log.Error("load failed",
			"category", "read",
			"err", err,
			"rows_read", result.RowsRead,
			"rows_written", rowsWritten,
		)
		return err
	}

	if repl != nil {
		if err := repl.commit(ctx, log); err != nil {
			_ = r.repo.FinishRun(ctx, run, "failed", result.RowsRead, rowsWritten, current.Value, err.Error())
			r.state.MarkFinished(load.ID, "failed", time.Now().UTC(), result.RowsRead, rowsWritten, err.Error())
			metrics.ObserveRun(load.Name, "failed", targetLabel, result.RowsRead, rowsWritten, run.StartedAt)
			log.Error("commit replace", "category", "write", "err", err)
			return err
		}
	}

	newWMValue := result.NewWatermark.Value
	if mode == contracts.LoadModeDelta && newWMValue != "" &&
		(result.WatermarkInit || newWMValue != current.Value) {

		if err := r.repo.UpsertWatermark(ctx, load.ID, newWMValue); err != nil {
			// D-04: a failed watermark upsert must fail the run to avoid duplicates.
			msg := fmt.Sprintf("update watermark: %v", err)
			_ = r.repo.FinishRun(ctx, run, "failed", result.RowsRead, rowsWritten, current.Value, msg)
			r.state.MarkFinished(load.ID, "failed", time.Now().UTC(), result.RowsRead, rowsWritten, msg)
			metrics.ObserveRun(load.Name, "failed", targetLabel, result.RowsRead, rowsWritten, run.StartedAt)
			log.Error("update watermark", "category", "watermark", "err", err)
			return fmt.Errorf("update watermark: %w", err)
		}
		if result.WatermarkInit {
			log.Info("watermark initialized (first run, no rows loaded)",
				"category", "watermark",
				"watermark_to", newWMValue,
			)
		} else {
			log.Info("watermark advanced",
				"category", "watermark",
				"watermark_from", current.Value,
				"watermark_to", newWMValue,
			)
		}
	}

	if err := r.repo.FinishRun(ctx, run, "success", result.RowsRead, rowsWritten, newWMValue, ""); err != nil {
		r.state.MarkFinished(load.ID, "failed", time.Now().UTC(), result.RowsRead, rowsWritten, err.Error())
		metrics.ObserveRun(load.Name, "failed", targetLabel, result.RowsRead, rowsWritten, run.StartedAt)
		return err
	}
	r.state.MarkFinished(load.ID, "success", time.Now().UTC(), result.RowsRead, rowsWritten, "")
	metrics.ObserveRun(load.Name, "success", targetLabel, result.RowsRead, rowsWritten, run.StartedAt)
	if mode == contracts.LoadModeDelta && load.WatermarkType == string(contracts.WatermarkDatetime) {
		metrics.ObserveWatermarkLag(load.Name, newWMValue, time.Now().UTC())
	}
	log.Info("load complete",
		"category", "read",
		"rows_read", result.RowsRead,
		"rows_written", rowsWritten,
		"duration_ms", time.Since(run.StartedAt).Milliseconds(),
	)
	return nil
}
