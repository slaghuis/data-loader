---
id: ADR-004
title: "Store configuration as metadata in a database"
status: Accepted
date: 2026-09-16
tags: [configuration, metadata, storage]
---
# ADR-004: Store configuration as metadata in a database

## Status

Accepted

## Date

2026-09-16 (recorded 2026-10-14)

## Context

New feeds must be added without code changes (BR-002). The brief also anticipates that metadata will be managed from the cloud later on.

## Decision

Sources, loads, tags and nodes are rows in `meta_*` tables. Module-specific connection settings go in `params_json` (a JSON string map) instead of a column per setting. `cmd/seed` provides YAML-as-code on top.

## Alternatives Considered

- YAML or JSON config files only: simple and diff-able, but they cannot be changed remotely or transactionally, and have no audit trail.
- A column per setting: type-safe, but every new module would need a schema migration.

## Consequences

Positive:

- Runtime changes with no redeploy. A natural basis for the Phase 3 API. Run and log data sits beside config for joins.

Negative:

- `params_json` is untyped. Validation happens at run time until Phase 3 adds per-kind JSON Schema validation.
