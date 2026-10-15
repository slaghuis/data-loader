---
id: ADR-019
title: "Microsoft Fabric through an Open Mirroring landing zone"
status: Proposed
date: 2026-10-14
tags: [sinks, fabric, cloud, azure]
---
# ADR-019: Microsoft Fabric through an Open Mirroring landing zone

## Status

Proposed — validate with the Phase 4 spike (03 §4.0). Product capabilities, limits and licensing must be checked against current Microsoft documentation at the time of the spike.

## Date

2026-10-14

## Context

Enterprise analytics will run in Microsoft Fabric (BR-007). The loader is written in Go, runs at the edge, and connects through an intermittent WAN. Fabric stores data in OneLake as Delta Lake tables.

## Decision

Recommended: the Fabric sink writes **Parquet files** plus `_metadata.json` (key columns) into a **mirrored database's Open Mirroring landing zone** in OneLake. It uses the ADLS Gen2–compatible OneLake endpoint (Azure SDK for Go) with a service principal or managed identity. Fabric's replication then maintains the Delta tables, which are queryable through the SQL analytics endpoint and Power BI. A row marker column carries insert or upsert semantics for delta loads with keys.

## Alternatives Considered

- **Fabric Data Warehouse over TDS** (reuse `go-mssqldb` with Entra ID authentication). The least new code, but Microsoft discourages small or singleton inserts in favour of bulk ingestion. Its type system differs (for example, no `nvarchar`, limited `datetime2` precision), and bulk-load support over TDS must be verified. This is the fallback if Open Mirroring fails the spike.
- **Write Delta tables directly to a Lakehouse.** There is no mature Go Delta writer, and implementing the transaction log ourselves is risky.
- **Eventstream (Event Hubs–compatible endpoint).** A strong fit for high-frequency telemetry (Modbus/OPC UA) but not for table loads. It may be adopted **in addition** for telemetry targets.
- **A Fabric pipeline or copy job pulling from the site SQL Server** through an on-premises data gateway. No loader code, but it needs inbound or gateway infrastructure per site and moves scheduling out of the loader.

## Consequences

Positive:

- Pure file uploads are resumable and WAN-friendly, and pair naturally with ADR-018 store-and-forward.
- Fabric owns the Delta maintenance.

Negative:

- Depends on the evolution of the Open Mirroring feature and its limits.
- Parquet encoding has to be implemented.
- The `replace` semantics need a defined convention.
- Mirroring latency (seconds to minutes) is acceptable for analytics but not for operational dashboards.
