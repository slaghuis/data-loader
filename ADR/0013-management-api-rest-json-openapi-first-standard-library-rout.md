---
id: ADR-013
title: "Management API: REST/JSON, OpenAPI-first, standard-library router, embedded in the loader"
status: Proposed
date: 2026-10-14
tags: [api, architecture]
---
# ADR-013: Management API: REST/JSON, OpenAPI-first, standard-library router, embedded in the loader

## Status

Proposed

## Date

2026-10-14

## Context

A separate application will configure sources and loads remotely, browse source schemas, and preview data (BR-006). Discovery and preview need live access to the sources, and only the loader on site has that.

## Decision

- Embed a REST/JSON API (`/api/v1`) in the loader process, served on its own listener (`LOADER_API_ADDR`), separate from the health server.
- Write the `openapi.yaml` contract first. Use `oapi-codegen` to generate types only. Handlers use the Go 1.22+ `net/http` method and path patterns, so each wildcard occupies a whole path segment (for example `/sources/{id}/test`, not `/sources/{id}:test`).
- Use problem+json errors, ETag / `If-Match` concurrency, and cursor pagination (details in [02 – API](../docs/02-architecture.md#api-requirements)).
- Mutations go through an application service shared with `cmd/seed`, then call `Scheduler.Reload` synchronously.

## Alternatives Considered

- **The configuration app writes directly to the metadata database.** Rejected: it bypasses validation, couples the app to the schema, and cannot do discovery or preview.
- **gRPC (with grpc-gateway).** Strong typing, but harder for a web configuration app and through corporate proxies. Can be revisited.
- **GraphQL.** Overkill for CRUD plus a few actions.
- **A separate API service** beside the loader. An extra deployable, and it would still need the source drivers for discovery.

## Consequences

Positive:

- One deployable. Discovery reuses the source adapters and type mapping, so a preview equals what a load produces.

Negative:

- The loader's attack surface grows (mitigated by ADR-014 and ADR-015).
- An API bug could affect loading. Mitigation: a separate listener and goroutines, panic recovery, and timeouts.
