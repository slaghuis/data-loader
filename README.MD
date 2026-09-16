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

## Modbus Data Loader
### Design Notes Worth Understanding
**Why one poll cycle = one batch (not one row per tag)?** Batch writes are dramatically faster than row-by-row inserts. Even a 200-tag PLC produces 200 rows per tick, and the sink chunks them appropriately.
**Why not use the driver's typed read helpers (e.g. ReadFloat32)?** They exist and work, but doing our own decoding lets us:
 - Support types (int64, uint64) that some drivers omit.
 - Return decode errors as quality = "bad" rows instead of losing the tag entirely.
 - Keep endianness/word-order handling in one testable function.
**Why store quality even if the sink table is untyped?** Bad readings are how you spot broken sensors. If you always drop them, you can't tell "sensor is offline" from "sensor read zero". Preserving bad rows with the error message in value_text gives you diagnostic breadcrumbs.
**Why serialize with a mutex?** Modbus TCP is transaction-oriented; simultaneous requests on the same connection interleave and corrupt responses. A single sync.Mutex around reads is the correct pattern — and since a load runs once per tick under the scheduler's running guard, contention is essentially zero.
**Why fail-open on connection errors?** If the PLC drops mid-poll, individual tag reads return errors and are stamped bad. The connection stays open for the next tick. If the entire load fails to open, the run is marked failed — same as SQL sources. This means transient network blips at a mine site don't require a restart.
### Testing Checklist
1. **Start a Modbus simulator** — diagslave or the excellent ModbusPal work well on Linux.
2. **Seed one holding-register tag with float32, data_type: float32, scale: 1.0.** Confirm the value lands in value_num.
3. **Seed one coil tag.** Confirm it lands in value_bool.
4. **Kill the simulator mid-run.** Confirm the next poll writes quality: bad rows without crashing the loader.
5. **Restart the simulator.** Confirm the loader recovers on the next tick without restart.
6. **Check yb_load_timestamp** — all rows from one poll cycle should share the same value.
### Known Limitations (v1, deliberate)
 - **No block-read optimization.** Each tag is a separate Modbus transaction. On a slow serial-over-TCP link with 100+ tags this adds up. If it becomes a problem, we group contiguous addresses of the same register type into one read — happy to add when needed.
 - ** No reconnect logic on total link failure.**  If the TCP connection dies (not just individual reads), the next full run will fail. The scheduler retries on the next tick, which reopens the client — so this self-heals within one polling interval. Explicit reconnect-in-place could be added if intervals get long.
 - ** No per-tag polling frequency.**  All tags in a load share one cron schedule. If you need "read temperature every 60s but pressure every 1s", create two loads.
