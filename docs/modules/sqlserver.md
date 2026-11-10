# SQL Server source module

Reads tables/views from Microsoft SQL Server. Supports `append`, `replace`, and `delta` modes.

## Watermarks

- Values are stored as strings in `meta_watermarks`.
- Delta queries snapshot `MAX(column)` before reading and use `> last AND <= upper` so mid-load inserts are deferred to the next run.
- First run with an empty watermark seeds `MAX` and reads no rows.
- Historic backfill siblings (ADR-022) pass a `WatermarkCeiling`: upper bound is `min(MAX, ceiling)` and `TOP (batch_size)` limits rows per tick; watermark advances by last row.

## Params

`server`, `port`, `database`, `user`, `password` (often `$env:…`), optional `encrypt`, `trustservercertificate`.

## See also

[ADR-006](../../ADR/0006-watermark-model-string-values-separate-table-first-run-initi.md) · [ADR-022](../../ADR/0022-opt-in-historic-backfill-for-delta-loads.md)
