# Edge Data Loader

## Design Notes
 - Why strings for watermark values? It keeps the schema simple and portable across watermark types (datetime, bigint, decimal). The WatermarkType on Load tells the source module how to parse/compare it. Trade-off: slight parsing overhead per run — negligible.
 - Why ParamsJSON instead of columns per source type? Each source module needs different config (SQL: server/db/user; Modbus: host/port/unit-id; OPC-UA: endpoint/security policy). JSON keeps the schema stable while allowing modules to evolve independently. The module owns the schema of its params.
 - Why a separate Watermark table (not a column on Load)? Cleaner audit trail (previous value, reset time, reset reason) and it separates config from runtime state. Config is user-owned; watermark is loader-owned.
 - Why RunUUID in addition to ID? The UUID is used for log correlation and will be safe to sync to the cloud later without primary-key collisions.
 - Non-blocking DB writes. If the metadata DB is slow or briefly down, log records queue in a bounded channel. Once full, new records drop (console still gets them). This prevents logging from ever stalling a load.
 - Repository.Log was left in place. Nothing that used it before breaks. But new code should prefer slog — cleaner and gives you dual output for free.
 - Well-known attribute promotion. Keys run_uuid, load_id, source_id, category map to typed columns in meta_log_entries. Everything else falls into details as JSON. This keeps queries fast without giving up flexibility.
 - No third-party log library. slog is standard, fast, and won't be abandoned.

## What's missing intentionally (keeping v1 lean):
 - No retry policy table yet — add later.
 - No schema drift tracking — AutoCreateTable only.
 - No secrets manager integration — start with env-var references in ParamsJSON (e.g. {"password": "$env:SRC1\_PW"}).

## Run
```
export LOADER_METADATA_DSN='sqlserver://user:pw@localhost?database=DataLoaderMeta'
export LOADER_SINK_DSN='sqlserver://user:pw@localhost?database=DataLoaderSink'
export ERP_PW='actual-password'
export LOADER_LOG_LEVEL=info      # debug | info | warn | error
export LOADER_LOG_FORMAT=json     # json | text
go run ./cmd/loader
```

## Seeding

For production-quality seeding, don't blindly insert. Use MERGE or check existence first:

```
IF NOT EXISTS (
   SELECT 1
   FROM meta\_sources
   WHERE name = @p1
)
BEGIN
   INSERT ...
END
```

or

```
MERGE meta\_sources AS target
USING (
   SELECT @p1 AS name
) AS source
ON target.name = source.name
WHEN NOT MATCHED THEN
   INSERT (...);
```

### RUN

```
export DB\_CONNECTION="sqlserver://user:password@host:1433?database=mydb"
go run ./cmd/seed -file seeds/seed.yaml
```

This gives you a reusable seeding mechanism where new sources and loads can be added simply by editing YAML instead of changing Go code.

Remember, once you have seeded, 
`go run ./cmd/seed -file seeds/mes-postgres.yaml`
you might have to add an environment variable with the password.
`export MES_PW='the-actual-password'`

## Postgres Source Module
###Testing Checklist
 - Connectivity test — create a load with mode: append against a small Postgres table first. Confirm the target table is created and rows land with yb_load_timestamp populated.
 - Delta test — enable a watermark column, run once, insert a new row in Postgres, wait for the next tick. Only the new row should land.
 - Type mapping test — try a table containing NUMERIC, UUID, JSONB, TIMESTAMPTZ, and BOOLEAN. All should land as NVARCHAR(MAX) / FLOAT / BIT / DATETIME2 in MS SQL. If you want native UNIQUEIDENTIFIER or NVARCHAR(36) for UUIDs later, we'd extend the sink's type mapper.
 - SSL test — set sslmode: require against a Postgres server that enforces TLS.
### Known Behaviors Worth Understanding
 - NUMERIC precision loss. Postgres NUMERIC values come across as strings (via []byte) and are being stored in NVARCHAR(MAX) in the sink. This preserves precision perfectly. If you later want them as DECIMAL(p,s) in MS SQL you'd need per-column metadata — happy to add that when it becomes a real need.
 - JSONB columns land as JSON text in NVARCHAR(MAX). MS SQL 2022+ has decent JSON support, so this is usable.
 - TIMESTAMPTZ normalization to UTC. All timestamps are converted to UTC before storage. This matches how we already handle SQL Server datetimes. Watermark comparisons therefore always operate in UTC.
search_path. If you don't fully qualify object_name (i.e. use orders instead of public.orders), Postgres will resolve it against the search_path you set in params. Recommendation: always fully qualify in your load config to avoid surprises.

