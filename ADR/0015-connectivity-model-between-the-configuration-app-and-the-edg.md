---
id: ADR-015
title: "Connectivity model between the configuration app and the edge loader"
status: Proposed
date: 2026-10-14
tags: [networking, security, deployment, ot, decision-required]
---
# ADR-015: Connectivity model between the configuration app and the edge loader

## Status

Proposed — **decision required from site OT security and the platform team**

## Date

2026-10-14

## Context

The loader sits on, or next to, the mine's OT network. Industrial security practice (IEC 62443 zones and conduits, the Purdue model) generally forbids inbound connections from enterprise or cloud networks into OT zones. The Phase 3 brief assumes the configuration app *calls* the loader's API. The original README instead envisaged the loader *pulling* metadata from the cloud. These two models conflict.

## Decision

Recommended, pending approval:

1. Build the API exactly as in ADR-013. The contract is the same in every option below.
2. Per site, deploy using one of:
   - **A — DMZ exposure.** The loader (or a reverse proxy) is in the site DMZ or Level 3.5, and the configuration app reaches it over approved HTTPS. This is the simplest option where OT security permits it.
   - **B — Outbound-only relay.** The loader opens an outbound connection (for example Azure Relay Hybrid Connections or an equivalent reverse tunnel), and the configuration app calls the API through it. No inbound firewall rules.
3. A pull model (the loader polls the cloud for its desired configuration) is **not** chosen for Phase 3, because discovery and preview are interactive request/response operations. It remains a backlog option for the configuration CRUD part.

## Alternatives Considered

- Inbound access straight into the OT network: rejected on security grounds.
- Pull-only desired state: cannot support interactive discovery or preview.

## Consequences

Positive:

- Development is unblocked. Per-site flexibility. Preserves OT segmentation.

Negative:

- Option B adds a cloud dependency (the relay) and an extra component to operate.
- This decision must be made before the first production deployment of Phase 3.
