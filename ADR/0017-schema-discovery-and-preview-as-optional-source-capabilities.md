---
id: ADR-017
title: "Schema discovery and preview as optional source capabilities"
status: Proposed
date: 2026-10-14
tags: [api, sources, discovery]
---
# ADR-017: Schema discovery and preview as optional source capabilities

## Status

Proposed

## Date

2026-10-14

## Context

The configuration app needs object lists, column schemas, watermark suggestions and a top-10 data preview (FR-022, FR-023). Not every source kind supports all of these (Modbus has no catalogue).

## Decision

- Add the optional interfaces `contracts.SchemaDiscoverer`, `contracts.Previewer` and `contracts.ConnectionTester`, detected with type assertions. The API returns `501 Not Implemented` for kinds that lack a capability.
- Preview is limited to `MaxPreviewRows = 10` **in the domain constant and again in each adapter**. It runs read-only (`ApplicationIntent=ReadOnly` / `BEGIN READ ONLY`) with a statement timeout, truncates large values at 4 KiB, uses the same canonical normalisation as loads, is rate-limited per client, and is audited.
- Watermark candidates are ranked heuristically: indexed non-null datetime columns first, then identity or increasing numeric keys, with the caveat that key-based watermarks only see inserts.

## Alternatives Considered

- Add methods to `Source` (forces stubs in every module).
- Let the configuration app query sources directly (no network path, and it would duplicate type mapping).

## Consequences

Positive:

- Modules opt in independently. Previews match real load output.

Negative:

- Preview exposes real data. This is mitigated by scope, audit and the row cap. Masking is out of scope for Phase 3 (assumption 7 in 01).
