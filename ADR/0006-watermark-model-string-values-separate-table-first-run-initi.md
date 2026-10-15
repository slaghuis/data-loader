---
id: ADR-006
title: "Watermark model: string values, separate table, first-run initialisation"
status: Accepted
date: 2026-09-16
tags: [watermark, data-loading]
---
# ADR-006: Watermark model: string values, separate table, first-run initialisation

## Status

Accepted

## Date

2026-09-16 (recorded 2026-10-14)

## Context

Incremental loads need a durable high-water mark per load. Watermark columns may be datetime or numeric. Large historical tables must not be back-loaded by accident (BR-003).

## Decision

- Store watermarks in `meta_watermarks` (one row per load) as **strings**, interpreted through `meta_loads.watermark_type`, and keep `previous_value`, `reset_at` and `reset_reason` for audit.
- **First run** stores the current `MAX(column)` and loads no rows.
- SQL Server snapshots the upper bound before reading (`> last AND <= upper`) so rows inserted during the read are picked up next time.
- The watermark advances only after a successful run.

## Alternatives Considered

- A watermark column on `meta_loads` (mixes user-owned configuration with loader-owned state, and has no audit trail).
- Typed columns per watermark type (schema churn).
- Back-load on first run (risky for multi-million-row tables).

## Consequences

Positive:

- Portable across types. Auditable. Safe default for new loads.

Negative:

- String formatting must be lossless. D-03 shows the SQL Server numeric path currently is not. Phase 2 stores the database's own textual representation.
- Watermarks miss rows whose watermark value is lower than one already loaded (late-arriving commits). Delivery is at-least-once, with possible duplicates after a crash.
- PostgreSQL does not use the upper-bound snapshot yet (D-18).
