# Agent instructions — data-loader

Read these before changing code:

1. [`docs/04-ai-rules.md`](docs/04-ai-rules.md): mandatory coding, architecture, testing and documentation rules.
2. [`docs/05-decisions.md`](docs/05-decisions.md): ADRs. Do not contradict an Accepted ADR. Draft a Proposed ADR for any new architectural choice and stop for review.
3. [`docs/03-implementation-plan.md`](docs/03-implementation-plan.md): current phase, checklist and the **defect register** (D-xx). Reference defect IDs in tests and commits.
4. [`docs/02-architecture.md`](docs/02-architecture.md): layering, data model, API and configuration. Update it in the same change as the code.

Non-negotiables:

- Sources are read-only. Never write to source databases, PLCs or OT devices.
- No secrets in code, logs, fixtures or API responses.
- Use `gofmt` (LF line endings) and `golangci-lint`, and run `go test -race ./...` before declaring work done.
