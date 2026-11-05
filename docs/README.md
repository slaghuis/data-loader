# Edge Data Loader — documentation

| # | Document | What it answers |
|---|----------|-----------------|
| 01 | [Project Requirements](01-project-requirements.md) | Why the loader exists; scope; business, functional and non-functional requirements by phase; assumptions |
| 02 | [Architecture](02-architecture.md) | How it is built: layers, Mermaid diagrams, data model, API (current and Phase 3), interfaces, configuration, logging, monitoring, testing, deployment |
| 03 | [Implementation Plan](03-implementation-plan.md) | What is done (Phases 1–2), Phase 2.1 backlog, Phases 3–4, and the **defect register** |
| 04 | [AI Rules](04-ai-rules.md) | Mandatory engineering rules for people and AI agents; Definition of Done |
| 05 | [Decisions](05-decisions.md) | Index of the Architectural Decision Records (ADR-001 to ADR-021), which live one per file in [`ADR/`](../ADR/) |

Phase status (2026-11-05): **Phases 1 and 2 operationally complete.** Phase 2.1 holds deferred CI/integration/coverage/godoc work. **Next:** Phase 3 (remote configuration API with schema discovery and top-10 preview), then Phase 4 (multiple sinks, including Microsoft Fabric).
