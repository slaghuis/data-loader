---
id: ADR-016
title: "Secrets for remotely created sources"
status: Proposed
date: 2026-10-14
tags: [secrets, security, api]
---
# ADR-016: Secrets for remotely created sources

## Status

Proposed

## Date

2026-10-14

## Context

Under ADR-010, a new credential requires host access. Phase 3 lets a remote app create sources, so credentials must be settable through the API without being stored in plain text or ever read back.

## Decision

- Add a `$secret:NAME` reference type, backed by `meta_secrets`. Values are encrypted with AES-256-GCM, with a per-row nonce and a `key_id` to support rotation. The key comes from `LOADER_SECRETS_KEY_FILE` (file permission 0400), held outside the database.
- The API accepts secrets in a write-only `secrets` object, stores them as `$secret:<source-name>.<field>`, and returns only `{ "set": true }`.
- `$env:` remains supported. Optionally, a `$kv:` resolver (Azure Key Vault) with a local encrypted cache can be added for sites that standardise on Key Vault.
- Key rotation is done with `loader secrets rotate`.

## Alternatives Considered

- Azure Key Vault only: a WAN dependency at run time; edge loads would fail during outages.
- HashiCorp Vault: operational overhead per site.
- SQL Server Always Encrypted: ties secret handling to driver and column-master-key setup on each site.

## Consequences

Positive:

- Remote configuration works offline. Secrets are never in plain text at rest or in responses.

Negative:

- The loader owns key management. Loss of the key file means re-entering all `$secret:` credentials (document it in the runbook).
