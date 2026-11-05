package pipeline_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/pipeline"
	"github.com/slaghuis/data-loader/internal/runstate"
	"github.com/slaghuis/data-loader/internal/testutil"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func sampleLoad(mode string) metadata.Load {
	return metadata.Load{
		ID:              1,
		SourceID:        2,
		Name:            "orders",
		ObjectName:      "dbo.Orders",
		TargetSchema:    "dbo",
		TargetTable:     "stg_orders",
		Mode:            mode,
		WatermarkType:   string(contracts.WatermarkNone),
		BatchSize:       100,
		AutoCreateTable: true,
		Source: metadata.Source{
			ID:         2,
			Name:       "erp",
			Kind:       "fake",
			ParamsJSON: `{}`,
			IsEnabled:  true,
		},
	}
}

func TestRunner_UpsertWatermarkFailure_FailsRun_D04(t *testing.T) {
	store := &testutil.FakeRunStore{UpsertErr: errors.New("db down")}
	sink := &testutil.FakeSink{Exists: true}
	src := &testutil.FakeSource{
		KindName: "fake",
		ReadFn: func(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
			return contracts.ReadResult{
				RowsRead:     1,
				NewWatermark: contracts.Watermark{Type: contracts.WatermarkNumeric, Value: "100"},
			}, nil
		},
	}
	load := sampleLoad(string(contracts.LoadModeDelta))
	load.WatermarkColumn = "id"
	load.WatermarkType = string(contracts.WatermarkNumeric)
	store.Watermark = &metadata.Watermark{Value: "50"}

	r := pipeline.NewRunnerWithBuilder(store, sink, runstate.New(), testLogger(),
		func(cfg contracts.SourceConfig) (contracts.Source, error) { return src, nil })

	err := r.Execute(context.Background(), load)
	if err == nil {
		t.Fatal("expected upsert failure to fail the run")
	}
	if len(store.FinishedStatus) == 0 || store.FinishedStatus[len(store.FinishedStatus)-1] != "failed" {
		t.Fatalf("finished status = %v, want failed", store.FinishedStatus)
	}
}

func TestRunner_Replace_TruncatesBeforeRead_ZeroRows_D05(t *testing.T) {
	store := &testutil.FakeRunStore{}
	sink := &testutil.FakeSink{Exists: true}
	src := &testutil.FakeSource{
		KindName: "fake",
		ReadFn: func(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
			// Zero rows: handler never called.
			return contracts.ReadResult{}, nil
		},
	}
	r := pipeline.NewRunnerWithBuilder(store, sink, runstate.New(), testLogger(),
		func(cfg contracts.SourceConfig) (contracts.Source, error) { return src, nil })

	if err := r.Execute(context.Background(), sampleLoad(string(contracts.LoadModeReplace))); err != nil {
		t.Fatal(err)
	}
	if sink.TruncateCalls != 1 {
		t.Fatalf("TruncateCalls = %d, want 1 (before read, even with zero rows)", sink.TruncateCalls)
	}
	if len(store.FinishedStatus) != 1 || store.FinishedStatus[0] != "success" {
		t.Fatalf("status = %v", store.FinishedStatus)
	}
}

func TestRunner_Replace_NoTable_SkipsTruncate_D05(t *testing.T) {
	store := &testutil.FakeRunStore{}
	sink := &testutil.FakeSink{Exists: false}
	src := &testutil.FakeSource{KindName: "fake"}
	r := pipeline.NewRunnerWithBuilder(store, sink, runstate.New(), testLogger(),
		func(cfg contracts.SourceConfig) (contracts.Source, error) { return src, nil })

	if err := r.Execute(context.Background(), sampleLoad(string(contracts.LoadModeReplace))); err != nil {
		t.Fatal(err)
	}
	if sink.TruncateCalls != 0 {
		t.Fatalf("TruncateCalls = %d, want 0 when table does not exist", sink.TruncateCalls)
	}
}
