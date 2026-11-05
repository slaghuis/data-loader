# SQL Server source module

Reads tables/views from Microsoft SQL Server. Supports `append`, `replace`, and `delta` modes.

## Watermarks

- Values are stored as strings in `meta_watermarks`.
- Delta queries snapshot `MAX(column)` before reading and use `> last AND <= upper` so mid-load inserts are deferred to the next run.
- First run with an empty watermark seeds `MAX` and reads no rows.

## Params

`server`, `port`, `database`, `user`, `password` (often `$env:…`), optional `encrypt`, `trustservercertificate`.

## See also

[ADR-006](../../ADR/0006-watermark-model-string-values-separate-table-first-run-initi.md)
