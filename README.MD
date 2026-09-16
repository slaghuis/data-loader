# Edge Data Loader

## Design Notes
 - Why strings for watermark values? It keeps the schema simple and portable across watermark types (datetime, bigint, decimal). The WatermarkType on Load tells the source module how to parse/compare it. Trade-off: slight parsing overhead per run — negligible.
 - Why ParamsJSON instead of columns per source type? Each source module needs different config (SQL: server/db/user; Modbus: host/port/unit-id; OPC-UA: endpoint/security policy). JSON keeps the schema stable while allowing modules to evolve independently. The module owns the schema of its params.
 - Why a separate Watermark table (not a column on Load)? Cleaner audit trail (previous value, reset time, reset reason) and it separates config from runtime state. Config is user-owned; watermark is loader-owned.
 - Why RunUUID in addition to ID? The UUID is used for log correlation and will be safe to sync to the cloud later without primary-key collisions.
## What's missing intentionally (keeping v1 lean):
 - No retry policy table yet — add later.
 - No schema drift tracking — AutoCreateTable only.
 - No secrets manager integration — start with env-var references in ParamsJSON (e.g. {"password": "$env:SRC1\_PW"}).

## Run
```
export LOADER_METADATA_DSN='sqlserver://user:pw@localhost?database=DataLoaderMeta'
export LOADER_SINK_DSN='sqlserver://user:pw@localhost?database=DataLoaderSink'
export ERP_PW='actual-password'
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
