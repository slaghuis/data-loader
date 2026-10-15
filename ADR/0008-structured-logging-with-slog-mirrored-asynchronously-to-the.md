---
id: ADR-008
title: "Structured logging with slog, mirrored asynchronously to the database"
status: Accepted
date: 2026-09-16
tags: [logging, observability]
---
# ADR-008: Structured logging with slog, mirrored asynchronously to the database

## Status

Accepted

## Date

2026-09-16 (recorded 2026-10-14)

## Context

Connection attempts, loads, watermark changes and errors must be recorded in the metadata database (BR-004), and logging must never stall a load.

## Decision

Use `log/slog` only. `logging.DBHandler` wraps the console handler and enqueues entries to a bounded channel (512 entries) drained by one worker. When the channel is full, entries are dropped from the database copy only. The keys `run_uuid`, `load_id`, `source_id` and `category` are promoted to columns; other attributes go to `details` as JSON.

## Alternatives Considered

- Synchronous DB writes (a slow metadata database would stall loads).
- zap or zerolog (extra dependency with no real benefit since slog).

## Consequences

Positive:

- The console always gets every record. Queryable logs. No third-party logging library.

Negative:

- DB logs can be lost under pressure. Phase 2 adds a dropped-entries counter.
- The implementation currently loses `With` attributes (D-01) and panics after `Close` (D-02). Both are Phase 2 fixes.
