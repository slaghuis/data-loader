# Operations runbook — data-loader

## Stuck runs (`status = running`)

On startup and shutdown the scheduler calls `AbortRunningRuns`, so orphaned `running` rows should clear automatically. If `/status` still shows `currently_running: true` after a crash:

```sql
UPDATE meta_load_runs
SET status = 'aborted',
    finished_at = SYSUTCDATETIME(),
    error_message = 'manual abort'
WHERE status = 'running';
```

Then `kill -HUP` the process (or wait for the reload interval) and confirm `/status`.

## Watermark rewind

To re-load from an earlier point (datetime or numeric string as stored):

```sql
UPDATE meta_watermarks
SET previous_value = value,
    value = N'<earlier-value>',
    reset_at = SYSUTCDATETIME(),
    reset_reason = N'ops rewind'
WHERE load_id = <id>;
```

Do not delete the watermark row unless you intend a first-run init (seed `MAX`, load no rows).

## Adding a site / load

1. Add YAML under `seeds/` using `is_enabled:` (not `enabled:`).
2. `go run ./cmd/seed -file seeds/<file>.yaml`
3. Ensure `$env:` secrets exist in the process environment.
4. `kill -HUP $(pgrep loader)` or wait for periodic reload.
5. Confirm the load appears under `/status` and that `/metrics` shows runs.

## Rotating secrets

1. Update the environment (systemd `EnvironmentFile`, k8s Secret) with the new password.
2. Restart the process (param resolution happens at run start; a HUP alone is enough only if the env already changed in-process — prefer restart).
3. Confirm the next run succeeds; failed connects appear in `meta_log_entries` with `category=connect`.

## Migrations

Schema is goose-managed (ADR-012). Before starting a new binary:

```bash
./loader migrate status
./loader migrate up
```

Existing AutoMigrate databases are stamped to version 1 on first `up` when `meta_sources` already exists.

## Health probes

| Path | Use |
|------|-----|
| `/healthz` | liveness |
| `/readyz` | readiness (metadata + sink ping) |
| `/status` | per-load JSON for humans |
| `/metrics` | Prometheus scrape |
