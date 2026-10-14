---
id: ADR-002
title: "Implement in Go"
status: Accepted
date: 2026-09-16
tags: [language, go]
---
# ADR-002: Implement in Go

## Status

Accepted

## Date

2026-09-16 (recorded 2026-10-14)

## Context

The loader runs unattended on mine-site servers. It needs a small footprint, simple deployment, strong concurrency for many schedules, and mature drivers for SQL Server, PostgreSQL, Modbus and OPC UA.

## Decision

Go (currently 1.26.9). It produces a single static binary, and the drivers used are `go-mssqldb`, `pgx`, `simonvetter/modbus` and `gopcua`.

## Alternatives Considered

- .NET: excellent SQL Server and OPC UA (OPC Foundation stack) support, but a heavier runtime and deployment.
- Python: rich ecosystem, but weaker concurrency and packaging for an edge service.
- Node-RED or other low-code tools: fast to prototype, but hard to test, version and audit.

## Consequences

Positive:

- One binary, low memory, easy systemd or container deployment.
- `log/slog`, `net/http` and `database/sql` in the standard library reduce dependencies.

Negative:

- The OPC UA ecosystem is less mature than .NET's (certificate workflows, browsing).
- There is no mature Go Delta Lake writer, which shapes ADR-019.
