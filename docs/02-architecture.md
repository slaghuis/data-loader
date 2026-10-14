# Architecture Requirements

| Item | Value |
|------|-------|
| Document | 02 – Architecture |
| Version | 1.0 |
| Last updated | 2026-10-14 |
| Related | [01 Requirements](01-project-requirements.md) · [03 Implementation Plan](03-implementation-plan.md) · [04 AI Rules](04-ai-rules.md) · [05 Decisions](05-decisions.md) |

Sections marked **(as built)** describe the Phase 1 code. Sections marked **(target)** describe the Phase 2–4 design.

## Solution Overview

The loader is a single Go binary (`cmd/loader`) built around a small engine:

1. **Metadata repository** (SQL Server, GORM). Defines *what* to load: sources, loads, tag lists. Records *what happened*: runs, watermarks, logs.
2. **Scheduler** (robfig/cron). Fires each load on its cron expression, with an overlap guard. A **reload coordinator** keeps the schedule in sync with metadata (fingerprint polling plus SIGHUP).
3. **Runner** (pipeline). For one run it resolves secrets, builds the source through the **source registry**, reads in batches, writes each batch to the **sink**, advances the watermark, and records the outcome.
4. **Source modules** (`sqlserver`, `postgres`, `modbus`, `opcua`) implement `contracts.Source` and self-register by kind.
5. **Sink** (`mssql`) implements `contracts.Sink`: auto-create table, truncate, and bulk-copy write with `yb_load_timestamp`.
6. **Cross-cutting**: slog structured logging (console plus asynchronous DB mirror), health/readiness/status HTTP server, retention housekeeping, and the YAML seeding CLI (`cmd/seed`).

## Required Patterns

- **Ports and adapters (hexagonal / Clean Architecture).** The engine depends on `pkg/contracts` interfaces (`Source`, `Sink`), never on concrete drivers (ADR-001).
- **Plugin registry.** Source modules register a `SourceFactory` by kind in `init()`. The runner builds by kind. Sinks follow the same pattern in Phase 4 (ADR-005, ADR-018).
- **Streaming batches.** Sources push `contracts.Batch` values to a `BatchHandler` callback. Memory is bounded by `batch_size`, never by table size.
- **Metadata-driven behaviour.** All runtime behaviour comes from metadata rows or environment variables. Nothing is hard-coded per site (ADR-004).
- **Constructor dependency injection.** Collaborators are passed in through `New…` functions and wired only in `cmd/*/main.go`.
- **Context propagation.** `context.Context` is the first parameter of every I/O call. The run-scoped logger travels in the context (`logging.WithContext` / `FromContext`).
- **Structured logging with categories.** Use the `category` key (`connect`, `read`, `write`, `watermark`, `schedule`, `health`, `housekeeping`, and in Phase 3 `api` and `audit`).
- **Idempotent reconciliation.** Seeding and schedule reloads converge on the desired state and are safe to repeat.

## Prohibited Patterns

- Writing to any source system or OT device (BR-008).
- Business logic in `cmd/*/main.go` beyond wiring.
- Package-level mutable dependencies. Phase 1 has two: `modbus.tagRepo` and `opcua.nodeRepo` (D-14); they are removed in Phase 2.
- Building SQL by concatenating identifiers that have not been validated and quoted. Values must always be bind parameters.
- Secrets in `params_json`, logs, API responses or error messages.
- `panic` for control flow, and swallowing errors without logging (`_ = err` without a comment explaining why).
- Using the ORM on the bulk data path. GORM is for metadata only; data moves through `database/sql` or driver bulk APIs.
- Third-party logging libraries. Use `log/slog` only (ADR-008).

## Layering

```mermaid
flowchart TB
    subgraph Presentation
        CMD_LOADER["cmd/loader (wiring)"]
        CMD_SEED["cmd/seed (CLI)"]
        HEALTH["internal/health (HTTP)"]
        API["internal/api (REST) – Phase 3"]:::planned
    end
    subgraph Application
        SCHED["internal/scheduler"]
        RUNNER["internal/pipeline (Runner)"]
        RELOAD["internal/reload"]
        HK["internal/housekeeping"]
        SEED["internal/seed"]
        RUNSTATE["internal/runstate"]
        DISCOVERY["internal/discovery – Phase 3"]:::planned
    end
    subgraph Domain
        CONTRACTS["pkg/contracts (Source, Sink, Batch, Watermark, LoadMode)"]
    end
    subgraph Infrastructure
        META["internal/metadata (GORM repo)"]
        SINK["internal/sink (mssql; fabric – Phase 4)"]
        SOURCES["internal/sources/* (sqlserver, postgres, modbus, opcua)"]
        LOGGING["internal/logging (slog + DB handler)"]
        CONFIG["internal/config (env, secrets)"]
    end
    Presentation --> Application
    Application --> Domain
    Infrastructure --> Domain
    Application -. "via interfaces (Phase 2)" .-> Infrastructure
    classDef planned stroke-dasharray: 5 5
```

### Presentation

- `cmd/loader`: reads configuration, wires every dependency, and handles OS signals and graceful shutdown. Contains no business logic.
- `cmd/seed`: CLI that applies a YAML seed file to the metadata database.
- `internal/health`: `/healthz`, `/readyz`, `/status`.
- *(Phase 3)* `internal/api`: REST management API. Handles HTTP concerns (authentication, validation, problem+json errors, ETags) and delegates to application services.

### Application

- `scheduler`: reconciles cron entries with metadata and enforces the overlap guard.
- `pipeline.Runner`: orchestrates one run (source → batches → sink → watermark → audit).
- `reload`, `housekeeping`, `seed`, `runstate`: lifecycle and operational services.
- *(Phase 3)* `discovery`: schema discovery and preview orchestration (timeouts, row cap, audit).
- *(Phase 4)* Delivery orchestration across multiple targets.

### Domain

- `pkg/contracts`: the stable vocabulary. `Source`, `Sink`, `SourceFactory`, `LoadConfig`, `Batch`, `Row`, `Column` (canonical types), `Watermark`, `WatermarkType`, `LoadMode`, `ReadResult`.
- Rules (as built, but scattered): first-run watermark initialisation, telemetry sources forced to append, and watermark advance only on success. Phase 2 consolidates them into domain functions with unit tests.

### Infrastructure

- `metadata`: GORM models and repository for all `meta_*` tables, batched pruning, and the fingerprint.
- `sources/*`: protocol adapters and type normalisation into canonical types.
- `sink`: MSSQL bulk copy and DDL. *(Phase 4)* `fabric`.
- `logging`: slog construction and the asynchronous DB mirror handler.
- `config`: environment parsing and `$env:` secret resolution.

