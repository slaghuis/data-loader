// Package pipeline orchestrates a single load run: open source, manage
// watermarks and run records, write batches to the sink, and update runstate.
//
// Invariants: sources are never written to; replace uses AtomicReplacer
// staging swap when available (ADR-020); optional SourcePool reuses connections
// across runs when LOADER_SOURCE_POOL is enabled (ADR-021); a failed watermark
// upsert fails the run (D-04).
package pipeline
