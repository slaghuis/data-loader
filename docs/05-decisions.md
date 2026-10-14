# Architectural Decisions

| Item | Value |
|------|-------|
| Document | 05 – Architectural Decision Records |
| Last updated | 2026-10-14 |
| Related | [01 Requirements](01-project-requirements.md) · [02 Architecture](02-architecture.md) · [03 Implementation Plan](03-implementation-plan.md) · [04 AI Rules](04-ai-rules.md) |

The Phase 1 decisions were made while the loader was being built (from 2026-09-16) and are recorded here retroactively (2026-10-14). The Phase 2–4 decisions are **Proposed** and need review before implementation (see [04 AI Rules](04-ai-rules.md#workflow-for-ai-agents)).

## Index

| ADR | Title | Status | Phase |
|-----|-------|--------|:-----:|
| [001](#adr-001) | Use Clean Architecture (ports and adapters) | Accepted | 1 |
| [002](#adr-002) | Implement in Go | Accepted | 1 |
| [003](#adr-003) | Use SQL Server for the metadata repository and local sink | Accepted | 1 |
| [004](#adr-004) | Store configuration as metadata in a database | Accepted | 1 |
| [005](#adr-005) | Self-registering source modules | Accepted | 1 |
| [006](#adr-006) | Watermark model: string values, separate table, first-run initialisation | Accepted | 1 |
| [007](#adr-007) | Telemetry sources: forced append and a narrow schema | Accepted | 1 |
| [008](#adr-008) | Structured logging with slog, mirrored asynchronously to the database | Accepted | 1 |
| [009](#adr-009) | Hot reload by fingerprint polling plus SIGHUP | Accepted | 1 |
| [010](#adr-010) | Secrets as `$env:` references | Accepted | 1 |
| [011](#adr-011) | Metadata schema through GORM AutoMigrate | Accepted (to be superseded by 012) | 1 |
| [012](#adr-012) | Versioned migrations with goose | Proposed | 2 |
| [013](#adr-013) | Management API: REST/JSON, OpenAPI-first, standard-library router, embedded in the loader | Proposed | 3 |
| [014](#adr-014) | API authentication: Entra ID JWT with scopes, optional mTLS | Proposed | 3 |
| [015](#adr-015) | Connectivity model between the configuration app and the edge loader | Proposed — **decision required** | 3 |
| [016](#adr-016) | Secrets for remotely created sources | Proposed | 3 |
| [017](#adr-017) | Schema discovery and preview as optional source capabilities | Proposed | 3 |
| [018](#adr-018) | Multi-sink delivery: direct fan-out plus store-and-forward | Proposed | 4 |
| [019](#adr-019) | Microsoft Fabric through an Open Mirroring landing zone | Proposed — spike required | 4 |
| 020 | Atomic `replace` through a staging table and swap (D-05) | To be written | 2 |
| 021 | Source connection reuse across runs (D-17) | To be written | 2 |

---

## ADR-001

### Title

Use Clean Architecture (ports and adapters)

### Status

Accepted

### Date

2026-09-16 (recorded 2026-10-14)

### Context

The loader integrates with four source protocols today, and more are expected. Phase 4 adds more sinks. The system will evolve over time and integrate with multiple infrastructure providers.

### Decision

Use Clean Architecture. The domain contracts (`pkg/contracts`: `Source`, `Sink`, `Batch`, `Watermark`) are independent of drivers. Adapters (`internal/sources/*`, `internal/sink`) implement them, and the application layer (`pipeline`, `scheduler`) orchestrates through them.

### Alternatives Considered

- One package per source with its own end-to-end pipeline (duplicated scheduling, watermark and audit logic).
- A generic ETL framework or a third-party connector platform (heavy for an edge appliance, and no Modbus/OPC UA focus).

### Consequences

Positive:

- Testable: engines can be tested with fake sources and sinks.
- Maintainable: a new protocol is a new package.
- Technology independent: the Phase 4 sinks need no change to sources.

Negative:

- More boilerplate.
- Phase 1 conforms only partially: the application layer depends on the concrete `*metadata.Repository`, and the telemetry adapters import `internal/metadata`. This is fixed in Phase 2 (03 §2.3).

---

## ADR-002

### Title

Implement in Go

### Status

Accepted

### Date

2026-09-16 (recorded 2026-10-14)

### Context

The loader runs unattended on mine-site servers. It needs a small footprint, simple deployment, strong concurrency for many schedules, and mature drivers for SQL Server, PostgreSQL, Modbus and OPC UA.

### Decision

Go (currently 1.26.9). It produces a single static binary, and the drivers used are `go-mssqldb`, `pgx`, `simonvetter/modbus` and `gopcua`.

### Alternatives Considered

- .NET: excellent SQL Server and OPC UA (OPC Foundation stack) support, but a heavier runtime and deployment.
- Python: rich ecosystem, but weaker concurrency and packaging for an edge service.
- Node-RED or other low-code tools: fast to prototype, but hard to test, version and audit.

### Consequences

Positive:

- One binary, low memory, easy systemd or container deployment.
- `log/slog`, `net/http` and `database/sql` in the standard library reduce dependencies.

Negative:

- The OPC UA ecosystem is less mature than .NET's (certificate workflows, browsing).
- There is no mature Go Delta Lake writer, which shapes ADR-019.

---

## ADR-003

### Title

Use SQL Server for the metadata repository and local sink

### Status

Accepted

### Date

2026-09-16 (recorded 2026-10-14)

### Context

The design brief requires the data to land in a local Microsoft SQL Server database. The metadata needs transactional consistency (watermarks, runs). Site DBAs already operate SQL Server.

### Decision

Use SQL Server for both the metadata database (`LOADER_METADATA_DSN`, GORM) and the sink (`LOADER_SINK_DSN`, `database/sql` bulk copy). They are separate databases, possibly on the same instance.

### Alternatives Considered

- PostgreSQL for metadata: mature and free, but adds a second database technology for site DBAs to operate.
- SQLite for metadata: zero operations overhead, but not shareable with the future configuration tooling and has a weaker concurrent-write story.

### Consequences

Positive:

- One database technology on site. Mature and reliable. Bulk copy gives fast sink writes.

Negative:

- The metadata SQL is T-SQL specific (`DELETE TOP`, `nvarchar(max)`, bracket quoting), so portability is low.
- Operational overhead (licensing, backup), which the site already carries.

---

## ADR-004

### Title

Store configuration as metadata in a database

### Status

Accepted

### Date

2026-09-16 (recorded 2026-10-14)

### Context

New feeds must be added without code changes (BR-002). The brief also anticipates that metadata will be managed from the cloud later on.

### Decision

Sources, loads, tags and nodes are rows in `meta_*` tables. Module-specific connection settings go in `params_json` (a JSON string map) instead of a column per setting. `cmd/seed` provides YAML-as-code on top.

### Alternatives Considered

- YAML or JSON config files only: simple and diff-able, but they cannot be changed remotely or transactionally, and have no audit trail.
- A column per setting: type-safe, but every new module would need a schema migration.

### Consequences

Positive:

- Runtime changes with no redeploy. A natural basis for the Phase 3 API. Run and log data sits beside config for joins.

Negative:

- `params_json` is untyped. Validation happens at run time until Phase 3 adds per-kind JSON Schema validation.

---

## ADR-005

### Title

Self-registering source modules

### Status

Accepted

### Date

2026-09-16 (recorded 2026-10-14)

### Context

Source modules must be pluggable ("there might be more modules in the future").

### Decision

Each source package calls `sources.Register(kind, factory)` in `init()`. `cmd/loader` includes it with a blank or named import. The runner calls `sources.Build(cfg)` by kind. Phase 4 applies the same pattern to sinks.

### Alternatives Considered

- A `switch kind` in the runner (central coupling).
- Go `plugin` packages or separate processes (fragile builds, and a gRPC plugin protocol is overkill).

### Consequences

Positive:

- Adding a module touches only its own package and one import line.

Negative:

- `init()` registration is implicit. A forgotten import shows up only at run time as `no source registered for kind`. Phase 2 adds a startup check that every kind present in metadata is registered.

---

## ADR-006

### Title

Watermark model: string values, separate table, first-run initialisation

### Status

Accepted

### Date

2026-09-16 (recorded 2026-10-14)

### Context

Incremental loads need a durable high-water mark per load. Watermark columns may be datetime or numeric. Large historical tables must not be back-loaded by accident (BR-003).

### Decision

- Store watermarks in `meta_watermarks` (one row per load) as **strings**, interpreted through `meta_loads.watermark_type`, and keep `previous_value`, `reset_at` and `reset_reason` for audit.
- **First run** stores the current `MAX(column)` and loads no rows.
- SQL Server snapshots the upper bound before reading (`> last AND <= upper`) so rows inserted during the read are picked up next time.
- The watermark advances only after a successful run.

### Alternatives Considered

- A watermark column on `meta_loads` (mixes user-owned configuration with loader-owned state, and has no audit trail).
- Typed columns per watermark type (schema churn).
- Back-load on first run (risky for multi-million-row tables).

### Consequences

Positive:

- Portable across types. Auditable. Safe default for new loads.

Negative:

- String formatting must be lossless. D-03 shows the SQL Server numeric path currently is not. Phase 2 stores the database's own textual representation.
- Watermarks miss rows whose watermark value is lower than one already loaded (late-arriving commits). Delivery is at-least-once, with possible duplicates after a crash.
- PostgreSQL does not use the upper-bound snapshot yet (D-18).

---

## ADR-007

### Title

Telemetry sources: forced append and a narrow schema

### Status

Accepted

### Date

2026-09-16 (recorded 2026-10-14)

### Context

Modbus and OPC UA produce point-in-time readings, not tables. Tag lists change often. Device faults must remain visible.

### Decision

Telemetry loads are always `append` with no watermark. Each poll writes one row per tag into a fixed narrow schema (`tag_name`, `ts`, `value_num`, `value_bool`, `value_text`, `quality`, plus `correlation_id` for Modbus or `node_id` for OPC UA). Failed reads become `quality = bad` rows with the error in `value_text`. Numerics are widened to float64.

### Alternatives Considered

- A wide table (one column per tag): schema churn on every tag change and very sparse rows.
- Dropping bad reads: you can no longer tell "offline" from "zero".

### Consequences

Positive:

- Tag changes need no DDL. Diagnostics are preserved. Modbus and OPC UA share one downstream model.

Negative:

- 64-bit integers above 2^53 lose precision (rare for sensors).
- Downstream pivoting is needed for wide analytics.

---

## ADR-008

### Title

Structured logging with slog, mirrored asynchronously to the database

### Status

Accepted

### Date

2026-09-16 (recorded 2026-10-14)

### Context

Connection attempts, loads, watermark changes and errors must be recorded in the metadata database (BR-004), and logging must never stall a load.

### Decision

Use `log/slog` only. `logging.DBHandler` wraps the console handler and enqueues entries to a bounded channel (512 entries) drained by one worker. When the channel is full, entries are dropped from the database copy only. The keys `run_uuid`, `load_id`, `source_id` and `category` are promoted to columns; other attributes go to `details` as JSON.

### Alternatives Considered

- Synchronous DB writes (a slow metadata database would stall loads).
- zap or zerolog (extra dependency with no real benefit since slog).

### Consequences

Positive:

- The console always gets every record. Queryable logs. No third-party logging library.

Negative:

- DB logs can be lost under pressure. Phase 2 adds a dropped-entries counter.
- The implementation currently loses `With` attributes (D-01) and panics after `Close` (D-02). Both are Phase 2 fixes.

---

## ADR-009

### Title

Hot reload by fingerprint polling plus SIGHUP

### Status

Accepted

### Date

2026-09-16 (recorded 2026-10-14)

### Context

Metadata changes must take effect without a restart. SQL Server has no simple portable change-notification mechanism that the loader could rely on.

### Decision

Every `LOADER_RELOAD_INTERVAL_SEC` (60 s), and on SIGHUP, hash the scheduling-relevant metadata (SHA-256). If the hash changed, reconcile the cron entries (add, remove, reschedule). In Phase 3, API mutations call `Reload` directly.

### Alternatives Considered

- SQL Server Query Notifications or Service Broker (complex, and the driver support is thin).
- Restart on change (drops in-flight runs).

### Consequences

Positive:

- Simple and cheap. No-op when nothing changed. Works for direct SQL edits, seeding and the API alike.

Negative:

- Up to 60 s of latency for out-of-band edits.
- Changes to non-scheduling fields on an existing load are picked up from the cached load only after a reload.

---

## ADR-010

### Title

Secrets as `$env:` references

### Status

Accepted. Extended by ADR-016 in Phase 3.

### Date

2026-09-16 (recorded 2026-10-14)

### Context

Source credentials must not be stored in plain text in `params_json`.

### Decision

Any param value `$env:NAME` is resolved from the process environment at run time (systemd `EnvironmentFile`, or a Kubernetes Secret). A missing variable fails the run.

### Alternatives Considered

- Encrypted columns (key management needed from day one).
- An external vault (a WAN dependency at the edge).

### Consequences

Positive:

- Zero infrastructure. Works with systemd and Kubernetes secrets.

Negative:

- Adding a source with new credentials requires host access and a restart. This is incompatible with remote configuration, which is why ADR-016 exists.

---

## ADR-011

### Title

Metadata schema through GORM AutoMigrate

### Status

Accepted. To be superseded by ADR-012 once that is accepted.

### Date

2026-09-16 (recorded 2026-10-14)

### Context

The schema changed rapidly during Phase 1.

### Decision

`metadata.AutoMigrate` runs on every startup and creates or alters the `meta_*` tables from the GORM models.

### Consequences

Positive:

- Zero friction while iterating.

Negative:

- No down migrations, no review of the generated DDL, no data migrations.
- Unsafe for Phase 3 and Phase 4 schema changes (renames, back-fills, `rowversion`).

---

## ADR-012

### Title

Versioned migrations with goose

### Status

Proposed

### Date

2026-10-14

### Context

Phases 2–4 add constraints, audit tables, `rowversion`, and sink and target tables, with data migrations (for example creating the `local` sink row). Rollback requires expand/contract migrations (see [02 – Deployment](02-architecture.md#deployment-requirements)).

### Decision

Adopt `github.com/pressly/goose/v3` with SQL migrations embedded through `embed.FS` in `migrations/`, and expose `loader migrate up|down|status`. Baseline `0001` reproduces the current AutoMigrate schema. Existing databases are detected and stamped as baselined. AutoMigrate is removed from startup.

### Alternatives Considered

- `golang-migrate/migrate`: comparable, but Go-code migrations are less ergonomic.
- Atlas (declarative): powerful diffing, but more tooling and a learning curve.
- Keep AutoMigrate: rejected (see ADR-011 consequences).

### Consequences

Positive:

- Reviewable, reversible, testable migrations (integration tests run up and down).

Negative:

- A one-time baseline effort. Developers must write SQL for schema changes.

---

## ADR-013

### Title

Management API: REST/JSON, OpenAPI-first, standard-library router, embedded in the loader

### Status

Proposed

### Date

2026-10-14

### Context

A separate application will configure sources and loads remotely, browse source schemas, and preview data (BR-006). Discovery and preview need live access to the sources, and only the loader on site has that.

### Decision

- Embed a REST/JSON API (`/api/v1`) in the loader process, served on its own listener (`LOADER_API_ADDR`), separate from the health server.
- Write the `openapi.yaml` contract first. Use `oapi-codegen` to generate types only. Handlers use the Go 1.22+ `net/http` method and path patterns, so each wildcard occupies a whole path segment (for example `/sources/{id}/test`, not `/sources/{id}:test`).
- Use problem+json errors, ETag / `If-Match` concurrency, and cursor pagination (details in [02 – API](02-architecture.md#api-requirements)).
- Mutations go through an application service shared with `cmd/seed`, then call `Scheduler.Reload` synchronously.

### Alternatives Considered

- **The configuration app writes directly to the metadata database.** Rejected: it bypasses validation, couples the app to the schema, and cannot do discovery or preview.
- **gRPC (with grpc-gateway).** Strong typing, but harder for a web configuration app and through corporate proxies. Can be revisited.
- **GraphQL.** Overkill for CRUD plus a few actions.
- **A separate API service** beside the loader. An extra deployable, and it would still need the source drivers for discovery.

### Consequences

Positive:

- One deployable. Discovery reuses the source adapters and type mapping, so a preview equals what a load produces.

Negative:

- The loader's attack surface grows (mitigated by ADR-014 and ADR-015).
- An API bug could affect loading. Mitigation: a separate listener and goroutines, panic recovery, and timeouts.

---

## ADR-014

### Title

API authentication: Entra ID JWT with scopes, optional mTLS

### Status

Proposed

### Date

2026-10-14

### Context

The API can change what data leaves the mine and can expose real business data through preview. Microsoft Entra ID is assumed to be the corporate identity provider.

### Decision

- Bearer access tokens issued by Entra ID (client-credentials grant for the configuration app's backend, or delegated tokens for user-attributed audit).
- Validate the issuer, audience, expiry and signature using a cached JWKS that keeps working through short WAN outages.
- Scopes and app roles: `config:read`, `config:write`, `sources:preview`, `ops:run`, `ops:watermark`. Audit records use the token's `oid` / `appid`.
- Optional mutual TLS (`LOADER_API_CLIENT_CA_FILE`) as defence in depth on site networks.
- `apikey` mode exists only for development and refuses to start unless bound to loopback.

### Alternatives Considered

- Static API keys in production (no rotation story, no identity in the audit trail).
- mTLS only (no per-user authorisation).
- Basic auth (rejected).

### Consequences

Positive:

- Central identity, per-scope least privilege, attributable audit.

Negative:

- Needs outbound HTTPS to Entra ID for key refresh. Initial setup requires an app registration.

---

## ADR-015

### Title

Connectivity model between the configuration app and the edge loader

### Status

Proposed — **decision required from site OT security and the platform team**

### Date

2026-10-14

### Context

The loader sits on, or next to, the mine's OT network. Industrial security practice (IEC 62443 zones and conduits, the Purdue model) generally forbids inbound connections from enterprise or cloud networks into OT zones. The Phase 3 brief assumes the configuration app *calls* the loader's API. The original README instead envisaged the loader *pulling* metadata from the cloud. These two models conflict.

### Decision

Recommended, pending approval:

1. Build the API exactly as in ADR-013. The contract is the same in every option below.
2. Per site, deploy using one of:
   - **A — DMZ exposure.** The loader (or a reverse proxy) is in the site DMZ or Level 3.5, and the configuration app reaches it over approved HTTPS. This is the simplest option where OT security permits it.
   - **B — Outbound-only relay.** The loader opens an outbound connection (for example Azure Relay Hybrid Connections or an equivalent reverse tunnel), and the configuration app calls the API through it. No inbound firewall rules.
3. A pull model (the loader polls the cloud for its desired configuration) is **not** chosen for Phase 3, because discovery and preview are interactive request/response operations. It remains a backlog option for the configuration CRUD part.

### Alternatives Considered

- Inbound access straight into the OT network: rejected on security grounds.
- Pull-only desired state: cannot support interactive discovery or preview.

### Consequences

Positive:

- Development is unblocked. Per-site flexibility. Preserves OT segmentation.

Negative:

- Option B adds a cloud dependency (the relay) and an extra component to operate.
- This decision must be made before the first production deployment of Phase 3.

---

## ADR-016

### Title

Secrets for remotely created sources

### Status

Proposed

### Date

2026-10-14

### Context

Under ADR-010, a new credential requires host access. Phase 3 lets a remote app create sources, so credentials must be settable through the API without being stored in plain text or ever read back.

### Decision

- Add a `$secret:NAME` reference type, backed by `meta_secrets`. Values are encrypted with AES-256-GCM, with a per-row nonce and a `key_id` to support rotation. The key comes from `LOADER_SECRETS_KEY_FILE` (file permission 0400), held outside the database.
- The API accepts secrets in a write-only `secrets` object, stores them as `$secret:<source-name>.<field>`, and returns only `{ "set": true }`.
- `$env:` remains supported. Optionally, a `$kv:` resolver (Azure Key Vault) with a local encrypted cache can be added for sites that standardise on Key Vault.
- Key rotation is done with `loader secrets rotate`.

### Alternatives Considered

- Azure Key Vault only: a WAN dependency at run time; edge loads would fail during outages.
- HashiCorp Vault: operational overhead per site.
- SQL Server Always Encrypted: ties secret handling to driver and column-master-key setup on each site.

### Consequences

Positive:

- Remote configuration works offline. Secrets are never in plain text at rest or in responses.

Negative:

- The loader owns key management. Loss of the key file means re-entering all `$secret:` credentials (document it in the runbook).

---

## ADR-017

### Title

Schema discovery and preview as optional source capabilities

### Status

Proposed

### Date

2026-10-14

### Context

The configuration app needs object lists, column schemas, watermark suggestions and a top-10 data preview (FR-022, FR-023). Not every source kind supports all of these (Modbus has no catalogue).

### Decision

- Add the optional interfaces `contracts.SchemaDiscoverer`, `contracts.Previewer` and `contracts.ConnectionTester`, detected with type assertions. The API returns `501 Not Implemented` for kinds that lack a capability.
- Preview is limited to `MaxPreviewRows = 10` **in the domain constant and again in each adapter**. It runs read-only (`ApplicationIntent=ReadOnly` / `BEGIN READ ONLY`) with a statement timeout, truncates large values at 4 KiB, uses the same canonical normalisation as loads, is rate-limited per client, and is audited.
- Watermark candidates are ranked heuristically: indexed non-null datetime columns first, then identity or increasing numeric keys, with the caveat that key-based watermarks only see inserts.

### Alternatives Considered

- Add methods to `Source` (forces stubs in every module).
- Let the configuration app query sources directly (no network path, and it would duplicate type mapping).

### Consequences

Positive:

- Modules opt in independently. Previews match real load output.

Negative:

- Preview exposes real data. This is mitigated by scope, audit and the row cap. Masking is out of scope for Phase 3 (assumption 7 in 01).

---

## ADR-018

### Title

Multi-sink delivery: direct fan-out plus store-and-forward

### Status

Proposed

### Date

2026-10-14

### Context

Phase 4 delivers loads to several sinks, including Microsoft Fabric over an unreliable WAN. Requirements: read sources once (BR-003), local loads unaffected by cloud outages (BR-005, FR-033), and no data loss.

### Decision

- Introduce a sink registry (`meta_sinks`) and per-load targets (`meta_load_targets`). Each sink is either **direct** or **forward**.
- **Direct** targets (LAN, for example the local MSSQL sink) receive batches inline from the runner. The **primary** target, `local`, determines run success.
- **Forward** targets (WAN, for example Fabric) are fed by a separate **forwarder** that reads completed runs' rows from the local sink table by `yb_load_timestamp`, in order, and delivers them. A per-target cursor (`meta_target_cursors`) advances only after a confirmed upload. Delivery is at-least-once, and remote sinks de-duplicate by key where supported.
- To avoid forwarding a partially written run, the run's load timestamp is stored in `meta_load_runs`, and the forwarder only forwards timestamps of runs with status `success`.
- `replace` loads on forward targets are sent as a full snapshot generation.

### Alternatives Considered

- **Synchronous fan-out to all targets.** A WAN outage would fail or stall local loads.
- **Per-target watermarks with separate source reads.** Multiplies load on production systems (violates BR-003).
- **An edge message broker** (Kafka/MQTT) between the loader and sinks. Robust, but a heavy operational burden per site.

### Consequences

Positive:

- Local SQL Server becomes the durable buffer at no extra cost. Cloud outages only create backlog. Sources are read once.

Negative:

- Forward targets require the local sink (it cannot be removed).
- `yb_load_timestamp` must be indexed in sink tables.
- Additional latency for remote targets (forwarder interval).

---

## ADR-019

### Title

Microsoft Fabric through an Open Mirroring landing zone

### Status

Proposed — validate with the Phase 4 spike (03 §4.0). Product capabilities, limits and licensing must be checked against current Microsoft documentation at the time of the spike.

### Date

2026-10-14

### Context

Enterprise analytics will run in Microsoft Fabric (BR-007). The loader is written in Go, runs at the edge, and connects through an intermittent WAN. Fabric stores data in OneLake as Delta Lake tables.

### Decision

Recommended: the Fabric sink writes **Parquet files** plus `_metadata.json` (key columns) into a **mirrored database's Open Mirroring landing zone** in OneLake. It uses the ADLS Gen2–compatible OneLake endpoint (Azure SDK for Go) with a service principal or managed identity. Fabric's replication then maintains the Delta tables, which are queryable through the SQL analytics endpoint and Power BI. A row marker column carries insert or upsert semantics for delta loads with keys.

### Alternatives Considered

- **Fabric Data Warehouse over TDS** (reuse `go-mssqldb` with Entra ID authentication). The least new code, but Microsoft discourages small or singleton inserts in favour of bulk ingestion. Its type system differs (for example, no `nvarchar`, limited `datetime2` precision), and bulk-load support over TDS must be verified. This is the fallback if Open Mirroring fails the spike.
- **Write Delta tables directly to a Lakehouse.** There is no mature Go Delta writer, and implementing the transaction log ourselves is risky.
- **Eventstream (Event Hubs–compatible endpoint).** A strong fit for high-frequency telemetry (Modbus/OPC UA) but not for table loads. It may be adopted **in addition** for telemetry targets.
- **A Fabric pipeline or copy job pulling from the site SQL Server** through an on-premises data gateway. No loader code, but it needs inbound or gateway infrastructure per site and moves scheduling out of the loader.

### Consequences

Positive:

- Pure file uploads are resumable and WAN-friendly, and pair naturally with ADR-018 store-and-forward.
- Fabric owns the Delta maintenance.

Negative:

- Depends on the evolution of the Open Mirroring feature and its limits.
- Parquet encoding has to be implemented.
- The `replace` semantics need a defined convention.
- Mirroring latency (seconds to minutes) is acceptable for analytics but not for operational dashboards.
