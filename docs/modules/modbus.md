# Modbus TCP source module

Polls PLC/gateway tags into the narrow telemetry schema (ADR-007). Mode is forced to `append`.

## Design notes

- One poll cycle = one batch (all tags).
- Decode locally so endian/word-order and quality=`bad` rows stay testable.
- Client access is mutex-serialized; the scheduler already prevents overlapping runs per load.
- Tag lists come from `contracts.TelemetryCatalog` injected on `SourceConfig` (no package globals).

## Limitations (v1)

No block-read coalescing, no in-place reconnect (next tick reopens), no per-tag schedule.
