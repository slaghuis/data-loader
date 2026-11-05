package scheduler_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/pipeline"
	"github.com/slaghuis/data-loader/internal/runstate"
	"github.com/slaghuis/data-loader/internal/scheduler"
	"github.com/slaghuis/data-loader/internal/testutil"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

type fakeRepo struct {
	mu          sync.Mutex
	abortCalls  []string
	abortCount  int64
	loads       []metadata.Load
	fingerprint string
}

func (f *fakeRepo) AbortRunningRuns(_ context.Context, reason string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abortCalls = append(f.abortCalls, reason)
	return f.abortCount, nil
}

func (f *fakeRepo) ListEnabledLoads(context.Context) ([]metadata.Load, error) {
	return f.loads, nil
}

func (f *fakeRepo) MetadataFingerprint(context.Context) (string, error) {
	return f.fingerprint, nil
}

func TestScheduler_Load_AbortsOrphans_D09(t *testing.T) {
	repo := &fakeRepo{abortCount: 2, fingerprint: "fp1"}
	sink := &testutil.FakeSink{}
	store := &testutil.FakeRunStore{}
	runner := pipeline.NewRunnerWithBuilder(store, nil, sink, runstate.New(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(cfg contracts.SourceConfig) (contracts.Source, error) {
			return &testutil.FakeSource{KindName: "fake"}, nil
		})
	sch := scheduler.New(repo, runner, runstate.New(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)

	if err := sch.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.abortCalls) != 1 {
		t.Fatalf("abortCalls = %v, want 1 startup abort", repo.abortCalls)
	}
	if repo.abortCalls[0] == "" {
		t.Fatal("expected non-empty abort reason")
	}
}

func TestScheduler_Stop_AbortsRunning_D09(t *testing.T) {
	repo := &fakeRepo{fingerprint: "fp1", abortCount: 1}
	sink := &testutil.FakeSink{}
	store := &testutil.FakeRunStore{}
	runner := pipeline.NewRunnerWithBuilder(store, nil, sink, runstate.New(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(cfg contracts.SourceConfig) (contracts.Source, error) {
			return &testutil.FakeSource{KindName: "fake"}, nil
		})
	sch := scheduler.New(repo, runner, runstate.New(), slog.New(slog.NewTextHandler(io.Discard, nil)), 50*time.Millisecond)

	sch.Start()
	sch.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	found := false
	for _, r := range repo.abortCalls {
		if r == "aborted: shutdown timeout" || r != "" {
			// Stop always calls AbortRunningRuns; startup not called here.
			found = true
		}
	}
	if !found {
		t.Fatalf("abortCalls = %v", repo.abortCalls)
	}
}
