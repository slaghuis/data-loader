package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/slaghuis/data-loader/internal/metadata"
)

// LogWriter persists a log entry. *metadata.Repository implements this.
type LogWriter interface {
	Log(ctx context.Context, e metadata.LogEntry) error
}

// sharedState is shared across WithAttrs / WithGroup clones of a DBHandler.
type sharedState struct {
	writer  LogWriter
	ch      chan metadata.LogEntry
	done    chan struct{}
	closed  atomic.Bool
	dropped atomic.Uint64
}

// DBHandler mirrors slog records into the metadata DB while delegating to a base handler.
type DBHandler struct {
	base   slog.Handler
	state  *sharedState
	attrs  []slog.Attr // keys already include any group prefix applied at WithAttrs time
	groups []string    // active group stack for subsequent WithAttrs and record attrs
}

// NewDBHandler starts a background worker that drains buffered DB log writes.
func NewDBHandler(base slog.Handler, writer LogWriter, buffer int) *DBHandler {
	if buffer <= 0 {
		buffer = 256
	}
	st := &sharedState{
		writer: writer,
		ch:     make(chan metadata.LogEntry, buffer),
		done:   make(chan struct{}),
	}
	h := &DBHandler{base: base, state: st}
	go h.worker()
	return h
}

// Dropped returns how many DB log entries were discarded because the buffer was full.
func (h *DBHandler) Dropped() uint64 {
	return h.state.dropped.Load()
}

func (h *DBHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.base.Enabled(ctx, l)
}

func (h *DBHandler) Handle(ctx context.Context, r slog.Record) error {
	if err := h.base.Handle(ctx, r); err != nil {
		return err
	}

	entry := metadata.LogEntry{
		CreatedAt: r.Time.UTC(),
		Level:     levelString(r.Level),
		Message:   r.Message,
	}

	details := map[string]any{}
	apply := func(a slog.Attr) {
		a.Value = a.Value.Resolve()
		switch a.Key {
		case "run_uuid":
			entry.RunUUID = a.Value.String()
		case "load_id":
			if v := int64FromAttr(a.Value); v != 0 {
				entry.LoadID = &v
			}
		case "source_id":
			if v := int64FromAttr(a.Value); v != 0 {
				entry.SourceID = &v
			}
		case "category":
			entry.Category = a.Value.String()
		default:
			details[a.Key] = a.Value.Any()
		}
	}

	for _, a := range h.attrs {
		apply(a)
	}
	prefix := strings.Join(h.groups, ".")
	r.Attrs(func(a slog.Attr) bool {
		if prefix != "" {
			a.Key = prefix + "." + a.Key
		}
		apply(a)
		return true
	})
	if len(details) > 0 {
		if b, err := json.Marshal(details); err == nil {
			entry.Details = string(b)
		}
	}

	if h.state.closed.Load() {
		return nil
	}
	select {
	case h.state.ch <- entry:
	default:
		h.state.dropped.Add(1)
	}
	return nil
}

func (h *DBHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	prefix := strings.Join(h.groups, ".")
	newAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	newAttrs = append(newAttrs, h.attrs...)
	for _, a := range attrs {
		if prefix != "" {
			a.Key = prefix + "." + a.Key
		}
		newAttrs = append(newAttrs, a)
	}
	return &DBHandler{
		base:   h.base.WithAttrs(attrs),
		state:  h.state,
		attrs:  newAttrs,
		groups: h.groups,
	}
}

func (h *DBHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := make([]string, len(h.groups)+1)
	copy(groups, h.groups)
	groups[len(h.groups)] = name
	return &DBHandler{
		base:   h.base.WithGroup(name),
		state:  h.state,
		attrs:  h.attrs,
		groups: groups,
	}
}

// Close stops accepting DB writes, drains the buffer, and waits up to timeout.
// It is safe to call more than once. Logging after Close still reaches the base
// handler but is not enqueued to the database (D-02).
func (h *DBHandler) Close(timeout time.Duration) {
	if !h.state.closed.Swap(true) {
		close(h.state.ch)
	}
	select {
	case <-h.state.done:
	case <-time.After(timeout):
	}
}

func (h *DBHandler) worker() {
	defer close(h.state.done)
	ctx := context.Background()
	for entry := range h.state.ch {
		// Best-effort persist; ignore errors (writing them via slog would recurse).
		_ = h.state.writer.Log(ctx, entry)
	}
}

func int64FromAttr(v slog.Value) int64 {
	switch v.Kind() {
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return int64(v.Uint64()) //nolint:gosec // load/source IDs fit in int64
	case slog.KindFloat64:
		return int64(v.Float64())
	default:
		return 0
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
