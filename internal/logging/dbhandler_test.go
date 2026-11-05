package logging_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/slaghuis/data-loader/internal/logging"
	"github.com/slaghuis/data-loader/internal/metadata"
)

type memWriter struct {
	mu      sync.Mutex
	entries []metadata.LogEntry
}

func (w *memWriter) Log(_ context.Context, e metadata.LogEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, e)
	return nil
}

func (w *memWriter) snapshot() []metadata.LogEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]metadata.LogEntry, len(w.entries))
	copy(out, w.entries)
	return out
}

func waitEntries(t *testing.T, w *memWriter, n int) []metadata.LogEntry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := w.snapshot()
		if len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d log entries; got %d", n, len(w.snapshot()))
	return nil
}

func TestDBHandler_WithAttrs_PersistsLoadID_D01(t *testing.T) {
	w := &memWriter{}
	base := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := logging.NewDBHandler(base, w, 32)
	defer h.Close(time.Second)

	logger := slog.New(h).With(
		"load_id", int64(42),
		"source_id", int64(7),
		"run_uuid", "run-abc",
		"load_name", "orders",
	)
	logger.Info("load started", "category", "read")

	got := waitEntries(t, w, 1)[0]
	if got.LoadID == nil || *got.LoadID != 42 {
		t.Fatalf("load_id = %v, want 42", got.LoadID)
	}
	if got.SourceID == nil || *got.SourceID != 7 {
		t.Fatalf("source_id = %v, want 7", got.SourceID)
	}
	if got.RunUUID != "run-abc" {
		t.Fatalf("run_uuid = %q, want run-abc", got.RunUUID)
	}
	if got.Category != "read" {
		t.Fatalf("category = %q, want read", got.Category)
	}
	if got.Message != "load started" {
		t.Fatalf("message = %q", got.Message)
	}
	if !cmp.Equal(got.Details, `{"load_name":"orders"}`) &&
		!cmp.Equal(got.Details, `{"load_name":"orders"}`, cmpopts.EquateEmpty()) {
		// details JSON object order is stable for a single key
		if got.Details != `{"load_name":"orders"}` {
			t.Fatalf("details = %q, want load_name", got.Details)
		}
	}
}

func TestDBHandler_Close_ThenLog_NoPanic_D02(t *testing.T) {
	w := &memWriter{}
	base := slog.NewJSONHandler(io.Discard, nil)
	h := logging.NewDBHandler(base, w, 8)
	logger := slog.New(h)

	logger.Info("before close")
	_ = waitEntries(t, w, 1)

	h.Close(time.Second)
	// Must not panic on send-to-closed-channel (D-02).
	logger.Info("data-loader stopped")
	h.Close(time.Second) // idempotent

	got := w.snapshot()
	for _, e := range got {
		if e.Message == "data-loader stopped" {
			t.Fatalf("post-close message was persisted to DB; entries=%+v", got)
		}
	}
}

type blockingWriter struct {
	release chan struct{}
}

func (w *blockingWriter) Log(context.Context, metadata.LogEntry) error {
	<-w.release
	return nil
}

func TestDBHandler_BufferFull_IncrementsDropped(t *testing.T) {
	// Block the worker so the buffer cannot drain while we flood.
	bw := &blockingWriter{release: make(chan struct{})}
	base := slog.NewJSONHandler(io.Discard, nil)
	h := logging.NewDBHandler(base, bw, 1)
	defer func() {
		close(bw.release)
		h.Close(time.Second)
	}()

	logger := slog.New(h)
	for i := 0; i < 64; i++ {
		logger.Info("flood", "i", i)
	}
	if h.Dropped() == 0 {
		t.Fatalf("expected dropped > 0 with blocked worker, got 0")
	}
}
