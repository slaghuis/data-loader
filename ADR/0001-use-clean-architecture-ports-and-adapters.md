---
id: ADR-001
title: "Use Clean Architecture (ports and adapters)"
status: Accepted
date: 2026-09-16
tags: [architecture, clean-architecture]
---
# ADR-001: Use Clean Architecture (ports and adapters)

## Status

Accepted

## Date

2026-09-16 (recorded 2026-10-14)

## Context

The loader integrates with four source protocols today, and more are expected. Phase 4 adds more sinks. The system will evolve over time and integrate with multiple infrastructure providers.

## Decision

Use Clean Architecture. The domain contracts (`pkg/contracts`: `Source`, `Sink`, `Batch`, `Watermark`) are independent of drivers. Adapters (`internal/sources/*`, `internal/sink`) implement them, and the application layer (`pipeline`, `scheduler`) orchestrates through them.

## Alternatives Considered

- One package per source with its own end-to-end pipeline (duplicated scheduling, watermark and audit logic).
- A generic ETL framework or a third-party connector platform (heavy for an edge appliance, and no Modbus/OPC UA focus).

## Consequences

Positive:

- Testable: engines can be tested with fake sources and sinks.
- Maintainable: a new protocol is a new package.
- Technology independent: the Phase 4 sinks need no change to sources.

Negative:

- More boilerplate.
- Phase 1 conformed only partially. Phase 2 introduced narrow ports for the runner/scheduler/logging and removed telemetry adapters' `internal/metadata` imports. Remaining DI (housekeeping, clocks, domain extract) is Phase 2.1.
