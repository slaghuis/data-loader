---
id: ADR-021
title: "Source connection reuse across runs"
status: Proposed
date: 2026-11-05
tags: [sources, performance, connections]
---
# ADR-021: Source connection reuse across runs

## Status

Proposed

## Date

2026-11-05

## Context

Every run currently opens a source connection in `Runner.Execute` and closes it at the end (D-17). That is correct for correctness and leak avoidance, but Modbus/OPC UA and SQL sources pay connect latency on every cron tick. High-frequency telemetry loads (every 5–10 s) amplify the cost.

## Decision (proposed)

Introduce an optional **connection cache** owned by the application layer:

- Keyed by `source_id` (and a fingerprint of resolved params).
- Bounded TTL / idle timeout; closed on metadata reload that changes params or disables the source.
- Sources remain free of package-level globals; the runner (or a small `SourcePool`) holds the cache behind an interface.
- Health `/readyz` continues to ping live pooled connections when present.

Default remains open-per-run until a site opts in via config (e.g. `LOADER_SOURCE_POOL=true`).

## Alternatives Considered

- Always pool (harder to reason about stale sessions, Modbus unit id, OPC UA session expiry).
- Per-module internal pools (duplicates lifecycle logic; fights Clean Architecture).
- Keep open-per-run forever (simplest; may be enough for infrequent SQL loads).

## Consequences

Positive:

- Lower poll latency for telemetry.
- Fewer TCP/TLS handshakes against OT devices.

Negative:

- Must handle session expiry (OPC UA `BadSessionIDInvalid`) and Modbus transport resets.
- Reload and shutdown must drain the pool cleanly.
- Tests need a fake pool.

## Notes

Do **not** implement until this ADR is Accepted.
