# Edge Data Loader

## Design brief
A modular data loader in Go that will run on mine, and load data from various data sources and save the data to a local Microsoft SQL database.  The data loader should be modular, in the sense that one module will read SQL sources, one module will read Postgres sources, one module will read Modbus TCP sources, and one module will read OPC-UA sources.  There might be more modules in the future.  
The data loader will be meta data driven, with all the meta data in a database.  Any data source will be associated to a module. For a SQL database, for example, the data source will be the server | database combination.  For Modbus, it will be the gateway, etc.  
The metadata repository will carry specific data loads.  For SQL, it might be a table or a view.    Every load will have a schedule (hourly, every n minutes, daily, etc), a target table in the sink database.  A load will be governed by a watermark.  A good watermark will be a datetime field in a table for instance.  It could also be numeric.  Sometimes a table might not have a watermark.  First prize would be to pick up from the database server any new records that has been added since the last load, but alternatively an option should be available to do a full load.     The full load can the either replace (purge and re-populate) the existing data in the target table in the sink database or append the data.  Every data table in the sink database will be appended with a [load timestamp] field.
The mine will have many SQL sources for instance.  On a set configurable schedule, the data loader will confirm that it is connected to the specified data source, (and connect if not). Then the loader will confirm the sink data table exists and create it if not.  The data loader will retrieve the last watermark, and if none exists, set the last value in the table as the watermark and execute a delta or full load of the data by reading data from the source and pushing it into the target table in the sink database.  If the load if for a watermark-based delta load, the query will only load all new data since the last watermark.  After the load, the watermark will be set to  
The attempt to connect to a data source, the attempt to read a table and do a load and any error messages or success messages, new watermarks created and watermark resets will be logged in a table in the data loader metadata repository.
The reason we are storing all the metadata of the loads in a database is that I want to use the data loader to retrieve the metadata from the cloud in future and push any logs back into the cloud. More about that later.

## Where the Loader Stands Now
Every one of the original requirements is now implemented, plus:
 - Structured JSON logging (console + DB)
 - Overlap-safe scheduling
 - Health/readiness/status HTTP endpoints
 - Automatic retention/housekeeping
 - Hot-reloadable metadata (SIGHUP + periodic)

This is a proper operational tool. Enjoy the shakedown period — a couple of days of real polling on a real mine will surface exactly the things that no design conversation can predict. Keep an eye on:
 - meta_log_entries row count over 48 hours to confirm housekeeping keeps up.
 - /status after intentional network blips to make sure error messages are useful and recovery is clean.
 - Load run durations — anything drifting upward is a smell.
 - Modbus/OPC-UA quality: bad rates — spikes are a network or PLC concern, not a loader concern, but the loader should be reporting them faithfully.


Next step
Cloud sync (metadata down, logs up), but this needs to be designed thoughtfully — it's the most architecturally consequential feature left on the roadmap.

