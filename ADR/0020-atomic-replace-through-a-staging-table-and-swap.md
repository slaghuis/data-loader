---
id: ADR-020
title: "Atomic replace through a staging table and swap"
status: Accepted
date: 2026-11-05
tags: [sinks, data-loading, replace]
---
# ADR-020: Atomic replace through a staging table and swap

## Status

Accepted

## Date

2026-11-05 (proposed and accepted)

## Context

`replace` mode previously truncated the sink target **before** reading the source (D-05 timing fix). A failed mid-load therefore left an empty or partial target. Operators need an all-or-nothing replace for dimension-style tables.

## Decision

For `LoadModeReplace`, sinks that implement the optional `contracts.AtomicReplacer` capability (MSSQL does):

1. Create (or recreate) a staging table `{target}_stg` with the same column shape as the live target (`SELECT * INTO … WHERE 1=0` when live exists; otherwise `EnsureTable` on staging from the first batch columns).
2. Stream source rows into the staging table only — the live table is never truncated mid-run.
3. On success, swap staging into the live name inside a single transaction using `sp_rename` (live → `{name}_old`, staging → live, drop `_old`). If live did not exist, rename staging to the live name.
4. On failure, leave the previous live table untouched and `DROP` the staging table.

Sinks without `AtomicReplacer` keep the legacy truncate-before-read behaviour.

## Alternatives Considered

- Keep truncate-before-read only: simple, but not atomic.
- Soft-delete + insert + purge: more complex watermarking of rows; harder for consumers.
- Sink-specific `MERGE` full refresh: vendor-specific and still not a true table swap.
- Adding replace methods to the core `Sink` interface: rejected — optional capabilities use type assertions (AI rules / Clean Architecture).

## Consequences

Positive:

- Failed replaces do not destroy good data.
- Zero-row replaces still clear the live table when it already exists (begin+commit empty staging).

Negative:

- Requires double disk for large tables during the run.
- `sp_rename` locks; concurrent readers may briefly block.
- `SELECT * INTO` copies columns only (indexes, FKs and triggers are not cloned onto staging).
