---
id: ADR-011
title: "Metadata schema through GORM AutoMigrate"
status: Accepted
date: 2026-09-16
tags: [storage, migrations]
---
# ADR-011: Metadata schema through GORM AutoMigrate

## Status

Accepted. To be superseded by ADR-012 once that is accepted.

## Date

2026-09-16 (recorded 2026-10-14)

## Context

The schema changed rapidly during Phase 1.

## Decision

`metadata.AutoMigrate` runs on every startup and creates or alters the `meta_*` tables from the GORM models.

## Consequences

Positive:

- Zero friction while iterating.

Negative:

- No down migrations, no review of the generated DDL, no data migrations.
- Unsafe for Phase 3 and Phase 4 schema changes (renames, back-fills, `rowversion`).
