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

	r := pipeline.NewRunnerWithBuilder(store, nil, sink, runstate.New(), testLogger(),
		func(cfg contracts.SourceConfig) (contracts.Source, error) { return src, nil }, nil)

	err := r.Execute(context.Background(), load)
	if err == nil {
		t.Fatal("expected upsert failure to fail the run")
	}
	if len(store.FinishedStatus) == 0 || store.FinishedStatus[len(store.FinishedStatus)-1] != "failed" {
		t.Fatalf("finished status = %v, want failed", store.FinishedStatus)
	}
}

func TestRunner_Replace_BeginsAndCommits_ZeroRows_ADR020(t *testing.T) {
	store := &testutil.FakeRunStore{}
	sink := &testutil.FakeSink{Exists: true}
	src := &testutil.FakeSource{
		KindName: "fake",
		ReadFn: func(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
			return contracts.ReadResult{}, nil
		},
	}
	r := pipeline.NewRunnerWithBuilder(store, nil, sink, runstate.New(), testLogger(),
		func(cfg contracts.SourceConfig) (contracts.Source, error) { return src, nil }, nil)

	if err := r.Execute(context.Background(), sampleLoad(string(contracts.LoadModeReplace))); err != nil {
		t.Fatal(err)
	}
	if sink.TruncateCalls != 0 {
		t.Fatalf("TruncateCalls = %d, want 0 (atomic path)", sink.TruncateCalls)
	}
	if sink.BeginCalls != 1 || sink.CommitCalls != 1 {
		t.Fatalf("Begin=%d Commit=%d, want 1/1", sink.BeginCalls, sink.CommitCalls)
	}
	if sink.AbortCalls != 0 {
		t.Fatalf("AbortCalls = %d, want 0", sink.AbortCalls)
	}
	if len(store.FinishedStatus) != 1 || store.FinishedStatus[0] != "success" {
		t.Fatalf("status = %v", store.FinishedStatus)
	}
}

func TestRunner_Replace_NoTable_ZeroRows_Skips_ADR020(t *testing.T) {
	store := &testutil.FakeRunStore{}
	sink := &testutil.FakeSink{Exists: false}
	src := &testutil.FakeSource{KindName: "fake"}
	r := pipeline.NewRunnerWithBuilder(store, nil, sink, runstate.New(), testLogger(),
		func(cfg contracts.SourceConfig) (contracts.Source, error) { return src, nil }, nil)

	if err := r.Execute(context.Background(), sampleLoad(string(contracts.LoadModeReplace))); err != nil {
		t.Fatal(err)
	}
	if sink.BeginCalls != 0 || sink.CommitCalls != 0 {
		t.Fatalf("Begin=%d Commit=%d, want 0/0 when no table and no rows", sink.BeginCalls, sink.CommitCalls)
	}
}

func TestRunner_Replace_WritesToStaging_ADR020(t *testing.T) {
	store := &testutil.FakeRunStore{}
	sink := &testutil.FakeSink{Exists: true}
	src := &testutil.FakeSource{
		KindName: "fake",
		ReadFn: func(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
			batch := contracts.Batch{
				Columns: []contracts.Column{{Name: "id", DataType: "int64"}},
				Rows:    []contracts.Row{{"id": int64(1)}},
			}
			if err := handler(ctx, batch); err != nil {
				return contracts.ReadResult{}, err
			}
			return contracts.ReadResult{RowsRead: 1}, nil
		},
	}
	r := pipeline.NewRunnerWithBuilder(store, nil, sink, runstate.New(), testLogger(),
		func(cfg contracts.SourceConfig) (contracts.Source, error) { return src, nil }, nil)

	if err := r.Execute(context.Background(), sampleLoad(string(contracts.LoadModeReplace))); err != nil {
		t.Fatal(err)
	}
	if sink.LastWriteTo.Name != "stg_orders_stg" {
		t.Fatalf("LastWriteTo = %+v, want staging suffix", sink.LastWriteTo)
	}
	if sink.CommitCalls != 1 {
		t.Fatalf("CommitCalls = %d", sink.CommitCalls)
	}
}

func TestRunner_Replace_AbortOnReadFailure_ADR020(t *testing.T) {
	store := &testutil.FakeRunStore{}
	sink := &testutil.FakeSink{Exists: true}
	src := &testutil.FakeSource{
		KindName: "fake",
		ReadFn: func(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
			return contracts.ReadResult{RowsRead: 0}, errors.New("source down")
		},
	}
	r := pipeline.NewRunnerWithBuilder(store, nil, sink, runstate.New(), testLogger(),
		func(cfg contracts.SourceConfig) (contracts.Source, error) { return src, nil }, nil)

	if err := r.Execute(context.Background(), sampleLoad(string(contracts.LoadModeReplace))); err == nil {
		t.Fatal("expected error")
	}
	if sink.BeginCalls != 1 {
		t.Fatalf("BeginCalls = %d", sink.BeginCalls)
	}
	if sink.CommitCalls != 0 {
		t.Fatalf("CommitCalls = %d, want 0", sink.CommitCalls)
	}
	if sink.AbortCalls != 1 {
		t.Fatalf("AbortCalls = %d, want 1", sink.AbortCalls)
	}
}

func TestRunner_Replace_LegacyTruncate_WhenNoAtomicReplacer_ADR020(t *testing.T) {
	store := &testutil.FakeRunStore{}
	sink := &testutil.TruncateOnlySink{Exists: true}
	src := &testutil.FakeSource{KindName: "fake"}
	r := pipeline.NewRunnerWithBuilder(store, nil, sink, runstate.New(), testLogger(),
		func(cfg contracts.SourceConfig) (contracts.Source, error) { return src, nil }, nil)

	if err := r.Execute(context.Background(), sampleLoad(string(contracts.LoadModeReplace))); err != nil {
		t.Fatal(err)
	}
	if sink.TruncateCalls != 1 {
		t.Fatalf("TruncateCalls = %d, want 1 on legacy path", sink.TruncateCalls)
	}
}
