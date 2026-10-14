---
id: ADR-007
title: "Telemetry sources: forced append and a narrow schema"
status: Accepted
date: 2026-09-16
tags: [telemetry, modbus, opcua, schema]
---
# ADR-007: Telemetry sources: forced append and a narrow schema

## Status

Accepted

## Date

2026-09-16 (recorded 2026-10-14)

## Context

Modbus and OPC UA produce point-in-time readings, not tables. Tag lists change often. Device faults must remain visible.

## Decision

Telemetry loads are always `append` with no watermark. Each poll writes one row per tag into a fixed narrow schema (`tag_name`, `ts`, `value_num`, `value_bool`, `value_text`, `quality`, plus `correlation_id` for Modbus or `node_id` for OPC UA). Failed reads become `quality = bad` rows with the error in `value_text`. Numerics are widened to float64.

## Alternatives Considered

- A wide table (one column per tag): schema churn on every tag change and very sparse rows.
- Dropping bad reads: you can no longer tell "offline" from "zero".

## Consequences

Positive:

- Tag changes need no DDL. Diagnostics are preserved. Modbus and OPC UA share one downstream model.

Negative:

- 64-bit integers above 2^53 lose precision (rare for sensors).
- Downstream pivoting is needed for wide analytics.
