# Edge Data Loader

| Item | Value |
|------|-------|
| Document | 01 – Project Requirements |
| Version | 1.0 |
| Last updated | 2026-10-14 |
| Status | Phase 1 complete; Phases 2–4 planned |
| Related | [02 Architecture](02-architecture.md) · [03 Implementation Plan](03-implementation-plan.md) · [04 AI Rules](04-ai-rules.md) · [05 Decisions](05-decisions.md) |

## Objective

Build a modular, metadata-driven data loader in Go that runs on a mine site ("the edge"). On a configurable schedule it extracts data from heterogeneous operational sources (SQL Server, PostgreSQL, Modbus TCP and OPC UA) and loads it into a local Microsoft SQL Server sink. Delta loads use watermarks. Every connection, run, watermark change and error is recorded in a metadata database. Later phases harden the codebase, add a remote configuration API (with schema discovery and data preview), and extend delivery to multiple sinks, including Microsoft Fabric.

---

# Background

## Current Situation

Operational data on the mine is spread across systems that were never designed to talk to each other:

- **Business systems** (ERP, MES) on SQL Server and PostgreSQL databases.
- **PLCs and gateways** (for example crushers and conveyors) that speak Modbus TCP.
- **SCADA / historian servers** (for example mill SCADA) that expose OPC UA.

The loader described in this document already exists and runs (Phase 1). It was built iteratively, before any formal documentation existed. This document set reconstructs the requirements as if the loader were built from scratch, and records what was delivered against them.

## Problem Statement

- There is no single local store where operational and business data can be queried together.
- Ad-hoc extracts put unpredictable load on production databases and PLCs.
- Nobody can see what was loaded, when, or why a load failed.
- Adding a new feed requires a developer and a redeploy.
- The site has intermittent WAN connectivity, so a cloud-only solution cannot be the only path.

## Desired Outcome

- All in-scope operational data lands in one local SQL Server database on predictable schedules, stamped with a load timestamp.
- Engineers add or change feeds by editing metadata, not code.
- Every load is auditable from the metadata database.
- *(Phase 3)* A separate configuration application manages sources and loads remotely, and can browse source schemas and preview data before a load is created.
- *(Phase 4)* The same loads can be delivered to additional sinks, including Microsoft Fabric for enterprise analytics, without re-reading the sources.

---

# Scope

## In Scope

| Phase | Requirement |
|-------|-------------|
| 1 ✅ | Source modules: SQL Server, PostgreSQL, Modbus TCP, OPC UA |
| 1 ✅ | Microsoft SQL Server sink with automatic table creation and a load-timestamp column |
| 1 ✅ | Metadata repository (sources, loads, watermarks, runs, logs, Modbus tags, OPC UA nodes) |
| 1 ✅ | Cron-based scheduling with an overlap guard |
| 1 ✅ | Delta (watermark), append and replace load modes |
| 1 ✅ | Structured logging to the console and the metadata database |
| 1 ✅ | Health, readiness and status HTTP endpoints |
| 1 ✅ | Retention housekeeping for logs and run history |
| 1 ✅ | Hot reload of metadata (periodic and on SIGHUP) |
| 1 ✅ | YAML seeding CLI |
| 2 | Compliance with [04 AI Rules](04-ai-rules.md): tests at ≥ 80% coverage, dependency injection, documentation, defect fixes, versioned migrations, metrics, CI gates |
| 3 | REST management API for sources, loads, Modbus tags and OPC UA nodes |
| 3 | Source schema discovery and a top-10-row data preview |
| 3 | Operational API: connection test, run now, reset watermark, run and log history |
| 4 | Sink registry and fan-out from one load to multiple targets |
| 4 | Microsoft Fabric sink |
| 4 | Per-target delivery state so that one sink failing does not block the others |

## Out of Scope