## Run
```
export LOADER_METADATA_DSN='sqlserver://user:pw@localhost?database=DataLoaderMeta'
export LOADER_SINK_DSN='sqlserver://user:pw@localhost?database=DataLoaderSink'
export ERP_PW='actual-password'   # see seeding
export LOADER_LOG_LEVEL=info      # debug | info | warn | error
export LOADER_LOG_FORMAT=json     # json | text
# Housekeeping (all optional; sensible defaults shown)
export LOADER_HOUSEKEEPING_ENABLED=true
export LOADER_HOUSEKEEPING_CRON="0 2 * * *"      # daily at 02:00
export LOADER_LOG_RETENTION_DAYS=14
export LOADER_LOAD_RUN_RETENTION_DAYS=90
export LOADER_HOUSEKEEPING_BATCH_SIZE=5000
export LOADER_HOUSEKEEPING_BATCH_PAUSE_MS=100
# Health monitoring (all optional; sensible defaults shown)
export LOADER_HEALTH_ENABLED=true
export LOADER_HEALTH_ADDR=127.0.0.1:9090
# Metadata reload (all optional; sensible defaults shown)
export LOADER_RELOAD_ENABLED=true
export LOADER_RELOAD_INTERVAL_SEC=60     # 0 to disable periodic reload
export LOADER_RELOAD_ON_SIGHUP=true
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


## SQL Source Module

## Design Notes
 - **Why strings for watermark values?** It keeps the schema simple and portable across watermark types (datetime, bigint, decimal). The WatermarkType on Load tells the source module how to parse/compare it. Trade-off: slight parsing overhead per run — negligible.
 - **Why ParamsJSON instead of columns per source type?** Each source module needs different config (SQL: server/db/user; Modbus: host/port/unit-id; OPC-UA: endpoint/security policy). JSON keeps the schema stable while allowing modules to evolve independently. The module owns the schema of its params.
 - **Why a separate Watermark table (not a column on Load)?** Cleaner audit trail (previous value, reset time, reset reason) and it separates config from runtime state. Config is user-owned; watermark is loader-owned.
 - **Why RunUUID in addition to ID?** The UUID is used for log correlation and will be safe to sync to the cloud later without primary-key collisions.
 - **Non-blocking DB writes.** If the metadata DB is slow or briefly down, log records queue in a bounded channel. Once full, new records drop (console still gets them). This prevents logging from ever stalling a load.
 - **Repository.Log was left in place.** Nothing that used it before breaks. But new code should prefer slog — cleaner and gives you dual output for free.
 - **Well-known attribute promotion**. Keys run_uuid, load_id, source_id, category map to typed columns in meta_log_entries. Everything else falls into details as JSON. This keeps queries fast without giving up flexibility.
 - **No third-party log library.** slog is standard, fast, and won't be abandoned.

## What's missing intentionally (keeping v1 lean):
 - No retry policy table yet — add later.
 - No schema drift tracking — AutoCreateTable only.
 - No secrets manager integration — start with env-var references in ParamsJSON (e.g. {"password": "$env:SRC1\_PW"}).


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

## OPC-UA Source Loader
### Design Notes Worth Understanding
 - **Why batch reads instead of per-node calls?** OPC-UA's Read service accepts N nodes in one round-trip. For 100 tags at 5-second polling, this is the difference between 100 network round-trips every 5 seconds and 1. The library handles this natively and the server does too.
 - **Why prefer SourceTimestamp over the poll time?** When a well-behaved OPC-UA server reports SourceTimestamp, that's the actual moment the sensor produced the value — sometimes seconds before the poll if the value hasn't changed. This is far more accurate than "when I read it". If the server doesn't provide one, we fall back to ServerTimestamp, then to poll time.
 - **Why qualityFromStatus and not just dv.Status == StatusOK?** OPC-UA status codes are bitfields. A value can be "uncertain" (which is neither good nor bad — sensor is degraded but reporting) or have specific sub-flags. Mapping to three coarse buckets (good/uncertain/bad) keeps queries simple while preserving the useful distinction.
 - **Why generic assignValue with a type switch?** OPC-UA carries far more numeric types than SQL (Byte, SByte, Int16, UInt64, Float, Double, etc.). Rather than one column per type, we widen all numerics to float64. This matches the Modbus module and gives you a single value_num column downstream. If you ever have a tag that needs exact 64-bit integer precision (rare in industrial telemetry), we'd add a value_int column later.
 - **Why lock a mutex around reads?** Same reason as Modbus — the underlying session is transactional. The gopcua library is safe for concurrent use, but serializing reads-per-source keeps behavior predictable and matches how Modbus works. Under the scheduler's per-load lock, contention is zero anyway.
 - **Why not implement certificate auth yet?** It requires generating an application instance certificate, persisting it, and having the server trust it (usually a manual step in the server's admin UI). It's a real deployment workflow, not just a config change. Worth doing separately when a specific site needs it.
### Testing Checklist
1. **Free public test server:** opc.tcp://opcuademo.prosysopc.com:53530/OPCUA/SimulationServer — anonymous, no security. Great for smoke-testing.
2. **Start with Anonymous / None / None** to confirm connectivity. Read ns=3;i=1001 (a simulated Counter on Prosys) as a first tag.
3. **Escalate to username auth** against your actual SCADA server.
4. **Escalate to Basic256Sha256 + SignAndEncrypt** last — this is where cert issues surface.
5. **Kill the OPC-UA server mid-poll**. Confirm the next tick emits bad rows and the loader recovers on the tick after restart.
6. **Poll an unknown node id (typo it)**. Confirm you get a bad row with the OPC-UA status code in value_text — not a crashed load.
### Known Limitations (v1, deliberate)
 - **No auto-reconnect on session expiry**. If the SCADA server closes an idle session, the next tick's Read fails, and the tick after that will try again — but the client won't auto-reopen. If you have long polling intervals (>60s) or a server with an aggressive session timeout, this could be an issue. Fix would be catching BadSessionIDInvalid and reopening. Add when needed.
 - **No certificate auth**. As above.
 - **No browsing**. You must know your node IDs. In practice mine operators use UaExpert or similar to discover them once and paste them into the seed file.
 - **All numeric types widened to float64**. Lossy for 64-bit integers with more than ~15 significant digits. Not a real problem for sensor data.
 - **Arrays and complex types stringified**. Fine for the occasional string array; not appropriate for streaming structured data. If you need to unpack a Struct node, that's a bespoke decoder per struct type.

## Health Monitor
Use it like so
```
$ curl -s http://127.0.0.1:9090/healthz
{
  "status": "ok",
  "uptime_seconds": 143
}

