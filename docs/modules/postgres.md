# Postgres source module

Reads tables/views from PostgreSQL via pgx. Supports `append`, `replace`, and `delta`.

## Behaviour

- Fully qualify `object_name` (`schema.table`) to avoid `search_path` surprises.
- `NUMERIC`/`JSONB` land as text in the MSSQL sink; timestamps are normalized to UTC.
- Delta uses the same upper-bound snapshot pattern as SQL Server (`> $1 AND <= $2`).
- Historic backfill siblings (ADR-022) clamp to `WatermarkCeiling` and apply `LIMIT batch_size` per tick.

## Params

`host`, `port`, `database`, `user`, `password`, optional `sslmode`, `search_path`, `application_name`.

## Testing checklist

Connectivity (append), delta after insert, type mapping (`NUMERIC`, `UUID`, `JSONB`, `TIMESTAMPTZ`, `BOOLEAN`), and `sslmode=require`.
