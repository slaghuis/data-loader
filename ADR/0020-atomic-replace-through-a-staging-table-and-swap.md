---
id: ADR-020
title: "Atomic replace through a staging table and swap"
status: Proposed
date: 2026-11-05
tags: [sinks, data-loading, replace]
---
# ADR-020: Atomic replace through a staging table and swap

## Status

Proposed

## Date

2026-11-05

## Context

`replace` mode currently truncates the sink target **before** reading the source (D-05 timing fix). A failed mid-load therefore leaves an empty or partial target. Operators need an all-or-nothing replace for dimension-style tables.

## Decision (proposed)

For `LoadModeReplace` against the MSSQL sink:

1. Create (or truncate) a staging table `{target}_stg` with the same schema as the target.
2. Stream source rows into the staging table.
3. On success, swap staging into the live name inside a single transaction using `sp_rename` (or equivalent) so readers never see a truncated empty table.
4. On failure, leave the previous live table untouched and drop/truncate staging.

## Alternatives Considered

- Keep truncate-before-read (current): simple, but not atomic.
- Soft-delete + insert + purge: more complex watermarking of rows; harder for consumers.
- Sink-specific `MERGE` full refresh: vendor-specific and still not a true table swap.

## Consequences

Positive:

- Failed replaces do not destroy good data.
- Matches the at-least-once / no-partial-replace expectation in architecture docs.

Negative:

- Requires double disk for large tables during the run.
- `sp_rename` locks; concurrent readers may briefly block.
- Needs careful handling of indexes, FKs, and AutoCreateTable.

## Notes

Do **not** implement until this ADR is Accepted.