$ curl -s http://127.0.0.1:9090/readyz
{
  "metadata": "ok",
  "sink": "ok",
  "status": "ok"
}

$ curl -s http://127.0.0.1:9090/status | jq
{
  "version": "dev",
  "go_version": "go1.22.3",
  "started_at": "2024-06-10T08:15:22Z",
  "uptime_seconds": 143,
  "summary": {
    "total_loads": 3,
    "running": 0,
    "last_success": 2,
    "last_failed": 1
  },
  "loads": [
    {
      "load_id": 1,
      "load_name": "erp.orders",
      "source_name": "erp-prod",
      "source_kind": "sqlserver",
      "cron_expression": "@every 5m",
      "last_run_uuid": "7c1e...",
      "last_started_at": "2024-06-10T08:17:00Z",
      "last_finished_at": "2024-06-10T08:17:01Z",
      "last_status": "success",
      "last_rows_read": 42,
      "last_rows_written": 42,
      "currently_running": false
    },
    {
      "load_id": 2,
      "load_name": "crusher.telemetry",
      "source_name": "crusher-plc",
      "source_kind": "modbus",
      "cron_expression": "@every 10s",
      "last_status": "failed",
      "last_error": "open modbus client: dial tcp 10.0.10.55:502: i/o timeout",
      "currently_running": false
    }
  ]
}
```

### systemd Integration
Now that the endpoint exists, your unit file can use it. Example `/etc/systemd/system/data-loader.service`:
```
[Unit]
Description=Mine Data Loader
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=data-loader
EnvironmentFile=/etc/data-loader/environment
ExecStart=/opt/data-loader/loader
Restart=on-failure
RestartSec=10

# Health check via systemd watchdog is out of scope here, but
# an external monitoring probe on http://127.0.0.1:9090/readyz
# is the intended integration.

[Install]
WantedBy=multi-user.target
```
For monitoring integration (Prometheus Blackbox, uptime-kuma, etc.), point at /readyz and treat any non-200 as an alert.

## Metadata Hot-Reload
Adding a new load
```
# Seed the new load via your seed CLI or SQL
./seed --file new-load.yaml

# Trigger reload immediately (optional — periodic will catch it within 60s)
sudo systemctl kill --signal=SIGHUP data-loader.service
# or:
kill -HUP $(pgrep data-loader)
```

Expected log line within seconds:
```
{"level":"INFO","msg":"SIGHUP received — reloading","category":"schedule"}
{"level":"INFO","msg":"metadata reload complete","category":"schedule","total_loads":4,"added":[7],"removed":[],"updated":[]}
```
Disabling a load:
```
UPDATE meta_loads SET is_enabled = 0 WHERE name = 'crusher.telemetry';
```
Next reload:
```
{"level":"INFO","msg":"metadata reload complete","category":"schedule","total_loads":3,"added":[],"removed":[2],"updated":[]}
```
Changing a schedule:
```
UPDATE meta_loads SET cron_expression = '@every 30s' WHERE name = 'mill.telemetry';
```
next reload
```
{"level":"INFO","msg":"metadata reload complete","category":"schedule","total_loads":3,"added":[],"removed":[],"updated":[3]}
```
