package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/slaghuis/data-loader/internal/metadata"
)

// DBHandler mirrors slog records into the metadata DB while delegating to a base handler.
type DBHandler struct {
	base slog.Handler
	repo *metadata.Repository
	// buffered writes to avoid blocking the caller
	ch   chan metadata.LogEntry
	done chan struct{}
}

func NewDBHandler(base slog.Handler, repo *metadata.Repository, buffer int) *DBHandler {
	if buffer <= 0 {
		buffer = 256
	}
	h := &DBHandler{
		base: base,
		repo: repo,
		ch:   make(chan metadata.LogEntry, buffer),
		done: make(chan struct{}),
	}
	go h.worker()
	return h
}

func (h *DBHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.base.Enabled(ctx, l)
}

func (h *DBHandler) Handle(ctx context.Context, r slog.Record) error {
	// Always call the base handler (console).
	if err := h.base.Handle(ctx, r); err != nil {
		return err
	}

	entry := metadata.LogEntry{
		CreatedAt: r.Time.UTC(),
		Level:     levelString(r.Level),
		Message:   r.Message,
	}

	// Collect attributes; promote well-known keys to columns.
	details := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "run_uuid":
			entry.RunUUID = a.Value.String()
		case "load_id":
			if v := a.Value.Int64(); v != 0 {
				entry.LoadID = &v
			}
		case "source_id":
			if v := a.Value.Int64(); v != 0 {
				entry.SourceID = &v
			}
		case "category":
			entry.Category = a.Value.String()
		default:
			details[a.Key] = a.Value.Any()
		}
		return true
	})
	if len(details) > 0 {
		if b, err := json.Marshal(details); err == nil {
			entry.Details = string(b)
		}
	}

	// Non-blocking send; drop if the buffer is full.
	select {
	case h.ch <- entry:
	default:
		// buffer full — drop and let console record survive
	}
	return nil
}

func (h *DBHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &DBHandler{base: h.base.WithAttrs(attrs), repo: h.repo, ch: h.ch, done: h.done}
}

func (h *DBHandler) WithGroup(name string) slog.Handler {
	return &DBHandler{base: h.base.WithGroup(name), repo: h.repo, ch: h.ch, done: h.done}
}

// Close flushes pending log writes.
func (h *DBHandler) Close(timeout time.Duration) {
	close(h.ch)
	select {
	case <-h.done:
	case <-time.After(timeout):
	}
}

func (h *DBHandler) worker() {
	defer close(h.done)
	ctx := context.Background()
	for entry := range h.ch {
		// Best-effort persist; ignore errors (they'll only be visible to the app itself,
		// so writing them via slog would recurse).
		_ = h.repo.Log(ctx, entry)
	}
}

func levelString(l slog.Level) string {
	switch {
	case l <= slog.LevelDebug:
		return "debug"
	case l <= slog.LevelInfo:
		return "info"
	case l <= slog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}