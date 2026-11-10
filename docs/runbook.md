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

## Historic backfill (ADR-022)

Opt-in dual-cursor fill for SQL `delta` loads. Prefer this over a manual rewind when you need history **and** live catch-up at the same time.

1. On the **parent** load set `backfill_required = 1` and optionally `backfill_stop` (exclusive lower bound; omit to resolve `MIN(watermark_column)` once). Seed YAML: `backfill_required: true`, `backfill_stop: "…"`.
2. Run `loader migrate up` if needed (migration `00003_backfill_columns`), then HUP / wait for reload.
3. After the parent’s first-run watermark init, the coordinator creates `{name}#backfill`, freezes `backfill_ceiling`, seeds the sibling watermark at the stop, and enables it (cron ≈ parent + 1 minute for 5-field expressions).
4. Monitor sibling runs in `meta_load_runs` / `/status`. Each tick loads at most `batch_size` rows under the ceiling.
5. When the sibling cursor reaches the ceiling it is disabled; parent gets `backfill_completed_at` and `backfill_required = 0`.

**Cancel:** `UPDATE meta_loads SET is_enabled = 0 WHERE backfill_of_load_id = <parent_id>;` and clear `backfill_required` on the parent.

**Re-entry:** set a new `backfill_stop` and `backfill_required = 1` on the parent (explicit only).

**Source pool (ADR-021):** parent and sibling share `source_id` and serialize on the pool lease while a run holds it.

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
