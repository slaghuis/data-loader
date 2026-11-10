// Package backfill implements ADR-022 dual-cursor historic fill for SQL delta loads.
//
// A parent load with BackfillRequired gets a sibling meta_loads row that walks
// from BackfillStop up to a frozen BackfillCeiling while the parent continues
// forward from its ADR-006 first-run watermark.
package backfill
