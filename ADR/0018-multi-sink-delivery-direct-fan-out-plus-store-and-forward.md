---
id: ADR-018
title: "Multi-sink delivery: direct fan-out plus store-and-forward"
status: Proposed
date: 2026-10-14
tags: [sinks, messaging, reliability]
---
# ADR-018: Multi-sink delivery: direct fan-out plus store-and-forward

## Status

Proposed

## Date

2026-10-14

## Context

Phase 4 delivers loads to several sinks, including Microsoft Fabric over an unreliable WAN. Requirements: read sources once (BR-003), local loads unaffected by cloud outages (BR-005, FR-033), and no data loss.

## Decision

- Introduce a sink registry (`meta_sinks`) and per-load targets (`meta_load_targets`). Each sink is either **direct** or **forward**.
- **Direct** targets (LAN, for example the local MSSQL sink) receive batches inline from the runner. The **primary** target, `local`, determines run success.
- **Forward** targets (WAN, for example Fabric) are fed by a separate **forwarder** that reads completed runs' rows from the local sink table by `yb_load_timestamp`, in order, and delivers them. A per-target cursor (`meta_target_cursors`) advances only after a confirmed upload. Delivery is at-least-once, and remote sinks de-duplicate by key where supported.
- To avoid forwarding a partially written run, the run's load timestamp is stored in `meta_load_runs`, and the forwarder only forwards timestamps of runs with status `success`.
- `replace` loads on forward targets are sent as a full snapshot generation.

## Alternatives Considered

- **Synchronous fan-out to all targets.** A WAN outage would fail or stall local loads.
- **Per-target watermarks with separate source reads.** Multiplies load on production systems (violates BR-003).
- **An edge message broker** (Kafka/MQTT) between the loader and sinks. Robust, but a heavy operational burden per site.

## Consequences

Positive:

- Local SQL Server becomes the durable buffer at no extra cost. Cloud outages only create backlog. Sources are read once.

Negative:

- Forward targets require the local sink (it cannot be removed).
- `yb_load_timestamp` must be indexed in sink tables.
- Additional latency for remote targets (forwarder interval).
