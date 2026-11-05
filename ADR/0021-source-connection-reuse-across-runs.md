---
id: ADR-021
title: "Source connection reuse across runs"
status: Accepted
date: 2026-11-05
tags: [sources, performance, connections]
---
# ADR-021: Source connection reuse across runs

## Status

Accepted

## Date

2026-11-05 (proposed and accepted)

## Context

Every run previously opened a source connection in `Runner.Execute` and closed it at the end (D-17). That is correct for correctness and leak avoidance, but Modbus/OPC UA and SQL sources pay connect latency on every cron tick. High-frequency telemetry loads (every 5–10 s) amplify the cost.

## Decision

Introduce an optional **connection cache** (`pipeline.SourcePool`) owned by the application layer:

- Keyed by `source_id` and a fingerprint of kind + resolved params.
- Per-source mutex so loads that share a source serialize on one connection.
- Idle timeout and optional max age; expired entries are closed on the next acquire.
- Closed / invalidated on metadata reload and on process shutdown.
- After a failed `Read`, the pooled entry is invalidated so the next run reconnects (session expiry / transport reset).
- Sources remain free of package-level globals; the runner talks to a `SourceSession` interface.
- When enabled, `/readyz` pings each live pooled connection.

Default remains open-per-run until a site opts in via `LOADER_SOURCE_POOL=true`. Related knobs: `LOADER_SOURCE_POOL_IDLE_SEC` (default 300), `LOADER_SOURCE_POOL_MAX_AGE_SEC` (default 3600; `0` disables max age).

## Alternatives Considered

- Always pool (harder to reason about stale sessions).
- Per-module internal pools (duplicates lifecycle logic; fights Clean Architecture).
- Keep open-per-run forever (simplest; may be enough for infrequent SQL loads).

## Consequences

Positive:

- Lower poll latency for telemetry when opted in.
- Fewer TCP/TLS handshakes against OT devices.

Negative:

- Concurrent loads against the same `source_id` are serialized while a lease is held.
- Failed reads force reconnect even when the failure was not connection-related.
- Operators must enable the pool explicitly and tune idle/max-age for their OT estate.
