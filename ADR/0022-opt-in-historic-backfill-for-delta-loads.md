---
id: ADR-022
title: "Opt-in historic backfill for SQL delta loads"
status: Accepted
date: 2026-11-09
tags: [watermark, data-loading, backfill]
---
# ADR-022: Opt-in historic backfill for SQL delta loads

## Status

Accepted ([GitHub #7](https://github.com/slaghuis/data-loader/issues/7))

## Date

2026-11-09 (proposed and accepted)

## Context

Analytics often need year-on-year or year-to-date history in the sink. Today, [ADR-006](0006-watermark-model-string-values-separate-table-first-run-initi.md) initialises a new delta load by storing `MAX(watermark_column)` and loading **no rows**. That default protects [BR-003](../docs/01-project-requirements.md) (no accidental full-history load).

Operators therefore cannot intentionally load a historic window without manually rewinding `meta_watermarks` and waiting for normal delta ticks to catch up — which either floods the source or takes a very long time.

Feature request #7 asks for an **opt-in** backfill. Two shapes were proposed:

| | Preferred (issue) | Alternative (issue) |
|---|-------------------|---------------------|
| Idea | Flag + stop watermark; schedule a **second** backfill job ~1 minute after the forward load; same `batch_size`; auto-deschedule when done | Flag + optional start watermark; seed the **single** load watermark and let normal delta catch up |
| Forward progress | Immediate (ADR-006 first-run, then live deltas) | Blocked until catch-up reaches “now” |
| Extra state | Second cursor / second scheduled unit of work | None beyond seed value |
| Source load | Parallel pressure if both run; mitigated by stagger + batch size | Serial only; long catch-up window |

Constraints that any choice must keep:

- Default first-run behaviour of ADR-006 **unchanged** when backfill is not requested.
- Sources remain read-only (BR-008).
- Configuration stays metadata-driven (BR-002).
- Runs remain auditable (BR-004): who asked for backfill, what range, when it finished.
- Scope is **SQL `mode=delta` only**. Telemetry sources stay append-only ([ADR-007](0007-telemetry-sources-forced-append-and-a-narrow-schema.md)); historic backfill does not apply.

## Decision

**Accepted: Preferred dual-cursor model** via a sibling `meta_loads` row (not a hidden internal job, not a separate `meta_backfills` table).

1. **Parent opt-in fields** on `meta_loads`:
   - `backfill_required` (bit, default 0)
   - `backfill_stop` (nvarchar, nullable) — same type semantics as the load’s watermark. Empty/`NULL` means resolve once via source `MIN(watermark_column)` (or equivalent) and persist that concrete value before the sibling starts reading.
   - `backfill_completed_at` (datetime2, nullable) — set when the sibling finishes.
2. **Forward (parent) load** keeps ADR-006: first run stores `MAX`, loads 0 rows, then normal incremental ticks.
3. **Sibling backfill load** is a second `meta_loads` row:
   - `backfill_of_load_id` (FK → parent `meta_loads.id`, unique when not null) marks the sibling.
   - Copies parent `source_id`, `object_name`, target, `mode=delta`, watermark column/type, `batch_size`, `auto_create_table`.
   - `cron_expression` is the parent expression offset by **+1 minute** (or `@every` + 1m) when that is expressible; otherwise an explicit offset cron supplied at creation.
   - Created **disabled** until the parent has a stored watermark (after first-run init). On activation, freeze `backfill_ceiling` on the sibling as a copy of the parent’s watermark **at activation time** (the ADR-006 first-run `MAX`). Later parent advances must not raise the sibling’s ceiling.
   - Sibling watermark is seeded to the resolved `backfill_stop` (exclusive lower bound, same as normal delta `>` last).
4. **Read bounds** (SQL Server and PostgreSQL, aligned with existing delta):  
   `watermark > last AND watermark <= min(snapshot_upper, backfill_ceiling)`  
   with `ORDER BY watermark` and the usual `batch_size` cap. When `last >= backfill_ceiling` (or a successful run writes an end watermark equal to the ceiling with zero rows left in range), the backfill is **complete**.
5. **Completion**: disable the sibling (`is_enabled=0`), set parent `backfill_required=0` and `backfill_completed_at=now`, and record `reset_reason` / log category `watermark` with an audit-friendly message. Do not delete the sibling row (keeps run history joinable).
6. **Re-entry**: a new backfill after completion is an explicit metadata change (new `backfill_stop` + `backfill_required=1`), which may recreate or re-enable a sibling with a new ceiling snapshot — never an automatic rewind of the parent.
7. **Out of scope:** `append` / `replace` modes; Modbus / OPC UA; changing ADR-006’s default first-run behaviour.

ADR-006 remains Accepted; this ADR **extends** the watermark lifecycle for opt-in history.

## Alternatives Considered

### A. Alternative from #7 — seed the single watermark and catch up

Set the load’s watermark to `backfill_stop` (or empty → treat as before-min) and run normal delta until caught up with live data.

- **Pros:** Smallest change to ADR-006’s one-row-per-load model; no dual writers; easy to reason about; reuses existing runner/scheduler.
- **Cons:** Forward “live” data is delayed for the entire historic catch-up (issue risk); a large table can monopolise the schedule for a long time; harder to pause/cancel backfill without also pausing live loading unless extra mode flags are added anyway.
- **Verdict:** Rejected as the primary design. Remains a valid **manual ops workaround** (watermark reset / seed).

### B. Preferred as a hidden internal task (not a second load row)

Runner spawns an in-process backfill loop beside the cron entry.

- **Pros:** No second row for operators to see.
- **Cons:** Weak BR-004 audit story; fights metadata-driven scheduling (ADR-004 / ADR-009); overlap guard and reload semantics become special cases.
- **Verdict:** Rejected.

### C. Sibling state in `meta_backfills` instead of a second `meta_loads` row

- **Pros:** Parent schedule table stays “one row per feed”.
- **Cons:** Duplicates scheduling, overlap guard, run audit, and reload wiring already built for loads.
- **Verdict:** Rejected in favour of a sibling `meta_loads` row with `backfill_of_load_id`.

### D. Keep ADR-006 only; document manual rewind

- **Verdict:** Rejected as the product answer to #7.

### E. Change default first-run to back-load history

- Already rejected by ADR-006 / BR-003.
- **Verdict:** Rejected again.

## Consequences

Positive:

- Intentional historic load without weakening the safe default for new loads.
- Live forward loading starts immediately while history fills in under a controlled batch size.
- Clear completion and audit trail; sibling runs appear in `meta_load_runs` like any other load.
- Matches #7 acceptance criteria (optional stop watermark, separate schedule, range fill, deschedule when done).

Negative / follow-ups:

- Two writers to one sink table rely on the frozen ceiling for range partitioning; boundary tests are mandatory (inclusive upper at ceiling, exclusive lower at last).
- Sibling lifecycle (create, wait for parent WM, offset cron, complete, disable) must survive hot reload (ADR-009) and process restart.
- Goose migration for new columns; seed YAML / Phase 3 API validation (`backfill_stop` type matches `watermark_type`; only SQL delta parents).
- Source connection pool (ADR-021): parent and sibling sharing a `source_id` serialize while a lease is held — document for operators.
- Implementation tracked as Phase 2.2 in [03 Implementation Plan](../docs/03-implementation-plan.md).
