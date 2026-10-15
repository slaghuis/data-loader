---
id: ADR-009
title: "Hot reload by fingerprint polling plus SIGHUP"
status: Accepted
date: 2026-09-16
tags: [configuration, scheduling, reload]
---
# ADR-009: Hot reload by fingerprint polling plus SIGHUP

## Status

Accepted

## Date

2026-09-16 (recorded 2026-10-14)

## Context

Metadata changes must take effect without a restart. SQL Server has no simple portable change-notification mechanism that the loader could rely on.

## Decision

Every `LOADER_RELOAD_INTERVAL_SEC` (60 s), and on SIGHUP, hash the scheduling-relevant metadata (SHA-256). If the hash changed, reconcile the cron entries (add, remove, reschedule). In Phase 3, API mutations call `Reload` directly.

## Alternatives Considered

- SQL Server Query Notifications or Service Broker (complex, and the driver support is thin).
- Restart on change (drops in-flight runs).

## Consequences

Positive:

- Simple and cheap. No-op when nothing changed. Works for direct SQL edits, seeding and the API alike.

Negative:

- Up to 60 s of latency for out-of-band edits.
- Changes to non-scheduling fields on an existing load are picked up from the cached load only after a reload.
