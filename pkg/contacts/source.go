package contracts

import "context"

// SourceConfig is passed to a source when instantiated. The Params map is
// module-specific (e.g. connection string, query, table name).
type SourceConfig struct {
	SourceID   int64
	SourceType string            // "sqlserver", "postgres", "modbus", "opcua"
	Name       string
	Params     map[string]string // decrypted connection details
}

// LoadConfig describes a single data load (a job) that the source must execute.
type LoadConfig struct {
	LoadID          int64
	Name            string
	ObjectName      string        // table/view name for SQL sources
	Mode            LoadMode
	WatermarkColumn string        // empty if none
	WatermarkType   WatermarkType
	LastWatermark   Watermark     // current known watermark, empty on first run
	BatchSize       int
}

// BatchHandler is called by a Source for each batch of rows read.
// Returning an error aborts the read.
type BatchHandler func(ctx context.Context, batch Batch) error

// Source is the interface every source module implements.
type Source interface {
	// Kind returns the module identifier, e.g. "sqlserver".
	Kind() string

	// Open validates and establishes the connection.
	Open(ctx context.Context) error

	// Ping checks connectivity without side effects.
	Ping(ctx context.Context) error

	// Read streams rows for the given load, invoking handler for each batch.
	// It returns the new watermark to persist and total rows read.
	Read(ctx context.Context, load LoadConfig, handler BatchHandler) (ReadResult, error)

	// Close releases resources.
	Close() error
}

// SourceFactory builds a Source from a SourceConfig. Each module registers one.
type SourceFactory func(cfg SourceConfig) (Source, error)