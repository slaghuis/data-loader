---
id: ADR-014
title: "API authentication: Entra ID JWT with scopes, optional mTLS"
status: Proposed
date: 2026-10-14
tags: [api, auth, security]
---
# ADR-014: API authentication: Entra ID JWT with scopes, optional mTLS

## Status

Proposed

## Date

2026-10-14

## Context

The API can change what data leaves the mine and can expose real business data through preview. Microsoft Entra ID is assumed to be the corporate identity provider.

## Decision

- Bearer access tokens issued by Entra ID (client-credentials grant for the configuration app's backend, or delegated tokens for user-attributed audit).
- Validate the issuer, audience, expiry and signature using a cached JWKS that keeps working through short WAN outages.
- Scopes and app roles: `config:read`, `config:write`, `sources:preview`, `ops:run`, `ops:watermark`. Audit records use the token's `oid` / `appid`.
- Optional mutual TLS (`LOADER_API_CLIENT_CA_FILE`) as defence in depth on site networks.
- `apikey` mode exists only for development and refuses to start unless bound to loopback.

## Alternatives Considered

- Static API keys in production (no rotation story, no identity in the audit trail).
- mTLS only (no per-user authorisation).
- Basic auth (rejected).

## Consequences

Positive:

- Central identity, per-scope least privilege, attributable audit.

Negative:

- Needs outbound HTTPS to Entra ID for key refresh. Initial setup requires an app registration.
