# Edge Data Loader

## Documentation
The formal documentation set lives in [`docs/`](docs/README.md):
[requirements](docs/01-project-requirements.md) · [architecture](docs/02-architecture.md) · [implementation plan and defect register](docs/03-implementation-plan.md) · [AI rules](docs/04-ai-rules.md) · [decisions (ADRs)](docs/05-decisions.md).
Phases 1–2 are operationally complete (defects closed, goose migrations, `/metrics`, atomic replace, opt-in source pool). Phase 2.1 is a short deferred backlog (integration/coverage gates). Phase 3 adds a remote configuration API; Phase 4 adds multiple sinks, including Microsoft Fabric.

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
Phase 3 remote configuration API (after ADR-014/015/016), or Phase 2.1 integration/coverage gates if you want stricter CI first.

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
An automated seeding tool has been built to import a YAML file.  Some examples are included in the seeds directory.

```
export LOADER_METADATA_DSN='sqlserver://user:pw@localhost?database=DataLoaderMeta'
go run ./cmd/seed -file seeds/seed.yaml
```

This gives you a reusable seeding mechanism where new sources and loads can be added simply by editing YAML instead of changing Go code.

Remember, once you have seeded, 
`go run ./cmd/seed -file seeds/mes-postgres.yaml`
you might have to add an environment variable with the password.
`export MES_PW='the-actual-password'`


## Source modules

Module design notes, checklists and limitations live under [`docs/modules/`](docs/modules/):

- [SQL Server](docs/modules/sqlserver.md)
- [Postgres](docs/modules/postgres.md)
- [Modbus TCP](docs/modules/modbus.md)
- [OPC UA](docs/modules/opcua.md)

Operational procedures (stuck runs, watermark rewind, secrets): [`docs/runbook.md`](docs/runbook.md).

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

$ curl -s http://127.0.0.1:9090/metrics | head
# HELP loader_runs_total …
# TYPE loader_runs_total counter

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
