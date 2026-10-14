---
id: ADR-012
title: "Versioned migrations with goose"
status: Proposed
date: 2026-10-14
supersedes: ADR-011
tags: [storage, migrations]
---
# ADR-012: Versioned migrations with goose

## Status

Proposed

## Date

2026-10-14

## Context

Phases 2–4 add constraints, audit tables, `rowversion`, and sink and target tables, with data migrations (for example creating the `local` sink row). Rollback requires expand/contract migrations (see [02 – Deployment](../docs/02-architecture.md#deployment-requirements)).

## Decision

Adopt `github.com/pressly/goose/v3` with SQL migrations embedded through `embed.FS` in `migrations/`, and expose `loader migrate up|down|status`. Baseline `0001` reproduces the current AutoMigrate schema. Existing databases are detected and stamped as baselined. AutoMigrate is removed from startup.

## Alternatives Considered

- `golang-migrate/migrate`: comparable, but Go-code migrations are less ergonomic.
- Atlas (declarative): powerful diffing, but more tooling and a learning curve.
- Keep AutoMigrate: rejected (see ADR-011 consequences).

## Consequences

Positive:

- Reviewable, reversible, testable migrations (integration tests run up and down).

Negative:

- A one-time baseline effort. Developers must write SQL for schema changes.
