---
id: ADR-003
title: "Use SQL Server for the metadata repository and local sink"
status: Accepted
date: 2026-09-16
tags: [storage, database, sql-server]
---
# ADR-003: Use SQL Server for the metadata repository and local sink

## Status

Accepted

## Date

2026-09-16 (recorded 2026-10-14)

## Context

The design brief requires the data to land in a local Microsoft SQL Server database. The metadata needs transactional consistency (watermarks, runs). Site DBAs already operate SQL Server.

## Decision

Use SQL Server for both the metadata database (`LOADER_METADATA_DSN`, GORM) and the sink (`LOADER_SINK_DSN`, `database/sql` bulk copy). They are separate databases, possibly on the same instance.

## Alternatives Considered

- PostgreSQL for metadata: mature and free, but adds a second database technology for site DBAs to operate.
- SQLite for metadata: zero operations overhead, but not shareable with the future configuration tooling and has a weaker concurrent-write story.

## Consequences

Positive:

- One database technology on site. Mature and reliable. Bulk copy gives fast sink writes.

Negative:

- The metadata SQL is T-SQL specific (`DELETE TOP`, `nvarchar(max)`, bracket quoting), so portability is low.
- Operational overhead (licensing, backup), which the site already carries.