> **Phase 1 conformance gap:** `pipeline.Runner`, `scheduler.Scheduler` and `housekeeping.Housekeeper` depend on the concrete `*metadata.Repository`. The Modbus and OPC UA sources import `internal/metadata` models (infrastructure leaking into an adapter that should only see the domain). Phase 2 introduces narrow repository interfaces in the consuming packages (see [Interface Definitions](#interface-definitions)).

---

## Architecture Diagrams

### System context (target, Phases 1–4)

```mermaid
flowchart LR
    subgraph Mine["Mine site (OT / site network)"]
        ERP[(ERP<br/>SQL Server)]
        MES[(MES<br/>PostgreSQL)]
        PLC[[Crusher PLC<br/>Modbus TCP]]
        SCADA[[Mill SCADA<br/>OPC UA]]
        LOADER["Edge Data Loader<br/>(Go service)"]
        META[(Metadata DB<br/>SQL Server)]
        SINKDB[(Local sink<br/>SQL Server)]
        OPS["Site ops / monitoring<br/>(systemd, uptime probe)"]
    end
    subgraph Cloud["Microsoft Azure"]
        CFGAPP["Configuration app<br/>(separate, Phase 3)"]
        ENTRA["Microsoft Entra ID"]
        FABRIC[("Microsoft Fabric<br/>OneLake – Phase 4")]
    end
    ERP -->|read-only| LOADER
    MES -->|read-only| LOADER
    PLC -->|poll, read-only| LOADER
    SCADA -->|poll, read-only| LOADER
    LOADER <--> META
    LOADER -->|bulk copy| SINKDB
    OPS -->|/healthz /readyz /status| LOADER
    CFGAPP -->|"HTTPS + JWT (Phase 3)"| LOADER
    CFGAPP --> ENTRA
    LOADER -->|"Parquet via OneLake (Phase 4)"| FABRIC
    LOADER --> ENTRA
```

### Components (as built)

```mermaid
flowchart TB
    MAIN["cmd/loader main"] --> CFG["config.FromEnv"]
    MAIN --> REPO["metadata.Repository"]
    MAIN --> SINK["sink.MSSQLSink"]
    MAIN --> SCHED["scheduler.Scheduler"]
    MAIN --> HS["health.Server"]
    MAIN --> RC["reload.Coordinator"]
    MAIN --> HK["housekeeping.Housekeeper"]
    MAIN --> DBH["logging.DBHandler"]
    RC -->|Reload| SCHED
    SCHED -->|fingerprint, ListEnabledLoads| REPO
    SCHED -->|trigger| RUNNER["pipeline.Runner"]
    RUNNER -->|Build kind| REG["sources registry"]
    REG --> SQLS["sqlserver"]
    REG --> PG["postgres"]
    REG --> MB["modbus"]
    REG --> UA["opcua"]
    MB -.->|"ListModbusTags (global)"| REPO
    UA -.->|"ListOPCUANodes (global)"| REPO
    RUNNER -->|EnsureTable / Truncate / WriteBatch| SINK
    RUNNER -->|watermark, StartRun, FinishRun| REPO
    RUNNER --> RS["runstate.Registry"]
    HS --> RS
    HK -->|PruneLogEntries, PruneLoadRuns| REPO
    DBH -->|async Log| REPO
```

### Run sequence: delta load (as built)

```mermaid
sequenceDiagram
    autonumber
    participant Cron as Scheduler (cron tick)
    participant R as Runner
    participant Reg as Source registry
    participant S as Source (e.g. sqlserver)
    participant M as Metadata repo
    participant K as Sink (MSSQL)

    Cron->>Cron: overlap guard (skip if running)
    Cron->>R: Execute(load)
    R->>R: ResolveParams ($env: secrets)
    R->>Reg: Build(kind, params)
    Reg-->>R: Source
    R->>S: Open(ctx)
    R->>M: GetWatermark(load)
    R->>M: StartRun → run_uuid (status=running)
    alt no stored watermark (first run)
        R->>S: Read()
        S->>S: SELECT MAX(wm)
        S-->>R: NewWatermark, WatermarkInit=true, 0 rows
    else subsequent run
        R->>S: Read(handler)
        S->>S: upper = SELECT MAX(wm)
        S->>S: SELECT * WHERE wm > last AND wm <= upper ORDER BY wm
        loop each batch (batch_size rows)
            S->>R: handler(batch)
            opt first batch
                R->>K: EnsureTable (if auto_create_table)
                R->>K: Truncate (if mode=replace)
            end
            R->>K: WriteBatch(rows, yb_load_timestamp)
        end
        S-->>R: RowsRead, NewWatermark=upper
    end
    R->>M: UpsertWatermark (delta only, previous_value kept)
    R->>M: FinishRun(success|failed, rows, wm_end, error)
    R->>S: Close()
```

### Watermark lifecycle

```mermaid
stateDiagram-v2
    [*] --> Empty: load created (delta)
    Empty --> Initialised: first run stores MAX(wm), loads 0 rows
    Initialised --> Advanced: successful run with new rows
    Advanced --> Advanced: successful run (value → previous_value)
    Advanced --> Advanced: run fails (watermark unchanged)
    Advanced --> Empty: ResetWatermark(reason) → next run re-initialises
    Initialised --> Empty: ResetWatermark(reason)
```

> A reset sets the value to empty, so the next run **re-initialises to the current MAX and loads nothing**. To back-fill history, change the mode to `append`/`replace` for one run, or (Phase 3) use the API reset endpoint with an explicit `value` to rewind to.

### Target components (Phases 3–4)

```mermaid
flowchart TB
    subgraph Presentation
        API["internal/api<br/>REST /api/v1 · OpenAPI 3.1 · JWT · ETag"]
        HEALTH["internal/health<br/>+ /metrics (Phase 2)"]
    end
    subgraph Application
        CFGSVC["config service<br/>(validate → repo → reload)"]
        DISC["discovery service<br/>(timeouts, row cap, audit)"]
        SCHED["scheduler"]
        RUNNER["runner"]
        DELIV["delivery orchestrator<br/>(fan-out, per-target state) – P4"]
        FWD["forwarder<br/>(store-and-forward to remote sinks) – P4"]
    end
    subgraph Domain
        C["contracts: Source, SchemaDiscoverer, Previewer,<br/>Sink, SinkCapabilities, TargetSpec"]
    end
    subgraph Infrastructure
        SRCS["sources/* (+ discovery impl)"]
        MSSQL["sink/mssql"]
        FAB["sink/fabric (Open Mirroring → OneLake)"]
        REPO["metadata repo + migrations"]
        SEC["secret store (ADR-016)"]
    end
    API --> CFGSVC & DISC
    CFGSVC --> REPO
    CFGSVC -->|Reload| SCHED
    DISC --> SRCS
    SCHED --> RUNNER --> DELIV
    DELIV --> MSSQL
    FWD -->|read new rows by yb_load_timestamp| MSSQL
    FWD --> FAB
    RUNNER --> SRCS
    SRCS --> SEC
```

---

## Folder Structure

```text
data-loader/
├── cmd/
│   ├── loader/main.go          # service entrypoint; wiring + signals only
│   └── seed/main.go            # YAML → metadata CLI
├── internal/
│   ├── config/                 # env config, $env: secret resolution
│   ├── health/                 # /healthz /readyz /status (+ /metrics in P2)
│   ├── housekeeping/           # retention pruning on cron
│   ├── logging/                # slog factory, context helpers, DB mirror handler
│   ├── metadata/               # GORM models, repository, AutoMigrate, fingerprint
│   ├── pipeline/               # Runner: one load execution
│   ├── reload/                 # SIGHUP + periodic reload coordinator
│   ├── runstate/               # in-memory last-run registry for /status
│   ├── scheduler/              # cron reconciliation + overlap guard
│   ├── seed/                   # seed file parsing + transactional apply
│   ├── sink/                   # mssql.go (P4: sink registry, fabric/)
│   ├── sources/                # registry.go + one package per kind
│   │   ├── modbus/
│   │   ├── opcua/
│   │   ├── postgres/
│   │   └── sqlserver/
│   ├── api/                    # (P3) REST handlers, auth, problem+json, openapi.yaml
│   ├── discovery/              # (P3) schema discovery + preview service
│   ├── secrets/                # (P3) secret store per ADR-016
│   └── delivery/               # (P4) multi-target orchestration + forwarder
├── pkg/contracts/              # domain ports: Source, Sink, types
├── migrations/                 # (P2) versioned SQL migrations (ADR-012)
├── deploy/                     # (P2) systemd unit, k8s manifests referenced by pipeline.yaml
├── test/integration/           # (P2) testcontainers-based integration suites
├── seeds/                      # example seed files
├── pipeline/                   # Dagger CI pipeline (pipeline-lib)
├── docs/                       # this documentation set
├── pipeline.yaml               # CI configuration (lint/test/scan/build/deploy)
└── README.MD
```

---

# Data Model

## Metadata database (as built)

Created by `metadata.AutoMigrate` (GORM) at startup. Phase 2 moves to versioned migrations (ADR-012).

```mermaid
erDiagram
    meta_sources ||--o{ meta_loads : "has"
    meta_loads ||--o| meta_watermarks : "tracks"
    meta_loads ||--o{ meta_load_runs : "executes"
    meta_loads ||--o{ meta_modbus_tags : "polls"
    meta_loads ||--o{ meta_opcua_nodes : "polls"
    meta_load_runs ||--o{ meta_log_entries : "run_uuid (logical)"

    meta_sources {
        bigint id PK
        nvarchar name UK
        nvarchar kind
        nvarchar description
        nvarchar params_json
        bit is_enabled
        datetime created_at
        datetime updated_at
        datetime deleted_at
    }
    meta_loads {
        bigint id PK
        bigint source_id FK
        nvarchar name UK
        nvarchar object_name
        nvarchar target_schema
        nvarchar target_table
        nvarchar mode
        nvarchar watermark_column
        nvarchar watermark_type
        int batch_size
        bit auto_create_table
        nvarchar cron_expression
        bit is_enabled
        datetime deleted_at
    }
    meta_watermarks {
        bigint load_id PK
        nvarchar value
        nvarchar previous_value
        datetime updated_at
        datetime reset_at
        nvarchar reset_reason
    }
    meta_load_runs {
        bigint id PK
        nvarchar run_uuid UK
        bigint load_id
        nvarchar status
        datetime started_at
        datetime finished_at
        bigint rows_read
        bigint rows_written
        nvarchar watermark_start
        nvarchar watermark_end
        nvarchar error_message
        bigint duration_ms
    }
    meta_log_entries {
        bigint id PK
        datetime created_at
        nvarchar level
        nvarchar run_uuid
        bigint load_id
        bigint source_id
        nvarchar category
        nvarchar message
        nvarchar details
    }
    meta_modbus_tags {
        bigint id PK
        bigint load_id
        nvarchar correlation_id
        nvarchar tag_name
        tinyint unit_id
        nvarchar register_type
        int address
        int quantity
        nvarchar data_type
        nvarchar byte_order
        nvarchar word_order
        float scale
        float offset
        bit is_enabled
    }
    meta_opcua_nodes {
        bigint id PK
        bigint load_id
        nvarchar tag_name
        nvarchar node_id
        float scale
        float offset
        bit is_enabled
    }
```

> Foreign keys: GORM creates a constraint only for `meta_loads.source_id → meta_sources.id` (declared by the association). Other `load_id` columns are indexed but not constrained. Phase 2 migrations add explicit constraints.

## Source

```text
meta_sources
------------
id, name, kind, description, params_json, is_enabled, created_at, updated_at, deleted_at
```

### Field Definitions

| Field | Type | Description |
|-------|------|-------------|
| id | bigint identity | Primary key |
| name | nvarchar(200), unique | Human-readable unique name, e.g. `erp-prod` |
| kind | nvarchar(50), indexed | `sqlserver` \| `postgres` \| `modbus` \| `opcua` (P4 adds none; sinks are separate) |
| description | nvarchar(500) | Free text |
| params_json | nvarchar(max) | JSON object of string → string, kind-specific (see [Source params](#source-params-by-kind)). Secrets as `$env:NAME` |
| is_enabled | bit, default 1 | Intended to stop all loads on the source — **not enforced today** (see note) |
| deleted_at | datetime2 null | GORM soft delete |

> Note: `ListEnabledLoads` filters on `meta_loads.is_enabled` only. A disabled **source** does not stop its loads. Phase 2 fixes this (D-19).

## Load

```text
meta_loads
----------
id, source_id, name, object_name, target_schema, target_table, mode, watermark_column,
watermark_type, batch_size, auto_create_table, cron_expression, is_enabled, timestamps
```

### Field Definitions

| Field | Type | Description |
|-------|------|-------------|
| source_id | bigint FK | Owning source |
| name | nvarchar(200), unique | e.g. `erp.orders` |
| object_name | nvarchar(400) | `schema.table` / view for SQL; logical group name for telemetry |
| target_schema | nvarchar(100), default `dbo` | Sink schema |
| target_table | nvarchar(200) | Sink table |
| mode | nvarchar(20) | `delta` \| `append` \| `replace` |
| watermark_column | nvarchar(200) | Required for `delta` |
| watermark_type | nvarchar(20), default `none` | `none` \| `datetime` \| `numeric` |
| batch_size | int, default 1000 | Rows per batch handed to the sink |
| auto_create_table | bit, default 1 | Create target on first batch if missing |
| cron_expression | nvarchar(100) | robfig/cron v3 (`@every 5m`, `0 */2 * * *`) |
| is_enabled | bit, default 1 | Scheduled only when true |

## Watermark

| Field | Type | Description |
|-------|------|-------------|
| load_id | bigint PK | One row per load |
| value | nvarchar(100) | Datetime as RFC 3339 (UTC, nanoseconds) or numeric as string |
| previous_value | nvarchar(100) | Value before the last change (audit / manual rollback) |
| updated_at | datetime2 | Last change |
| reset_at / reset_reason | datetime2 / nvarchar(500) | Set by `ResetWatermark` |

## LoadRun

| Field | Type | Description |
|-------|------|-------------|
| run_uuid | nvarchar(36), unique | Correlation ID across logs; cloud-safe identifier |
| status | nvarchar(20) | `running` \| `success` \| `failed` (`aborted` reserved, unused) |
| rows_read / rows_written | bigint | Counts |
| watermark_start / watermark_end | nvarchar(100) | Range covered |
| error_message | nvarchar(max) | Failure reason |
| duration_ms | bigint | Wall-clock duration |

## LogEntry

| Field | Type | Description |
|-------|------|-------------|
| level | nvarchar(10) | `debug` \| `info` \| `warn` \| `error` |
| run_uuid, load_id, source_id | nullable | Promoted correlation keys |
| category | nvarchar(50) | See [Logging](#logging-requirements) |
| message | nvarchar(max) | slog message |
| details | nvarchar(max) | JSON of remaining attributes |

## ModbusTag / OPCUANode

See [01 FR-007 / FR-008](01-project-requirements.md#fr-007--modbus-tcp-polling-). Scaling for both: `value = raw * scale + offset` (numeric only).

## Sink tables (as built)

**SQL sources** — columns mirror the source result set, mapped through canonical types, plus `yb_load_timestamp datetime2 NOT NULL`.

**Telemetry sources** — fixed narrow schema:

| Column | Modbus | OPC UA | Type |
|--------|:-----:|:-----:|------|
| correlation_id | ✓ | – | nvarchar(max) NULL |
| tag_name | ✓ | ✓ | nvarchar(max) |
| node_id | – | ✓ | nvarchar(max) |
| ts | ✓ (poll time) | ✓ (source → server → poll time) | datetime2 |
| value_num | ✓ | ✓ | float NULL |
| value_bool | ✓ | ✓ | bit NULL |
| value_text | ✓ | ✓ | nvarchar(max) NULL (also carries error text for bad quality) |
| quality | ✓ | ✓ | nvarchar(max): `good` \| `uncertain` \| `bad` |
| yb_load_timestamp | ✓ | ✓ | datetime2 |

### Canonical type mapping

| Canonical | From SQL Server | From PostgreSQL | → MSSQL sink | → Fabric (P4, Parquet) |
|-----------|-----------------|-----------------|--------------|------------------------|
| bool | BIT | BOOL | bit | BOOLEAN |
| int32 | TINYINT, SMALLINT, INT | INT2, INT4 | int | INT32 |
| int64 | BIGINT | INT8 | bigint | INT64 |
| float32 | REAL | FLOAT4 | real | FLOAT |
| float64 | FLOAT | FLOAT8 | float | DOUBLE |
| decimal | DECIMAL, NUMERIC, MONEY, SMALLMONEY | NUMERIC, MONEY | decimal(38,10) | DECIMAL(38,10) |
| datetime | DATE, DATETIME*, TIME, DATETIMEOFFSET | TIMESTAMP(TZ), DATE, TIME(TZ) | datetime2 | TIMESTAMP (UTC, µs) |
| bytes | BINARY, VARBINARY, IMAGE | BYTEA | varbinary(max) | BINARY |
| guid | UNIQUEIDENTIFIER | – (UUID → string) | uniqueidentifier | STRING (36) |
| string | everything else | TEXT, VARCHAR, UUID, JSON(B), … | nvarchar(max) | STRING (UTF-8) |

## Phase 3 additions (target)

| Table | Purpose |
|-------|---------|
| `meta_audit_events` | id, occurred_at, actor (JWT `sub`/`appid`), action (`source.create`, `load.update`, `source.preview`, `watermark.reset`, …), resource type/id, request_id, outcome, details JSON (never secrets) |
| `meta_secrets` | Per ADR-016: name, ciphertext (AES-256-GCM), key_id, created_at, rotated_at. Params reference `$secret:NAME` |
| `row_version` column | `rowversion` on `meta_sources`, `meta_loads` → exposed as ETag for optimistic concurrency |

## Phase 4 additions (target)

```mermaid
erDiagram
    meta_sinks ||--o{ meta_load_targets : "receives"
    meta_loads ||--o{ meta_load_targets : "delivers to"
    meta_load_targets ||--o| meta_target_cursors : "progress"
    meta_load_runs ||--o{ meta_target_runs : "per target"
    meta_sinks {
        bigint id PK
        nvarchar name UK
        nvarchar kind "mssql | fabric"
        nvarchar params_json
        nvarchar delivery "direct | forward"
        bit is_enabled
    }
    meta_load_targets {
        bigint id PK
        bigint load_id FK
        bigint sink_id FK
        nvarchar target_schema
        nvarchar target_table
        nvarchar write_mode "inherit | append | replace | upsert"
        nvarchar key_columns "for upsert / mirroring"
        bit is_enabled
    }
    meta_target_cursors {
        bigint load_target_id PK
        datetime last_forwarded_load_ts
        nvarchar last_file_sequence
        datetime updated_at
    }
    meta_target_runs {
        bigint id PK
        bigint load_run_id FK
        bigint load_target_id FK
        nvarchar status
        bigint rows_written
        nvarchar error_message
    }
```

Migration rule: on upgrade, create one `meta_sinks` row (`local`, `mssql`, from `LOADER_SINK_DSN`) and one `meta_load_targets` row per existing load, so Phase 1 behaviour is preserved with zero configuration.

---

# API Requirements

## As built: health server

Bound to `LOADER_HEALTH_ADDR` (default `127.0.0.1:9090`). No authentication (loopback only by default).

### `GET /healthz`

```json
{ "status": "ok", "uptime_seconds": 143 }
```

### `GET /readyz`

200 when both databases answer a ping within 2 s, otherwise 503:

```json
{ "status": "degraded", "metadata": "ok", "sink": "dial tcp 10.0.0.9:1433: i/o timeout" }
```

### `GET /status`

```json
{
  "version": "dev",
  "go_version": "go1.26.9",
  "started_at": "2026-10-14T06:15:22Z",
  "uptime_seconds": 143,
  "summary": { "total_loads": 3, "running": 0, "last_success": 2, "last_failed": 1 },
  "loads": [
    {
      "load_id": 1, "load_name": "erp.orders", "source_name": "erp-prod",
      "source_kind": "sqlserver", "cron_expression": "@every 5m",
      "last_run_uuid": "7c1e…", "last_status": "success",
      "last_rows_read": 42, "last_rows_written": 42, "currently_running": false
    }
  ]
}
```

## Phase 3: management API (target)

**Conventions**

- Base path `/api/v1`, JSON (UTF-8), OpenAPI 3.1 contract at `internal/api/openapi.yaml` (contract-first; handlers validated against it in tests).
- Served on `LOADER_API_ADDR` over TLS. Auth: `Authorization: Bearer <JWT>` (Entra ID), scopes per endpoint (ADR-014).
- Errors: RFC 9457 `application/problem+json`. Every response carries `X-Request-Id`.
- Concurrency: `GET` returns `ETag`; `PUT`/`PATCH`/`DELETE` require `If-Match` (412 on mismatch).
- Pagination: `?limit=50&cursor=<opaque>` → `{ "items": [...], "next_cursor": "…" }`.
- Secret fields are **write-only**: responses show `"password": { "set": true }`.
- Every mutation writes `meta_audit_events` and triggers `Scheduler.Reload` synchronously before responding.

### Endpoint catalogue

| Method | Path | Scope | Purpose |
|--------|------|-------|---------|
| GET | `/source-kinds` | config:read | Supported kinds + JSON Schema of their params |
| GET / POST | `/sources` | config:read / config:write | List / create |
| GET / PUT / PATCH / DELETE | `/sources/{id}` | config:* | Read / replace / partial update / soft delete (409 if loads exist) |
| POST | `/sources/{id}/test` | config:write | Connection test (FR-024) |
| POST | `/connection-tests` | config:write | Test an unsaved definition |
| GET | `/sources/{id}/objects` | config:read | List tables/views (FR-022) |
| GET | `/sources/{id}/objects/{object}/schema` | config:read | Describe columns + watermark candidates |
| GET | `/sources/{id}/objects/{object}/preview` | sources:preview | Top-N rows, N ≤ 10 (FR-023) |
| GET / POST | `/loads` | config:* | List (filter by source, enabled) / create |
| GET / PUT / PATCH / DELETE | `/loads/{id}` | config:* | CRUD |
| GET / PUT | `/loads/{id}/modbus-tags` | config:* | Read / atomically replace tag list |
| GET / PUT | `/loads/{id}/opcua-nodes` | config:* | Read / atomically replace node list |
| POST | `/loads/{id}/runs` | ops:run | Run now (202; 409 if already running) |
| GET | `/loads/{id}/runs` | config:read | Run history |
| GET | `/runs/{run_uuid}` | config:read | One run + its log entries |
| GET | `/loads/{id}/watermark` | config:read | Current watermark |
| POST | `/loads/{id}/watermark/reset` | ops:watermark | Reset (reason required, optional explicit value) |
| GET | `/logs` | config:read | Query log entries |
| POST | `/validate/cron` | config:read | Validate + next 5 fire times |
| GET | `/sinks`, `/loads/{id}/targets` | config:* | (Phase 4) sink + target management |

### `POST /api/v1/sources`

#### Request

```json
{
  "name": "erp-prod",
  "kind": "sqlserver",
  "description": "ERP production DB",
  "is_enabled": true,
  "params": {
    "server": "10.0.0.5",
    "database": "ERP",
    "user": "reader",
    "encrypt": "true"
  },
  "secrets": { "password": "s3cr3t!" }
}
```

#### Response — `201 Created`, `Location: /api/v1/sources/7`, `ETag: "AAAAAAAAB9E="`

```json
{
  "id": 7,
  "name": "erp-prod",
  "kind": "sqlserver",
  "description": "ERP production DB",
  "is_enabled": true,
  "params": { "server": "10.0.0.5", "database": "ERP", "user": "reader", "encrypt": "true" },
  "secrets": { "password": { "set": true } },
  "created_at": "2026-10-14T11:02:00Z",
  "updated_at": "2026-10-14T11:02:00Z"
}
```

### `GET /api/v1/sources/{id}/objects?schema=dbo&search=order`

```json
{
  "items": [
    { "schema": "dbo", "name": "Orders", "type": "table", "approx_rows": 1840221 },
    { "schema": "dbo", "name": "vw_OpenOrders", "type": "view", "approx_rows": null }
  ],
  "next_cursor": null
}
```

Implementation: SQL Server `sys.objects` + `sys.schemas` + `sys.partitions` (approx rows); PostgreSQL `information_schema.tables` + `pg_class.reltuples`. Modbus/OPC UA return a single logical object (`telemetry`).

### `GET /api/v1/sources/{id}/objects/dbo.Orders/schema`

```json
{
  "object": { "schema": "dbo", "name": "Orders", "type": "table" },
  "columns": [
    { "name": "OrderId",    "ordinal": 1, "native_type": "int",       "canonical_type": "int32",    "nullable": false, "primary_key": true },
    { "name": "CustomerId", "ordinal": 2, "native_type": "uniqueidentifier", "canonical_type": "guid", "nullable": false, "primary_key": false },
    { "name": "Amount",     "ordinal": 3, "native_type": "decimal(18,2)", "canonical_type": "decimal", "nullable": true, "primary_key": false },
    { "name": "ModifiedAt", "ordinal": 4, "native_type": "datetime2(7)", "canonical_type": "datetime", "nullable": false, "primary_key": false }
  ],
  "watermark_candidates": [
    { "column": "ModifiedAt", "watermark_type": "datetime", "indexed": true,  "reason": "non-null datetime, indexed" },
    { "column": "OrderId",    "watermark_type": "numeric",  "indexed": true,  "reason": "identity primary key (inserts only — misses updates)" }
  ],
  "proposed_target": { "schema": "dbo", "table": "erp_orders", "create_table_ddl": "CREATE TABLE [dbo].[erp_orders] (…)" }
}
```

`proposed_target.create_table_ddl` is produced by the sink's own type mapper, so the configuration app shows the user exactly what will be created.

### `GET /api/v1/sources/{id}/objects/dbo.Orders/preview?limit=10&order_by_watermark=ModifiedAt`

```json
{
  "columns": [
    { "name": "OrderId", "canonical_type": "int32" },
    { "name": "CustomerId", "canonical_type": "guid" },
    { "name": "Amount", "canonical_type": "decimal" },
    { "name": "ModifiedAt", "canonical_type": "datetime" }
  ],
  "rows": [
    [1840221, "A7027B07-49BC-4766-A283-4B776DFA74DA", "1520.00", "2026-10-14T10:58:31.1200000Z"]
  ],
  "row_count": 1,
  "limit": 10,
  "truncated_columns": [],
  "elapsed_ms": 84
}
```

Rules: `limit` is clamped to 1–10; long text/binary values truncated to 4 KiB (listed in `truncated_columns`); executed as `SELECT TOP (@n) <quoted cols> FROM <quoted object> [ORDER BY <wm> DESC]` with a 15 s statement timeout in a read-only context (`ApplicationIntent=ReadOnly` / `SET TRANSACTION READ ONLY`); audited as `source.preview`.

### `POST /api/v1/loads`

```json
{
  "name": "erp.orders",
  "source_id": 7,
  "object_name": "dbo.Orders",
  "target": { "schema": "dbo", "table": "erp_orders" },
  "mode": "delta",
  "watermark": { "column": "ModifiedAt", "type": "datetime" },
  "batch_size": 1000,
  "auto_create_table": true,
  "cron_expression": "@every 5m",
  "is_enabled": true
}
```

Response `201` with the stored load, `next_run_at`, and `ETag`.

### `POST /api/v1/loads/{id}/watermark/reset`

```json
{ "reason": "Re-load October after source correction", "value": "2026-10-01T00:00:00Z" }
```

`value` omitted ⇒ clear (next run re-initialises to MAX, loading nothing). Response `200` with the new watermark row.

### Error Responses

```json
{
  "type": "https://data-loader/errors/validation",
  "title": "Validation failed",
  "status": 422,
  "detail": "mode 'delta' requires watermark.column and watermark.type",
  "instance": "/api/v1/loads",
  "request_id": "01JA…",
  "errors": [ { "field": "watermark.column", "message": "required when mode=delta" } ]
}
```

| Code | Meaning |
|------|---------|
| 400 | Bad Request — malformed JSON / parameters |
| 401 | Unauthorized — missing/invalid token |
| 403 | Forbidden — token lacks required scope |
| 404 | Not Found |
| 409 | Conflict — duplicate name, load already running, delete with dependants |
| 412 | Precondition Failed — `If-Match` ETag mismatch |
| 422 | Unprocessable — semantic validation (cron, mode/watermark, identifiers) |
| 429 | Too Many Requests — preview/discovery rate limit per client |
| 502 | Bad Gateway — source unreachable / authentication to source failed |
| 504 | Gateway Timeout — discovery/preview exceeded timeout |
| 500 | Internal Error |

---

# Interface Definitions

## As built (`pkg/contracts`)

```go
type Source interface {
    Kind() string
    Open(ctx context.Context) error
    Ping(ctx context.Context) error
    Read(ctx context.Context, load LoadConfig, handler BatchHandler) (ReadResult, error)
    Close() error
}

type Sink interface {
    Open(ctx context.Context) error
    Ping(ctx context.Context) error
    EnsureTable(ctx context.Context, target TargetTable, columns []Column) error
    Truncate(ctx context.Context, target TargetTable) error
    WriteBatch(ctx context.Context, target TargetTable, batch Batch, loadTS time.Time) (int64, error)
    Close() error
}

type SourceFactory func(cfg SourceConfig) (Source, error)
type BatchHandler func(ctx context.Context, batch Batch) error
```

## Phase 2: narrow ports for dependency injection (target)

Defined in the **consuming** package (Go idiom: accept interfaces where used).

```go
// internal/pipeline
type RunStore interface {
    GetWatermark(ctx context.Context, loadID int64) (*metadata.Watermark, error)
    UpsertWatermark(ctx context.Context, loadID int64, value string) error
    StartRun(ctx context.Context, loadID int64, watermarkStart string) (*metadata.LoadRun, error)
    FinishRun(ctx context.Context, run *metadata.LoadRun, status string, rowsRead, rowsWritten int64, wmEnd, errMsg string) error
}
type SourceBuilder interface {
    Build(cfg contracts.SourceConfig) (contracts.Source, error)
}

// internal/scheduler
type LoadCatalog interface {
    ListEnabledLoads(ctx context.Context) ([]metadata.Load, error)
    MetadataFingerprint(ctx context.Context) (string, error)
}
type Executor interface {
    Execute(ctx context.Context, load metadata.Load) error
}

// pkg/contracts — replaces modbus.tagRepo / opcua.nodeRepo globals
type PointCatalog interface {
    ListPoints(ctx context.Context, loadID int64) ([]PointSpec, error)
}
```

`SourceConfig` gains a `Points PointCatalog` field (or the factory signature gains a deps struct) so telemetry modules receive their catalogue by injection instead of a global.

## Phase 3: discovery capabilities (target)

Optional interfaces, detected with a type assertion so modules opt in independently (ADR-017):

```go
// pkg/contracts

// SchemaDiscoverer is implemented by sources that can enumerate and describe objects.
type SchemaDiscoverer interface {
    ListObjects(ctx context.Context, filter ObjectFilter) ([]ObjectInfo, error)
    DescribeObject(ctx context.Context, object string) (ObjectSchema, error)
}

// Previewer is implemented by sources that can return a bounded sample.
// Implementations MUST enforce limit <= MaxPreviewRows and MUST NOT write.
type Previewer interface {
    Preview(ctx context.Context, req PreviewRequest) (Batch, error)
}

const MaxPreviewRows = 10

type ObjectFilter struct{ Schema, Search string; Limit int; Cursor string }
type ObjectInfo   struct{ Schema, Name, Type string; ApproxRows *int64 }
type ObjectSchema struct {
    Object               ObjectInfo
    Columns              []ColumnInfo
    WatermarkCandidates  []WatermarkCandidate
}
type ColumnInfo struct {
    Column                       // embeds Name, DataType (canonical), Nullable
    Ordinal    int
    NativeType string
    PrimaryKey bool
}
type WatermarkCandidate struct{ Column string; Type WatermarkType; Indexed bool; Reason string }
type PreviewRequest struct {
    Object       string
    Columns      []string
    Limit        int
    OrderByDesc  string // optional watermark column → latest rows first
}

// ConnectionTester is implemented by all sources (wraps Open+Ping+Close with diagnostics).
type ConnectionTester interface {
    TestConnection(ctx context.Context) (ConnectionReport, error)
}
```

## Phase 4: sink registry and capabilities (target)

```go
// pkg/contracts
type SinkConfig struct {
    SinkID int64
    Kind   string            // "mssql" | "fabric"
    Name   string
    Params map[string]string // secrets resolved
}
type SinkFactory func(cfg SinkConfig) (Sink, error)

type SinkCapabilities struct {
    Truncate       bool // supports replace mode natively
    Upsert         bool // supports key-based merge (Fabric Open Mirroring: yes)
    Transactional  bool // batch writes are atomic
    Remote         bool // behind WAN → must use store-and-forward (ADR-018)
}

// Describer is optional; sinks without it are assumed {Truncate:true}.
type Describer interface{ Capabilities() SinkCapabilities }

// Flusher is optional: sinks that buffer (e.g. Parquet files) commit on Flush.
type Flusher interface {
    Flush(ctx context.Context, target TargetTable) error
}
```

---

# User Stories

## US-001 — Add a SQL table feed without a developer

**As a** site data engineer **I want** to register a new SQL Server table as a delta load **So that** its new rows appear in the local sink within one schedule interval.

### Acceptance Criteria

- Given a seed file (P1) or API call (P3) defining source `erp-prod` and load `erp.orders` (`delta`, `ModifiedAt`, `@every 5m`)
- When the metadata is applied and the reload interval (≤ 60 s) passes or SIGHUP is sent
- Then the first run stores `MAX(ModifiedAt)` as watermark and loads 0 rows
- And subsequent runs load only rows with `ModifiedAt` greater than the watermark into `dbo.erp_orders`, each stamped with `yb_load_timestamp`

## US-002 — Discover schema and preview before configuring *(P3)*

**As a** data engineer using the configuration app **I want** to browse a source's tables, see column types and suggested watermark columns, and view the top 10 rows **So that** I can configure a correct load without database access.

### Acceptance Criteria

- Given a source with valid credentials and a token with `config:read` and `sources:preview`
- When I call `GET …/objects`, `…/schema`, and `…/preview?limit=10`
- Then I receive objects, columns with canonical types, ranked watermark candidates, and at most 10 rows
- And a request with `limit=500` returns at most 10 rows
- And the preview is recorded in `meta_audit_events` with my identity
- And a token without `sources:preview` receives 403

## US-003 — Know whether the loader is healthy

**As a** site operator **I want** a readiness endpoint and per-load status **So that** monitoring alerts me when loads fail.

### Acceptance Criteria

- Given the sink database is unreachable
- When the probe calls `/readyz`
- Then it returns 503 with the sink error
- And `/status` shows `last_status: failed` with `last_error` for affected loads

## US-004 — Telemetry survives device outages

**As a** process engineer **I want** bad readings recorded rather than dropped **So that** I can tell "sensor offline" from "sensor reads zero".

### Acceptance Criteria

- Given a Modbus gateway that stops responding mid-poll
- When the next tick runs
- Then rows are written with `quality = bad` and the error in `value_text`, or the run is marked failed if the connection cannot open
- And the loader recovers on the first tick after the device returns, without restart

## US-005 — Pause and rewind a load remotely *(P3)*

**As a** data engineer **I want** to disable a load and rewind its watermark with a reason **So that** I can re-load a corrected period.

### Acceptance Criteria

- Given load `erp.orders` with ETag `E1`
- When I `PATCH` `{ "is_enabled": false }` with `If-Match: E1`
- Then the load is unscheduled before the response returns
- When I `POST …/watermark/reset` with reason and value `2026-10-01T00:00:00Z`
- Then `previous_value`, `reset_at`, `reset_reason` are recorded and the next run loads from that value

## US-006 — Data in Fabric despite an unreliable WAN *(P4)*

**As an** enterprise analyst **I want** mine data in Microsoft Fabric **So that** I can report across sites.

### Acceptance Criteria

- Given load `erp.orders` with targets `local` (mssql) and `fabric-prod` (fabric)
- When the WAN is down for 4 hours
- Then local loads continue unaffected and `fabric-prod` target status shows `lagging`
- When the WAN returns
- Then the forwarder uploads the backlog in order and the Fabric table converges to the local table (verified by row counts per `yb_load_timestamp`)

---

# Business Rules

## Rule 1 — First delta run never back-loads

A delta load without a stored watermark records the source's current `MAX(watermark_column)` and loads zero rows. Historical data requires an explicit full load or a watermark rewind.

## Rule 2 — Watermark advances only after a successful write

The watermark is upserted only after all batches are written without error. *(Defect D-04: a failure of the upsert itself is currently ignored.)*

## Rule 3 — Telemetry sources are append-only

Loads on `modbus`/`opcua` sources are forced to `append` with no watermark, regardless of configuration (warning logged).

## Rule 4 — Replace truncates before the first write

`replace` truncates the target immediately before the first batch. *(Defect D-05: zero-row results skip truncation.)*

## Rule 5 — No overlapping runs of the same load

A tick that fires while the same load is running is skipped and logged; different loads run concurrently.

## Rule 6 — One load timestamp per run

All rows written by one run share one `yb_load_timestamp` (UTC).

## Rule 7 — Running runs are never pruned

Housekeeping deletes only terminal runs (`success`, `failed`, `aborted`).

## Rule 8 — Sources are read-only

No module may issue a write, DDL, or device write function (Modbus function codes 05/06/15/16, OPC UA Write/Call) against a source.

## Rule 9 — Preview is bounded *(P3)*

Preview returns at most 10 rows, within 15 s, audited, and requires `sources:preview`.

## Rule 10 — Names are unique and stable

Source and load names are unique; seeding and the API upsert/address by name and ID respectively. Renames are explicit operations.

---

# Configuration Requirements

## Required Settings

Process configuration is environment-only (`config.FromEnv`). Example `/etc/data-loader/environment`:

```yaml
# required
LOADER_METADATA_DSN: sqlserver://loader:***@sql01?database=DataLoaderMeta
LOADER_SINK_DSN:     sqlserver://loader:***@sql01?database=DataLoaderSink
# logging
LOADER_LOG_LEVEL: info            # debug | info | warn | error
LOADER_LOG_FORMAT: json           # json | text
```

## Environment Variables

| Variable | Default | Phase | Description |
|----------|---------|:-----:|-------------|
| `LOADER_METADATA_DSN` | — (required) | 1 | Metadata DB connection string |
| `LOADER_SINK_DSN` | — (required) | 1 | Local sink DB connection string (P4: seeds the `local` sink row) |
| `LOADER_LOG_LEVEL` | `info` | 1 | Minimum level |
| `LOADER_LOG_FORMAT` | `json` | 1 | Console format |
| `LOADER_HOUSEKEEPING_ENABLED` | `true` | 1 | |
| `LOADER_HOUSEKEEPING_CRON` | `0 2 * * *` | 1 | Server local time |
| `LOADER_LOG_RETENTION_DAYS` | `14` | 1 | `0` disables log pruning |
| `LOADER_LOAD_RUN_RETENTION_DAYS` | `90` | 1 | `0` disables run pruning |
| `LOADER_HOUSEKEEPING_BATCH_SIZE` | `5000` | 1 | Rows per DELETE batch |
| `LOADER_HOUSEKEEPING_BATCH_PAUSE_MS` | `100` | 1 | Pause between batches |
| `LOADER_HEALTH_ENABLED` | `true` | 1 | |
| `LOADER_HEALTH_ADDR` | `127.0.0.1:9090` | 1 | |
| `LOADER_RELOAD_ENABLED` | `true` | 1 | |
| `LOADER_RELOAD_INTERVAL_SEC` | `60` | 1 | `0` disables periodic reload |
| `LOADER_RELOAD_ON_SIGHUP` | `true` | 1 | |
| *secret refs* e.g. `ERP_PW` | — | 1 | Referenced from params as `$env:ERP_PW` |
| `LOADER_VERSION` (ldflags) | `dev` | 2 | Build version injected via `-ldflags -X` |
| `LOADER_SHUTDOWN_TIMEOUT_SEC` | `60` | 2 | Wait for in-flight runs on stop |
| `LOADER_API_ENABLED` | `false` | 3 | |
| `LOADER_API_ADDR` | `127.0.0.1:8443` | 3 | |
| `LOADER_API_TLS_CERT_FILE` / `_KEY_FILE` | — | 3 | Required when API enabled (except dev) |
| `LOADER_API_CLIENT_CA_FILE` | — | 3 | Enables mTLS |
| `LOADER_API_AUTH_MODE` | `jwt` | 3 | `jwt` \| `apikey` (dev only) |
| `LOADER_API_JWT_ISSUER` / `_AUDIENCE` | — | 3 | Entra ID tenant issuer / app ID URI |
| `LOADER_API_JWKS_URL` | derived from issuer | 3 | Cached signing keys (needs outbound HTTPS) |
| `LOADER_DISCOVERY_TIMEOUT_SEC` | `15` | 3 | Discovery/preview statement timeout |
| `LOADER_PREVIEW_RATE_PER_MIN` | `30` | 3 | Per-client preview rate limit |
| `LOADER_SECRETS_KEY` / `_KEY_FILE` | — | 3 | AES-256 key for `meta_secrets` (ADR-016) |
| `LOADER_FORWARDER_INTERVAL_SEC` | `30` | 4 | Store-and-forward cycle |
| `LOADER_FORWARDER_MAX_BATCH_ROWS` | `100000` | 4 | Rows per Parquet file |
| `AZURE_TENANT_ID` / `AZURE_CLIENT_ID` / `AZURE_CLIENT_SECRET` (or `AZURE_CLIENT_CERTIFICATE_PATH`) | — | 4 | Service principal for OneLake (Azure SDK default credential chain) |

## Source params by kind

| Kind | Params (`*` = required) |
|------|-------------------------|
| `sqlserver` | `server`*, `database`*, `user`, `password`, `port` (1433), `encrypt`, `trustservercertificate` |
| `postgres` | `host`*, `database`*, `user`, `password`, `port` (5432), `sslmode` (`disable`), `search_path`, `application_name` (`data-loader`) |
| `modbus` | `host`*, `port` (502), `timeout_ms` (2000) |
| `opcua` | `endpoint`*, `security_policy` (`None`), `security_mode` (`None`), `auth` (`anonymous`\|`username`), `username`, `password`, `session_timeout_ms`, `request_timeout_ms` |
| `fabric` *(P4 sink)* | `workspace_id`*, `mirrored_database_id`*, `onelake_endpoint` (`https://onelake.dfs.fabric.microsoft.com`), `auth` (`service_principal`\|`managed_identity`) |

## Seed file schema

```yaml
sources:
  - name: erp-prod            # unique
    kind: sqlserver
    description: ERP production DB
    is_enabled: true          # NOTE: 'is_enabled', not 'enabled' (strict parser)
    params: { server: 10.0.0.5, database: ERP, user: reader, password: $env:ERP_PW }
loads:
  - name: erp.orders
    source: erp-prod
    object_name: dbo.Orders
    target_schema: dbo
    target_table: erp_orders
    mode: delta               # delta | append | replace
    watermark_column: ModifiedAt
    watermark_type: datetime  # none | datetime | numeric
    batch_size: 1000
    auto_create_table: true
    cron_expression: "@every 5m"
    is_enabled: true
modbus_tags: [ { load: crusher.telemetry, tag_name: motor_amps, unit_id: 1, register_type: holding, address: 100, data_type: float32 } ]
opcua_nodes: [ { load: mill.telemetry, tag_name: mill_speed, node_id: "ns=2;s=Mill1.Speed" } ]
```

---

# Logging Requirements

## Log Levels

- **Error** — run failed, source/sink unreachable, reload or housekeeping failure.
- **Warning** — overlapping run skipped, forced append mode, watermark update failure, no enabled tags/nodes, close errors.
- **Information** — lifecycle (start/stop), run start/complete, watermark initialised/advanced, truncation, reload summary, housekeeping summary.
- **Debug** — source opened, target ensured, per-batch progress, executed query text (never parameter values).

## Categories

`connect` · `read` · `write` · `watermark` · `schedule` · `health` · `housekeeping` · *(P3)* `api` · `audit` · *(P4)* `delivery` · `forward`

## Required Audit Events

| Event | Where recorded | Phase |
|-------|----------------|:-----:|
| Source connection attempt / failure | `meta_log_entries` (connect) | 1 |
| Run started / completed / failed (rows, duration, error) | `meta_load_runs` + log | 1 |
| Watermark initialised / advanced / reset (from → to, reason) | `meta_watermarks` + log | 1 |
| Target table created / truncated | log (write) | 1 |
| Schedule added / removed / updated | log (schedule) | 1 |
| Metadata changed via API (actor, before/after hash) | `meta_audit_events` | 3 |
| Preview executed (actor, object, row count) | `meta_audit_events` | 3 |
| Authentication / authorisation failure | log (api) | 3 |
| Target delivery lagging / recovered | `meta_target_runs` + log | 4 |

> **D-01:** today `load_id`, `run_uuid`, `source_id` attached via `logger.With` are not persisted; fixing this is a Phase 2 exit criterion because the audit trail depends on it.

---

# Monitoring Requirements

## Health Checks

- `/healthz` — process alive (liveness).
- `/readyz` — metadata DB and sink ping < 2 s (readiness). *(P4: per-sink, remote sinks reported but not failing readiness.)*
- `/status` — per-load last outcome for dashboards and support.

## Metrics *(Phase 2: Prometheus `/metrics` on the health server)*

- `loader_runs_total{load,status}` (counter)
- `loader_run_duration_seconds{load}` (histogram)
- `loader_rows_read_total{load}`, `loader_rows_written_total{load,target}` (counters)
- `loader_last_success_timestamp_seconds{load}` (gauge) — drives staleness alerts
- `loader_overlap_skips_total{load}` (counter)
- `loader_telemetry_bad_quality_total{load}` (counter)
- `loader_dblog_dropped_total` (counter) — DB log buffer overflow
- `loader_watermark_lag_seconds{load}` (gauge, datetime watermarks: now − watermark)
- *(P3)* `loader_api_requests_total{route,code}`, `loader_api_request_duration_seconds{route}`
- *(P4)* `loader_target_backlog_rows{target}`, `loader_target_last_delivered_timestamp_seconds{target}`

## Alerts

- `/readyz` non-200 for > 2 minutes — **critical**.
- No successful run of a load for > 3 × its schedule interval — **high**.
- `loader_runs_total{status="failed"}` increases on 3 consecutive runs of a load — **high**.
- Telemetry bad-quality ratio > 20 % over 15 min — **medium** (device/network issue).
- `loader_dblog_dropped_total` increasing — **medium** (metadata DB slow).
- `meta_log_entries` row count growing over 48 h despite housekeeping — **low**.
- *(P4)* Fabric target backlog > 24 h — **high**.

---

# Testing Requirements

## Unit Tests

Coverage Target:

```text
>= 80% per package (excluding cmd/*), enforced by pipeline.yaml test.coverage_min
```

Priority unit targets (pure functions first, then fakes):

- `sqlserver`: `sanitizeObject`, `canonicalType`, `normalizeValue`, `formatMSSQLGUID`, `buildDSN` (escaping), `buildQuery`.
- `postgres`: `quoteIdent`, `quoteQualifiedIdent`, `buildDSN`, `buildQuery`, `parseWatermark`, `formatWatermark`, `mapDriverType`.
- `modbus`: decoding for every data type × byte/word order, scaling, quantity defaults.
- `opcua`: `qualityFromStatus`, value assignment per variant type, `buildClientOptions`, `policyURI`.
- `sink`: `mapToSQLType`, `toGUID`, DDL generation.
- `config`: `FromEnv` defaults/required, `ResolveParams` (`$env:` present/missing/invalid JSON).
- `logging`: `DBHandler` attribute promotion incl. `WithAttrs` (regression for D-01), drop-on-full, close.
- `pipeline.Runner` with fake source/sink/store: delta init, advance, failure keeps watermark, replace truncation, telemetry forced append, watermark-upsert failure fails run (D-04).
- `scheduler`: reload diff (add/remove/update), overlap guard, invalid cron isolation.
- `runstate`, `reload`, `housekeeping` (fake clock/repo), `seed` (parsing, validation, `defaultQuantity`).

## Integration Tests

Build tag `integration`, run with `testcontainers-go`; enabled in `pipeline.yaml` (`integration.enabled: true`).

Validate:

- SQL Server → MSSQL sink: auto-create, append, replace, delta (datetime and numeric ≥ 1,000,000 — regression for D-03), GUID/decimal/datetime round-trip.
- PostgreSQL → MSSQL sink: NUMERIC, UUID, JSONB, TIMESTAMPTZ, BOOLEAN mapping; delta with `search_path`.
- Modbus: in-process server from `simonvetter/modbus` (it ships a server) — coils, holding float32/float64 with word orders; server stop → bad rows → recovery.
- OPC UA: simulation server container (or gopcua test server) — batch read, invalid node id → bad row, server restart.
- Metadata: `AutoMigrate`/migrations up and down, fingerprint stability, batched prune.
- *(P3)* API contract tests against `openapi.yaml`; discovery and preview against SQL Server and PostgreSQL containers.
- *(P4)* Fabric sink against Azurite (ADLS Gen2 API emulation) for file layout; nightly test against a real Fabric test workspace.

## Failure Tests

Validate:

- Source unreachable at open → run `failed`, watermark unchanged, next tick recovers.
- Source drops mid-read → run `failed`, partial rows written, watermark unchanged (document duplicates on retry).
- Sink unreachable → `/readyz` 503, runs fail, recovery without restart.
- Metadata DB unreachable → logs continue to console, buffered DB logs dropped and counted, no deadlock.
- SIGTERM during a long run → run finishes or is marked `aborted` within `LOADER_SHUTDOWN_TIMEOUT_SEC`; no panic (regression for D-02).
- Invalid cron / unknown kind / missing `$env:` secret → only that load fails.
- *(P3)* Preview timeout → 504, no lingering session; ETag conflict → 412.
- *(P4)* WAN loss to Fabric → local unaffected, backlog drains in order after recovery.

## Load Tests

Validate:

- 1,000,000-row SQL Server table append at `batch_size` 1000/5000/10000 → throughput and memory ceiling.
- 300 loads with `@every 1m` → scheduler jitter < 1 s, no overlap storms, metadata DB connections bounded.
- 500 OPC UA nodes at 5 s, 200 Modbus tags at 5 s on one source → poll duration < interval.
- Housekeeping over 10 M log rows → batches do not block concurrent inserts.

---

# Deployment Requirements

## Environments

- **Development** — local Docker SQL Server (`mcr.microsoft.com/mssql/server`), simulators (diagslave/ModbusPal, Prosys OPC UA simulation server).
- **Test** — CI (Dagger pipeline: lint, test with race + coverage, govulncheck, trivy, build image).
- **QA** — site-representative VM/k3d (`k3d-dev` / `data-loader-staging` per `pipeline.yaml`) against replica sources.
- **Production** — one instance per mine site (systemd service today; `prod` kube context per `pipeline.yaml` once manifests exist). Telegram approval gate on staging and production releases.

## Deployment Strategy

- **Single-instance replace (recreate)** per site — two concurrent instances would double-schedule loads. Stop old (graceful drain) → migrate metadata → start new.
- Blue/Green and rolling are **not** applicable without a leader-election mechanism (backlog).
- Schema migrations follow **expand → migrate → contract** so the previous version can still run against the expanded schema.

## Rollback Requirements

- Keep the previous binary (`/opt/data-loader/releases/<version>`) or image tag; rollback = repoint + restart.
- Migrations must be backward compatible for one version (expand/contract); destructive "contract" migrations ship one release after the code that stops using the column.
- Watermarks are forward-only state: after rollback, loads resume from the stored watermark — no data loss, possible duplicates per at-least-once semantics.
- Phase 4: rolling back below the multi-sink schema requires disabling remote targets first.

## systemd unit (current)

```ini
[Unit]
Description=Mine Data Loader
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=data-loader
EnvironmentFile=/etc/data-loader/environment
ExecStart=/opt/data-loader/loader
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=10
TimeoutStopSec=90

[Install]
WantedBy=multi-user.target
```

---

# Deliverables

| Deliverable | Phase 1 (as built) | Target phase |
|-------------|:------------------:|:------------:|
| Architecture design | ✅ this document | — |
| Mermaid diagrams | ✅ this document | updated per phase |
| Folder structure | ✅ | P3/P4 additions above |
| Database schema | ✅ GORM AutoMigrate | P2 versioned migrations |
| Source code | ✅ | P3 API, P4 sinks |
| Unit tests | ❌ none | P2 (≥ 80 %) |
| Integration tests | ❌ none | P2 |
| Deployment guide | 🟡 README + systemd above | P2 `deploy/` (systemd + k8s) |
| Monitoring guide | 🟡 this document | P2 `/metrics` + alert rules |
| Configuration documentation | ✅ this document | updated per phase |
