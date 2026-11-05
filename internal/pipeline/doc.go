// Package pipeline orchestrates a single load run: open source, manage
// watermarks and run records, write batches to the sink, and update runstate.
//
// Invariants: sources are never written to; replace truncates before read
// (atomic staging is ADR-020); a failed watermark upsert fails the run (D-04).
package pipeline
