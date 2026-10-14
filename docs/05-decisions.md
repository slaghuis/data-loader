# Architectural Decisions

| Item | Value |
|------|-------|
| Document | 05 – Architectural Decision Records (index) |
| Last updated | 2026-10-14 |
| Related | [01 Requirements](01-project-requirements.md) · [02 Architecture](02-architecture.md) · [03 Implementation Plan](03-implementation-plan.md) · [04 AI Rules](04-ai-rules.md) |

Each decision lives in its own file under [`ADR/`](../ADR/) so the design indexer and the ADR server (`find_decision`, `list_adrs`, `get_adr`) can index it individually. This page is only the index.

The Phase 1 decisions were made while the loader was being built (from 2026-09-16) and are recorded retroactively (2026-10-14). The Phase 2–4 decisions are **Proposed** and need review before implementation (see [04 AI Rules](04-ai-rules.md#workflow-for-ai-agents)).

## Index

| ADR | Title | Status | Phase |
|-----|-------|--------|:-----:|
| [ADR-001](../ADR/0001-use-clean-architecture-ports-and-adapters.md) | Use Clean Architecture (ports and adapters) | Accepted | 1 |
| [ADR-002](../ADR/0002-implement-in-go.md) | Implement in Go | Accepted | 1 |
| [ADR-003](../ADR/0003-use-sql-server-for-the-metadata-repository-and-local-sink.md) | Use SQL Server for the metadata repository and local sink | Accepted | 1 |
| [ADR-004](../ADR/0004-store-configuration-as-metadata-in-a-database.md) | Store configuration as metadata in a database | Accepted | 1 |
| [ADR-005](../ADR/0005-self-registering-source-modules.md) | Self-registering source modules | Accepted | 1 |
| [ADR-006](../ADR/0006-watermark-model-string-values-separate-table-first-run-initi.md) | Watermark model: string values, separate table, first-run initialisation | Accepted | 1 |
| [ADR-007](../ADR/0007-telemetry-sources-forced-append-and-a-narrow-schema.md) | Telemetry sources: forced append and a narrow schema | Accepted | 1 |
| [ADR-008](../ADR/0008-structured-logging-with-slog-mirrored-asynchronously-to-the.md) | Structured logging with slog, mirrored asynchronously to the database | Accepted | 1 |
| [ADR-009](../ADR/0009-hot-reload-by-fingerprint-polling-plus-sighup.md) | Hot reload by fingerprint polling plus SIGHUP | Accepted | 1 |
| [ADR-010](../ADR/0010-secrets-as-env-references.md) | Secrets as `$env:` references | Accepted (extended by ADR-016) | 1 |
| [ADR-011](../ADR/0011-metadata-schema-through-gorm-automigrate.md) | Metadata schema through GORM AutoMigrate | Accepted (to be superseded by ADR-012) | 1 |
| [ADR-012](../ADR/0012-versioned-migrations-with-goose.md) | Versioned migrations with goose | Proposed | 2 |
| [ADR-013](../ADR/0013-management-api-rest-json-openapi-first-standard-library-rout.md) | Management API: REST/JSON, OpenAPI-first, standard-library router, embedded in the loader | Proposed | 3 |
| [ADR-014](../ADR/0014-api-authentication-entra-id-jwt-with-scopes-optional-mtls.md) | API authentication: Entra ID JWT with scopes, optional mTLS | Proposed | 3 |
| [ADR-015](../ADR/0015-connectivity-model-between-the-configuration-app-and-the-edg.md) | Connectivity model between the configuration app and the edge loader | Proposed — **decision required** | 3 |
| [ADR-016](../ADR/0016-secrets-for-remotely-created-sources.md) | Secrets for remotely created sources | Proposed | 3 |
| [ADR-017](../ADR/0017-schema-discovery-and-preview-as-optional-source-capabilities.md) | Schema discovery and preview as optional source capabilities | Proposed | 3 |
| [ADR-018](../ADR/0018-multi-sink-delivery-direct-fan-out-plus-store-and-forward.md) | Multi-sink delivery: direct fan-out plus store-and-forward | Proposed | 4 |
| [ADR-019](../ADR/0019-microsoft-fabric-through-an-open-mirroring-landing-zone.md) | Microsoft Fabric through an Open Mirroring landing zone | Proposed — spike required | 4 |
| ADR-020 | Atomic `replace` through a staging table and swap (D-05) | To be written | 2 |
| ADR-021 | Source connection reuse across runs (D-17) | To be written | 2 |

## Writing a new ADR

1. Copy the most similar file in `ADR/` to `ADR/NNNN-short-slug.md` using the next free number.
2. Keep the YAML front matter at the very top of the file, with LF line endings. The design indexer only recognises front matter that starts with `---\n`. Use canonical status values, because the ADR server filters on exact matches.

   ```yaml
   ---
   id: ADR-020
   title: "Atomic replace through a staging table and swap"
   status: Proposed          # Proposed | Accepted | Rejected | Superseded | Deprecated
   date: 2026-10-20
   supersedes: ADR-0xx       # optional
   superseded_by: ADR-0xx    # optional; set on the old ADR when a new one is accepted
   tags: [sinks, data-loading]
   ---
   ```

3. Body sections: `# ADR-NNN: Title`, then `## Status`, `## Date`, `## Context`, `## Decision`, `## Alternatives Considered`, `## Consequences`.
4. Add a row to the index table above, then re-run the design indexer.
