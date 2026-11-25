---
id: ADR-023
title: "Opt-in datetime watermark overlap window"
status: Accepted
date: 2026-11-25
tags: [watermark, data-loading]
---
# ADR-023: Opt-in datetime watermark overlap window

## Status

Accepted ([GitHub #9](https://github.com/slaghuis/data-loader/issues/9))

## Date

2026-11-25 (proposed and accepted)

## Context

Delta loads store a forward high-water mark per load ([ADR-006](0006-watermark-model-string-values-separate-table-first-run-initi.md)). SQL sources read `watermark > last AND watermark <= upper`, then advance the stored watermark to `upper` after a successful run.

That model misses rows whose watermark value is **lower than a value already loaded** — late-arriving commits, out-of-order inserts, or in-place updates that bump a “modified at” column after the row was already past the cursor. ADR-006 already lists this as a known negative consequence. Delivery is **at-least-once**; duplicates after a crash are already accepted in the NFRs, with downstream de-duplication via `yb_load_timestamp` and business keys.

Feature request #9 asks for a **watermark overlap window**: rewind the **query** lower bound by a configured duration so those rows are re-read. The product choice for this ADR is:

- Ship the overlap on the **read** path.
- Allow **duplicate rows** in the local sink (bulk insert unchanged).
- **Do not** ship sink MERGE or delete-before-insert in this decision.

## Decision

**Accepted: opt-in datetime-only overlap** that affects the delta **lower bound only**:

1. **Metadata** on `meta_loads`: `watermark_overlap_sec` (int, not null, default **0**).
   - `0` = current behaviour (no overlap).
   - `> 0` = subtract that many seconds from the stored watermark when building the lower bound.
   - Reject negative values at validation / seed / (later) API time.
   - Cap at **604800** (7 days) to limit accidental full-history pressure (BR-003).
2. **Scope:** SQL `mode=delta` with `watermark_type=datetime` only. Numeric watermarks and telemetry loads ignore the field (treat as 0); setting a non-zero overlap on a non-datetime delta load is a configuration error.
3. **Query shape** (SQL Server and PostgreSQL, same predicate family as today):

   `watermark > (last − overlap) AND watermark <= upper`

   where `last` is the stored high-water mark and `upper` remains the pre-read `MAX` snapshot. Apply the subtraction in the loader (parsed RFC 3339 → subtract duration → bind as RFC 3339 / `time.Time`).
4. **Stored watermark:** still advances to `upper` (or the existing successful-run rule) after a good run. **Never** persist the rewound lower bound as `meta_watermarks.value`.
5. **First run (ADR-006):** unchanged — empty watermark → store `MAX`, load no rows. Overlap does not apply until a stored watermark exists.
6. **Sink:** keep bulk insert. Overlap **intentionally produces duplicates** for rows already loaded whose watermark falls inside the window. Consumers de-duplicate as they do for crash retries.
7. **ADR-022 backfill siblings:** overlap applies only to **forward** (parent) loads. Sibling ceiling-capped reads leave overlap unused (effective 0) so history fill stays a clean `(stop, ceiling]` walk.
8. ADR-006 remains Accepted; this ADR **extends** the delta read contract. Default `0` preserves BR-003 and existing site behaviour.

## Alternatives Considered

### A. Sink MERGE / UPSERT on business keys (#9 optional)

- **Pros:** Target stays “current row” without analyst-side de-dupe.
- **Cons:** Needs key columns, a new sink write path, and performance/correctness work; overlaps Phase 4 Fabric upsert/`key_columns` design.
- **Verdict:** Rejected for this ADR. May return as a later sink write-mode ADR.

### B. Delete sink rows newer than the adjusted lower bound, then insert (#9 optional)

- **Pros:** Fewer duplicates in the local table.
- **Cons:** Extra DML on every tick; easy to get wrong with types/nulls; erases the append-style load history stamped by `yb_load_timestamp` in that window.
- **Verdict:** Rejected for this ADR.

### C. Persist a rewound watermark instead of a query-only overlap

- **Pros:** Simple mental model (“watermark moves back”).
- **Cons:** Loses the true high-water mark; next run without overlap would re-read large ranges; fights audit/`previous_value` semantics.
- **Verdict:** Rejected. Overlap is query-time only.

### D. Support numeric overlap (e.g. subtract N from an integer key)

- **Pros:** Symmetric API for all watermark types.
- **Cons:** “Seconds” do not apply; units are ambiguous; #9’s motivating case is datetime / modified-at style columns.
- **Verdict:** Rejected for v1. Revisit only with an explicit unit model.

### E. Do nothing; rely on manual watermark rewind

- **Pros:** Zero product complexity.
- **Cons:** Does not meet #9; operators cannot safely cover a standing late-arrival window without perpetual manual resets.
- **Verdict:** Rejected as the product answer.

## Consequences

Positive:

- Late-arriving and updated rows inside the window are re-read without weakening ADR-006’s default first-run safety.
- Metadata-driven, per-load, default off.
- Sink path and at-least-once semantics stay simple; duplicates are an explicit, documented trade-off.

Negative / follow-ups:

- Larger overlap + high update rates amplify row volume and sink growth; operators must size the window and retention.
- Downstream analytics must de-duplicate (already required for crash retries).
- Goose migration for `watermark_overlap_sec`; seed YAML + Phase 3 Load CRUD validation; fingerprint include the new field.
- Docs: FR extension to FR-004, architecture data model, runbook guidance on choosing a window.
