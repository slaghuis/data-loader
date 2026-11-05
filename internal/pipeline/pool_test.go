package pipeline_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slaghuis/data-loader/internal/pipeline"
	"github.com/slaghuis/data-loader/internal/runstate"
	"github.com/slaghuis/data-loader/internal/testutil"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

func TestParamsFingerprint_Stable_ADR021(t *testing.T) {
	a := pipeline.ParamsFingerprint("sqlserver", map[string]string{"b": "2", "a": "1"})
	b := pipeline.ParamsFingerprint("sqlserver", map[string]string{"a": "1", "b": "2"})
	if a != b {
		t.Fatalf("fingerprints differ for key order: %s vs %s", a, b)
	}
	c := pipeline.ParamsFingerprint("sqlserver", map[string]string{"a": "1", "b": "3"})
	if a == c {
		t.Fatal("expected different fingerprint when param changes")
	}
}

func TestSourcePool_ReusesConnection_ADR021(t *testing.T) {
	var opens atomic.Int32
	build := func(cfg contracts.SourceConfig) (contracts.Source, error) {
		opens.Add(1)
		return &testutil.FakeSource{KindName: cfg.SourceType}, nil
	}
	pool := pipeline.NewSourcePool(build, pipeline.PoolConfig{IdleTimeout: time.Minute, MaxAge: time.Hour},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	cfg := contracts.SourceConfig{SourceID: 7, SourceType: "fake", Params: map[string]string{"h": "1"}}
	src1, rel1, err := pool.Obtain(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel1(false)

	src2, rel2, err := pool.Obtain(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel2(false)

	if opens.Load() != 1 {
		t.Fatalf("opens = %d, want 1 (reuse)", opens.Load())
	}
	if src1 != src2 {
		t.Fatal("expected same source instance from pool")
	}
	if pool.LiveCount() != 1 {
		t.Fatalf("LiveCount = %d", pool.LiveCount())
	}
}

func TestSourcePool_InvalidateOnRelease_ADR021(t *testing.T) {
	var opens atomic.Int32
	build := func(cfg contracts.SourceConfig) (contracts.Source, error) {
		opens.Add(1)
		return &testutil.FakeSource{KindName: "fake"}, nil
	}
	pool := pipeline.NewSourcePool(build, pipeline.PoolConfig{IdleTimeout: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	cfg := contracts.SourceConfig{SourceID: 1, SourceType: "fake", Params: map[string]string{}}
	_, rel, err := pool.Obtain(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel(true) // invalidate

	_, rel2, err := pool.Obtain(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel2(false)

	if opens.Load() != 2 {
		t.Fatalf("opens = %d, want 2 after invalidate", opens.Load())
	}
}

func TestSourcePool_FingerprintChange_Reopens_ADR021(t *testing.T) {
	var opens atomic.Int32
	build := func(cfg contracts.SourceConfig) (contracts.Source, error) {
		opens.Add(1)
		return &testutil.FakeSource{KindName: "fake"}, nil
	}
	pool := pipeline.NewSourcePool(build, pipeline.PoolConfig{IdleTimeout: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	cfg := contracts.SourceConfig{SourceID: 1, SourceType: "fake", Params: map[string]string{"p": "a"}}
	_, rel, err := pool.Obtain(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel(false)

	cfg.Params = map[string]string{"p": "b"}
	_, rel2, err := pool.Obtain(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel2(false)

	if opens.Load() != 2 {
		t.Fatalf("opens = %d, want 2 after fingerprint change", opens.Load())
	}
}

func TestSourcePool_IdleEvicts_ADR021(t *testing.T) {
	var opens atomic.Int32
	build := func(cfg contracts.SourceConfig) (contracts.Source, error) {
		opens.Add(1)
		return &testutil.FakeSource{KindName: "fake"}, nil
	}
	pool := pipeline.NewSourcePool(build, pipeline.PoolConfig{IdleTimeout: 10 * time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	cfg := contracts.SourceConfig{SourceID: 1, SourceType: "fake", Params: map[string]string{}}
	_, rel, err := pool.Obtain(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel(false)
	time.Sleep(25 * time.Millisecond)
	_, rel2, err := pool.Obtain(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel2(false)
	if opens.Load() != 2 {
		t.Fatalf("opens = %d, want 2 after idle eviction", opens.Load())
	}
}

func TestRunner_PoolInvalidatesAfterReadError_ADR021(t *testing.T) {
	var opens atomic.Int32
	build := func(cfg contracts.SourceConfig) (contracts.Source, error) {
		opens.Add(1)
		return &testutil.FakeSource{
			KindName: "fake",
			ReadFn: func(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
				return contracts.ReadResult{}, errors.New("boom")
			},
		}, nil
	}
	pool := pipeline.NewSourcePool(build, pipeline.PoolConfig{IdleTimeout: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	store := &testutil.FakeRunStore{}
	sink := &testutil.FakeSink{Exists: true}
	r := pipeline.NewRunnerWithBuilder(store, nil, sink, runstate.New(), testLogger(), build, pool)

	load := sampleLoad(string(contracts.LoadModeAppend))
	_ = r.Execute(context.Background(), load)
	_ = r.Execute(context.Background(), load)

	if opens.Load() != 2 {
		t.Fatalf("opens = %d, want 2 (invalidate after read error)", opens.Load())
	}
}
