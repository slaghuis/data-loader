# OPC UA source module

Batch-reads SCADA nodes into the narrow telemetry schema (ADR-007). Mode is forced to `append`.

## Design notes

- Prefers `SourceTimestamp`, then `ServerTimestamp`, then poll time.
- Status codes map to `good` / `uncertain` / `bad`.
- Numerics widen to `float64`; node lists come from injected `TelemetryCatalog`.

## Limitations (v1)

No certificate auth, no browse, no auto-reopen on session expiry (next tick fails then retries open).