- **Writing to any source system.** The loader is strictly read-only towards sources. It never writes to PLCs or OT devices (see BR-008).
- Data transformation beyond type normalisation. Business transformation is done downstream (ELT).
- Log-based change data capture (CDC). Watermarks are the incremental mechanism.
- The remote configuration user interface. It is a separate application that consumes the Phase 3 API.
- Automatic sink schema evolution (schema drift). Tables are created once and are not altered.
- Bidirectional cloud metadata sync. This remains backlog; see [03 Implementation Plan – Backlog](03-implementation-plan.md#backlog).
- OPC UA certificate-based authentication and address-space browsing (backlog).

---

# Business Requirements

## BR-001 – Consolidate operational data

Data from business systems, PLCs and SCADA must be available in a single queryable SQL Server database on site.

## BR-002 – Metadata-driven configuration

Adding, changing, disabling or rescheduling a feed must not require a code change or a redeploy. Changes must take effect within 60 seconds (or immediately on request).

## BR-003 – Minimal impact on production systems

Loads must be incremental wherever the source supports it. A new delta load must not back-load the entire history of a large table by accident.

## BR-004 – Full auditability

For every load execution the business must be able to answer: when did it run, how many rows were read and written, which watermark range was covered, and why did it fail.

## BR-005 – Autonomous edge operation

The loader must run unattended on site, recover from transient network and device failures without a restart, and keep running when the WAN is down.

## BR-006 – Remote configuration *(Phase 3)*

A separate application must be able to manage the loader's configuration over an authenticated API. Before creating a load, it must be able to see what tables and columns a source offers and a small sample of the data.

## BR-007 – Enterprise analytics delivery *(Phase 4)*

Loaded data must be deliverable to Microsoft Fabric (in addition to the local sink) for enterprise reporting, without a second extraction from the mine's sources.

## BR-008 – Operational technology safety

The loader must never issue write commands to PLCs, SCADA servers or source databases. Polling rates must be configurable per load so that devices are not overloaded.

---

# Functional Requirements

Status key: ✅ delivered · 🟡 delivered with known defects (see [03 – Phase 2 defect register](03-implementation-plan.md#22-defect-register)) · ⬜ planned.

## FR-001 – Source registry and pluggable source modules ✅

### Description

A source is a connection to one external system (a server/database pair, a Modbus gateway or an OPC UA endpoint). Each source has a `kind`, and each kind is implemented by a self-registering module.

### Inputs

- `meta_sources` row: `name`, `kind` (`sqlserver` | `postgres` | `modbus` | `opcua`), `params_json`, `is_enabled`.

### Processing

- At startup every compiled-in module registers a factory under its kind.
- For each run, the runner resolves `$env:` secret references in `params_json` and asks the registry to build a source of the given kind.
- An unknown kind fails the run with `no source registered for kind "<kind>"`.

### Outputs

- A `contracts.Source` instance ready to `Open`, `Read` and `Close`.

---

## FR-002 – Load definitions ✅

### Description

A load is one data movement from one source object (table, view or tag group) to one target table.

### Inputs

- `meta_loads` row: `source_id`, `name`, `object_name`, `target_schema`, `target_table`, `mode`, `watermark_column`, `watermark_type`, `batch_size`, `auto_create_table`, `cron_expression`, `is_enabled`.

### Processing

- Enabled loads on enabled sources are scheduled.
- Load names are globally unique.

### Outputs

- Scheduled jobs (see FR-003).

---

## FR-003 – Scheduling with overlap guard ✅

### Description

Each load runs on its own cron schedule. A run that is still in progress when its next tick fires is not started twice.

### Inputs

- `cron_expression` in robfig/cron v3 syntax: five-field cron (`0 2 * * *`) or descriptors (`@every 5m`, `@hourly`).

### Processing

- On tick, if the load is already running, log `skipping overlapping run` at warning level and do nothing.
- Otherwise execute the run pipeline (FR-004/FR-005).
- Invalid cron expressions are logged and the load is skipped; other loads are unaffected.

### Outputs

- One `meta_load_runs` row per executed run.

---

## FR-004 – Delta load with watermark ✅ 🟡

### Description

Load only rows that are new since the last successful run, based on a datetime or numeric watermark column.

### Inputs

- `mode = delta`, `watermark_column`, `watermark_type` (`datetime` | `numeric`), the stored watermark from `meta_watermarks`.

### Processing

1. **First run** (no stored watermark): read `MAX(watermark_column)` from the source, store it, and load **no rows** (protects BR-003). Logged as `watermark initialized (first run, no rows loaded)`.
2. **Subsequent runs**:
   - *SQL Server*: snapshot the upper bound `MAX(wm)` **before** reading, then read `wm > last AND wm <= upper ORDER BY wm`. Rows inserted during the read are picked up next time.
   - *PostgreSQL*: read `wm > last ORDER BY wm`; the new watermark is the value from the last row read.
3. After a successful write, upsert the watermark and keep the previous value for audit.

### Outputs

- Rows written to the sink. Updated `meta_watermarks.value` and `previous_value`.

> **Known defects:** numeric watermarks on SQL Server are formatted in scientific notation once they reach 1,000,000, and a failed watermark update still marks the run successful. See D-03 and D-04.

---

## FR-005 – Full loads: append and replace ✅ 🟡

### Description

Load the whole source object, either adding to the target or replacing its contents.

### Inputs

- `mode = append` or `mode = replace`.

### Processing

- `append`: `SELECT *` and insert all rows.
- `replace`: truncate the target when the first batch arrives, then insert.

### Outputs

- Rows in the target table, all stamped with the same load timestamp.

> **Known defect:** if the source returns zero rows, a `replace` load does not truncate, so stale data remains. Truncate and insert are also not atomic. See D-05.

---

## FR-006 – Sink table management and load timestamp ✅

### Description

Target tables are created automatically on first write. Every row is stamped with the time of the load.

### Inputs

- `auto_create_table`, column metadata from the first batch.

### Processing

- If the target does not exist (`INFORMATION_SCHEMA.TABLES`), create it from the canonical column types (see [02 – Type mapping](02-architecture.md#canonical-type-mapping)) and append `yb_load_timestamp datetime2 NOT NULL`.
- Rows are written with SQL Server bulk copy.

### Outputs

- A populated target table. Every row in one run shares one `yb_load_timestamp`.

---

## FR-007 – Modbus TCP polling ✅

### Description

Poll a configured list of registers from a Modbus gateway and store one row per tag per poll.

### Inputs

- Source params: `host`, `port` (default 502), `timeout_ms` (default 2000).
- `meta_modbus_tags`: unit ID, register type (`coil` | `discrete` | `holding` | `input`), address, quantity, data type, byte/word order, scale, offset, optional `correlation_id`.

### Processing

- Read every enabled tag once per tick, serialised on one connection.
- Decode with byte and word order, then apply `value = raw * scale + offset`.
- A failing tag produces a row with `quality = bad` and the error in `value_text`; the run continues.
- The mode is forced to `append`; watermarks are ignored.

### Outputs

- Telemetry rows: `correlation_id, tag_name, ts, value_num, value_bool, value_text, quality`.

---

## FR-008 – OPC UA polling ✅

### Description

Batch-read configured OPC UA nodes in one request per tick.

### Inputs

- Source params: `endpoint` (required), `security_policy`, `security_mode`, `auth` (`anonymous` | `username`), `username`, `password`, `session_timeout_ms`, `request_timeout_ms`.
- `meta_opcua_nodes`: tag name, node ID, scale, offset.

### Processing

- One Read service call for all valid node IDs.
- The timestamp is the source timestamp, falling back to the server timestamp, then to the poll time.
- Status codes map to `good` / `uncertain` / `bad`. Numerics are widened to float64. Arrays and structures are stringified.
- Invalid node IDs or a total read failure produce `bad` rows rather than failing the run.

### Outputs

- Telemetry rows: `tag_name, node_id, ts, value_num, value_bool, value_text, quality`.

---

## FR-009 – Run audit and structured logging ✅ 🟡

### Description

Every run and every significant event is recorded in the metadata database.

### Inputs

- Runtime events from all components.

### Processing

- `meta_load_runs`: `run_uuid`, status (`running` → `success` | `failed`), rows read/written, watermark start/end, error, duration.
- `meta_log_entries`: every slog record is mirrored asynchronously through a bounded buffer of 512 records. When the buffer is full, records are dropped from the database copy but always reach the console.
- The well-known keys `run_uuid`, `load_id`, `source_id` and `category` are promoted to columns.

### Outputs

- Queryable audit trail.

> **Known defect:** attributes attached with `logger.With(...)` (which is how the runner attaches `load_id` and `run_uuid`) are not persisted to the database, so those columns are mostly NULL. See D-01.

---

## FR-010 – Health, readiness and status endpoints ✅

### Description

HTTP endpoints for process supervision and monitoring.

### Inputs

- `LOADER_HEALTH_ENABLED`, `LOADER_HEALTH_ADDR` (default `127.0.0.1:9090`).

### Processing

- `GET /healthz`: liveness; always 200 while the process serves.
- `GET /readyz`: pings the metadata and sink databases with a 2-second timeout; returns 503 if either fails.
- `GET /status`: in-memory snapshot of every scheduled load's last outcome.

### Outputs

- JSON responses; see [02 – API Requirements](02-architecture.md#api-requirements).

---

## FR-011 – Retention housekeeping ✅

### Description

Prevent unbounded growth of the metadata database.

### Processing

- On `LOADER_HOUSEKEEPING_CRON` (default `0 2 * * *`), delete log entries older than `LOADER_LOG_RETENTION_DAYS` (default 14) and terminal runs older than `LOADER_LOAD_RUN_RETENTION_DAYS` (default 90).
- Deletes run in batches of `LOADER_HOUSEKEEPING_BATCH_SIZE` with a pause between batches.
- Runs with status `running` are never pruned.

---

## FR-012 – Metadata hot reload ✅

### Description

Pick up metadata changes without a restart.

### Processing

- Every `LOADER_RELOAD_INTERVAL_SEC` (default 60) and on SIGHUP, compute a SHA-256 fingerprint over sources, loads, and per-load tag/node counts.
- If the fingerprint changed, add, remove or reschedule cron entries.
- Log `metadata reload complete` with the added, removed and updated load IDs.

---

## FR-013 – YAML seeding CLI ✅ 🟡

### Description

`cmd/seed` imports sources, loads, Modbus tags and OPC UA nodes from a YAML file.

### Processing

- Strict parsing: unknown keys fail.
- Sources and loads are upserted by name in one transaction. Tags and nodes are replaced per load. Nothing is deleted at the source or load level.

> **Known defect:** the sample `seeds/seed.yaml`, which is also the CLI's default `-file`, uses `enabled:` instead of `is_enabled:` and is therefore rejected by the strict parser. See D-06.

---

## FR-014 – Secret references ✅

### Description

Credentials are not stored in plain text in the metadata database.

### Processing

- Any param value of the form `$env:NAME` is replaced at run time with environment variable `NAME`. A missing variable fails the run with a clear error.

---

## FR-020 – Source management API ⬜ *(Phase 3)*

### Description

Create, read, update, disable and delete sources over REST.

### Inputs

- JSON source definitions. Secret fields are write-only.

### Processing

- Validate the kind and the kind-specific params against a per-kind schema.
- Store secrets according to ADR-016. Never return secret values.
- Use optimistic concurrency (ETag / `If-Match`).
- Trigger a scheduler reload immediately after a successful change.

### Outputs

- The source resource with secrets redacted. An audit event.

---

## FR-021 – Load, Modbus tag and OPC UA node management API ⬜ *(Phase 3)*

### Description

CRUD for loads and their tag/node lists.

### Processing

- Validate the cron expression, mode/watermark consistency, and the target identifiers.
- Replacing a tag list is atomic.
- Trigger an immediate reload.

---

## FR-022 – Source schema discovery ⬜ *(Phase 3)*

### Description

Let the configuration application see what a source offers before a load is defined.

### Inputs

- Source ID. An optional schema filter and name search.

### Processing

- *SQL sources*: list tables and views (schema, name, type, approximate row count). Describe a single object: columns, native and canonical types, nullability, primary key, and watermark candidates (datetime or ever-increasing numeric columns, with indexed ones ranked first).
- *Modbus / OPC UA*: return the fixed telemetry schema that the load will produce.
- Run read-only, with a timeout (`LOADER_DISCOVERY_TIMEOUT_SEC`, default 15).

### Outputs

- JSON object list and schema description; see [02 – API Requirements](02-architecture.md#api-requirements).

---

## FR-023 – Data preview (top 10) ⬜ *(Phase 3)*

### Description

Return up to 10 sample rows from a source object.

### Inputs

- Source ID, object name, optional `limit` (1–10, default 10), optional column list, optional `order_by_watermark=true` (latest rows first).

### Processing

- *SQL Server*: `SELECT TOP (n) …`. *PostgreSQL*: `… LIMIT n`. Run in a read-only transaction where supported, with a statement timeout.
- *Modbus / OPC UA*: perform one live poll of the configured or supplied tags. This is still read-only.
- Values are normalised with the same canonical types as a real load, so the preview matches what the sink will receive.
- Requires the `sources:preview` scope and is audited (the preview exposes real data).

### Outputs

- Columns plus up to 10 rows, and the elapsed time.

---

## FR-024 – Connection test ⬜ *(Phase 3)*

### Description

`POST /sources/{id}/test` (or `POST /connection-tests` for an unsaved definition) to verify connectivity and credentials.

### Outputs

- `ok`, latency, server version where available, and a sanitised error message.

---

## FR-025 – Operational control ⬜ *(Phase 3)*

### Description

Run a load now (respecting the overlap guard), enable or disable a load, and reset a watermark with a mandatory reason (recorded in `reset_reason`).

---

## FR-026 – Run and log history API ⬜ *(Phase 3)*

### Description

Paginated query of `meta_load_runs` and `meta_log_entries`, filterable by load, status, level, category, time range and `run_uuid`.

---

## FR-030 – Sink registry ⬜ *(Phase 4)*

### Description

Sinks become metadata (`meta_sinks`: name, kind, params, enabled) instead of a single environment-configured DSN. Supported kinds: `mssql` (existing) and `fabric` (new). The registry is extensible in the same way as the source registry.

---

## FR-031 – Multi-target loads ⬜ *(Phase 4)*

### Description

A load can deliver to one or more targets (`meta_load_targets`: load, sink, schema, table, write mode). The source is read **once** per run.

---

## FR-032 – Microsoft Fabric sink ⬜ *(Phase 4)*

### Description

Deliver load output to Microsoft Fabric. The recommended approach (ADR-019, Proposed) is to write Parquet files to a Fabric **Open Mirroring** landing zone in OneLake, authenticated with a Microsoft Entra ID service principal. Fabric then maintains the Delta tables, which are immediately queryable from the SQL analytics endpoint and Power BI.

---

## FR-033 – Per-target delivery state and store-and-forward ⬜ *(Phase 4)*

### Description

Each target tracks its own delivery progress, so a Fabric outage (for example a WAN link down) never blocks local loads. Remote targets catch up automatically from the local store when connectivity returns.

---

# Non-Functional Requirements

Targets marked *(assumed)* are engineering estimates that have not been measured. They must be validated during Phase 2 load testing (see [02 – Load tests](02-architecture.md#load-tests)).

## Performance

- SQL to SQL bulk throughput: ≥ 5,000 rows/s per load on the site LAN *(assumed)*.
- Telemetry polling: 500 OPC UA nodes or 200 Modbus tags per load at a ≥ 5 s interval without overlapping runs *(assumed)*.
- Scheduler jitter: < 1 s from the cron tick to the run start.
- Phase 3 API: p95 < 300 ms for metadata CRUD; schema discovery < 5 s; preview bounded by a 15 s timeout.
- Logging must never block a load. The DB log buffer drops records rather than stalling.

## Reliability

- Recover from source, device or network failure on the next tick, without a restart.
- Delivery semantics: **at-least-once** per load window. A crash after writing but before the watermark update can cause duplicates on the next run. Downstream consumers can de-duplicate with `yb_load_timestamp` and business keys.
- Availability target: 99.5% monthly for the loader process *(assumed)*, under systemd `Restart=on-failure`.
- Metadata and sink database backups are the responsibility of the site DBA. Watermarks live in the metadata database, which therefore must be backed up together with the sink.
- *(Phase 4)* A cloud-sink outage must not affect local loads (FR-033).

## Scalability

- Expected growth over 3 years *(assumed)*: up to 50 sources, 300 loads, 5,000 telemetry tags per site.
- One loader instance per site. Horizontal scaling across sites, not within a site.

## Security

- **Authentication (Phase 3):** OAuth 2.0 bearer tokens (JWT) issued by Microsoft Entra ID, with mutual TLS as an option. API keys are allowed only in development (ADR-014).
- **Authorization:** scopes `config:read`, `config:write`, `sources:preview`, `ops:run`, `ops:watermark`.
- **Encryption:** TLS 1.2+ for the API and for database connections where the source supports it (`encrypt`, `sslmode`). OPC UA `Basic256Sha256` with `SignAndEncrypt` is available.
- **Secrets:** never stored raw in `params_json`, never logged, never returned by the API (ADR-010, ADR-016).
- **Least privilege:** source credentials are read-only accounts. The health and API servers bind to a non-public interface by default.
- **OT network:** comply with the site's network segmentation (IEC 62443 / Purdue model). Inbound connectivity to the loader must be approved by site OT security (ADR-015).

## Maintainability

- Follow [04 AI Rules](04-ai-rules.md): Clean Architecture boundaries, dependency injection, small functions, `gofmt` with LF line endings, `golangci-lint` clean.
- Unit test coverage ≥ 80% per package (excluding `cmd/`). Integration tests for each source and sink kind.
- Godoc on every exported identifier. Every architectural change is captured as an ADR in [05 Decisions](05-decisions.md).

---

# Technical Constraints

## Programming Language

- Go
- 1.26.9 (`go.mod`)

## Runtime Environment

- Linux on site hardware or a VM, supervised by systemd (current deployment).
- An OCI container image is built by the Dagger pipeline (`pipeline/`, `pipeline-lib v0.1.2`). `pipeline.yaml` references Kubernetes manifests in `./deploy/k8s`, which do not exist yet.

## Database

- Microsoft SQL Server for the **metadata** database (GORM with go-mssqldb).
- Microsoft SQL Server for the **local sink** (`database/sql` bulk copy).
- *(Phase 4)* Microsoft Fabric (OneLake / Open Mirroring) as an additional sink.

## Cloud Platform

- Microsoft Azure: Microsoft Fabric and Microsoft Entra ID (Phases 3 and 4).

## External Systems

| System | Protocol | Library |
|--------|----------|---------|
| SQL Server sources (ERP and others) | TDS | `github.com/microsoft/go-mssqldb` v1.11.0 |
| PostgreSQL sources (MES and others) | PostgreSQL wire | `github.com/jackc/pgx/v5` v5.11.0 |
| PLCs / gateways | Modbus TCP | `github.com/simonvetter/modbus` v1.6.4 |
| SCADA servers | OPC UA binary | `github.com/gopcua/opcua` v0.9.1 |
| Scheduler | – | `github.com/robfig/cron/v3` v3.0.1 |
| Configuration application *(Phase 3)* | HTTPS / JSON | – |
| Microsoft Fabric *(Phase 4)* | OneLake (ADLS Gen2 API) | Azure SDK for Go *(proposed)* |

---

# Assumptions

The AI rules require assumptions to be stated explicitly. Each of these should be confirmed or corrected by the product owner.

1. One loader instance per mine site; sites do not share a metadata database.
2. The local SQL Server sink remains the system of record on site, even after Fabric is added.
3. The Phase 3 configuration application is operated by the central data team and reaches the loader through a path approved by site OT security (ADR-015 is still open).
4. Microsoft Entra ID is the corporate identity provider.
5. Performance and scale figures marked *(assumed)* are estimates, not measurements.
6. Duplicate rows after a crash (at-least-once delivery) are acceptable to downstream consumers.
7. Preview data may contain sensitive business data. Access is controlled by scope and audited; masking is not required in Phase 3.
