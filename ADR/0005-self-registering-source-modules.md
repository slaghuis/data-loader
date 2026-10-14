---
id: ADR-005
title: "Self-registering source modules"
status: Accepted
date: 2026-09-16
tags: [architecture, plugins, sources]
---
# ADR-005: Self-registering source modules

## Status

Accepted

## Date

2026-09-16 (recorded 2026-10-14)

## Context

Source modules must be pluggable ("there might be more modules in the future").

## Decision

Each source package calls `sources.Register(kind, factory)` in `init()`. `cmd/loader` includes it with a blank or named import. The runner calls `sources.Build(cfg)` by kind. Phase 4 applies the same pattern to sinks.

## Alternatives Considered

- A `switch kind` in the runner (central coupling).
- Go `plugin` packages or separate processes (fragile builds, and a gRPC plugin protocol is overkill).

## Consequences

Positive:

- Adding a module touches only its own package and one import line.

Negative:

- `init()` registration is implicit. A forgotten import shows up only at run time as `no source registered for kind`. Phase 2 adds a startup check that every kind present in metadata is registered.
