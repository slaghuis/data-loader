package contracts

import "time"

// WatermarkType describes how to interpret a watermark value.
type WatermarkType string

const (
	WatermarkNone     WatermarkType = "none"
	WatermarkDatetime WatermarkType = "datetime"
	WatermarkNumeric  WatermarkType = "numeric"
)

// LoadMode determines how data is written to the sink.
type LoadMode string

const (
	LoadModeDelta   LoadMode = "delta"   // watermark-based incremental
	LoadModeAppend  LoadMode = "append"  // full read, append rows
	LoadModeReplace LoadMode = "replace" // truncate target, then load
)

// Watermark carries a typed watermark value as a string for portability.
type Watermark struct {
	Type  WatermarkType
	Value string // empty if none
}

// Column describes a column returned by a source or written to a sink.
type Column struct {
	Name     string
	DataType string // canonical type: string, int64, float64, bool, datetime, bytes
	Nullable bool
}

// Row is a single record. Keys are column names.
type Row map[string]any

// Batch is a chunk of rows returned by a source reader.
type Batch struct {
	Columns []Column
	Rows    []Row
}

// ReadResult carries the outcome of a source read.
type ReadResult struct {
	RowsRead      int64
	NewWatermark  Watermark // the watermark to persist after a successful load
	StartedAt     time.Time
	FinishedAt    time.Time
}