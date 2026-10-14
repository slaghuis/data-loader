---
id: ADR-010
title: "Secrets as `$env:` references"
status: Accepted
date: 2026-09-16
tags: [secrets, security]
---
# ADR-010: Secrets as `$env:` references

## Status

Accepted. Extended by ADR-016 in Phase 3.

## Date

2026-09-16 (recorded 2026-10-14)

## Context

Source credentials must not be stored in plain text in `params_json`.

## Decision

Any param value `$env:NAME` is resolved from the process environment at run time (systemd `EnvironmentFile`, or a Kubernetes Secret). A missing variable fails the run.

## Alternatives Considered

- Encrypted columns (key management needed from day one).
- An external vault (a WAN dependency at the edge).

## Consequences

Positive:

- Zero infrastructure. Works with systemd and Kubernetes secrets.

Negative:

- Adding a source with new credentials requires host access and a restart. This is incompatible with remote configuration, which is why ADR-016 exists.
