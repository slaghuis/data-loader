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

	// EnsureTable creates the target table if it does not exist,
	// based on the columns of the first batch. Always appends a
	// [load_timestamp] datetime2 column.
	EnsureTable(ctx context.Context, target TargetTable, columns []Column) error

	// Truncate empties the target table (used for LoadModeReplace).
	Truncate(ctx context.Context, target TargetTable) error

	// WriteBatch inserts a batch of rows, stamping each row with loadTS.
	WriteBatch(ctx context.Context, target TargetTable, batch Batch, loadTS time.Time) (int64, error)

	Close() error
}
