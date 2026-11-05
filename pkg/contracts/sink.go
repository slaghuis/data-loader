package contracts

import (
	"context"
	"time"
)

// TargetTable identifies where data should be written.
type TargetTable struct {
	Schema string
	Name   string
}

// Sink writes batches to the destination database.
type Sink interface {
	Open(ctx context.Context) error
	Ping(ctx context.Context) error

	// TableExists reports whether the target table is already present.
	TableExists(ctx context.Context, target TargetTable) (bool, error)

	// EnsureTable creates the target table if it does not exist,
	// based on the columns of the first batch. Always appends a
	// [load_timestamp] datetime2 column.
	EnsureTable(ctx context.Context, target TargetTable, columns []Column) error

	// Truncate empties the target table. Prefer AtomicReplacer for replace loads.
	Truncate(ctx context.Context, target TargetTable) error

	// WriteBatch inserts a batch of rows, stamping each row with loadTS.
	WriteBatch(ctx context.Context, target TargetTable, batch Batch, loadTS time.Time) (int64, error)

	Close() error
}

// AtomicReplacer is an optional Sink capability for all-or-nothing replace
// loads (ADR-020). Detect with a type assertion in the runner.
type AtomicReplacer interface {
	// BeginReplace prepares an empty staging table. When the live target
	// already exists, columns may be nil (schema is cloned from live).
	// When it does not, columns from the first batch are required.
	BeginReplace(ctx context.Context, target TargetTable, columns []Column) (staging TargetTable, err error)

	// CommitReplace atomically publishes staging as the live target.
	CommitReplace(ctx context.Context, target, staging TargetTable) error

	// AbortReplace drops staging without modifying the live target.
	AbortReplace(ctx context.Context, staging TargetTable) error
}
