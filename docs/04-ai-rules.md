# AI Coding Instructions

| Item | Value |
|------|-------|
| Document | 04 – AI Rules |
| Version | 1.0 |
| Last updated | 2026-10-14 |
| Applies to | All code changes from Phase 2 onwards, whether written by a person or an AI agent |
| Related | [02 Architecture](02-architecture.md) · [05 Decisions](05-decisions.md) · [`AGENTS.md`](../AGENTS.md) |

These rules are mandatory. If a rule conflicts with an Accepted ADR, the ADR wins. Raise the conflict and propose an ADR update rather than ignoring either.

## Code Standards

- Write production-quality Go that targets the version in `go.mod` (currently 1.26.9).
- Follow language best practices: *Effective Go*, the Go Code Review Comments, and the standard-library idioms.
- Code must pass `gofmt` (LF line endings, enforced through `.gitattributes`) and `golangci-lint run` with the repository configuration.
- Keep functions small: at most about 50 lines and cyclomatic complexity ≤ 15. Split orchestration (for example `Runner.Execute`) into named steps.
- Use meaningful names. Package names are short, lowercase and singular. Avoid stutter (`sink.MSSQL`, not `sink.MSSQLSink`, in new code). Initialisms are all caps (`ID`, `URL`, `OPCUA`).
- No dead code. Delete unused functions; do not comment them out.
- No hardcoded secrets, hosts or site names. Use metadata, environment variables, or `$env:` / `$secret:` references.
- `context.Context` is the first parameter of every function that does I/O or may block. Never store a context in a struct.
- Use `init()` only for source and sink self-registration. No other side effects.
- No package-level mutable state apart from the registries (which are mutex-guarded).
- Every goroutine has an owner, a stop signal, and a `WaitGroup` or equivalent. No goroutines that are started and forgotten.
- Identifiers in SQL go through the quoting helpers (`quoteMSSQLIdent`, `quoteIdent`) **and** the identifier validator. Values are always bind parameters. Never format a value into SQL.
- Log with `log/slog` only, take the logger from `logging.FromContext(ctx)`, always include a `category`, and use snake_case keys. Never log secrets, parameter values, or full row data.

## Architecture Rules

- Follow Clean Architecture (ADR-001). Allowed import directions:

| Package group | May import | Must not import |
|---------------|-----------|-----------------|
| `pkg/contracts` (domain) | stdlib only | anything in `internal/` |
| `internal/sources/*`, `internal/sink/*` (adapters) | `pkg/contracts`, `internal/logging`, drivers | `internal/metadata`, `internal/pipeline`, `internal/scheduler`, `internal/api` |
| `internal/pipeline`, `scheduler`, `discovery`, `delivery` (application) | `pkg/contracts`, small interfaces they define themselves | concrete adapters, drivers, GORM |
| `internal/metadata` (infrastructure) | GORM, `pkg/contracts` | application packages |
| `internal/api`, `internal/health` (presentation) | application services via interfaces | drivers, GORM, adapters |
| `cmd/*` | everything (wiring only) | — |

- Use dependency injection: constructors receive their collaborators. Only `cmd/*/main.go` constructs concrete infrastructure.
- Use interfaces for external dependencies. Define them **in the consuming package**, keep them as narrow as the consumer needs, and have constructors return concrete types.
- Separate domain logic from infrastructure. Watermark rules, mode normalisation and preview limits live in pure, tested functions.
- Add a new source or sink kind by adding a package that self-registers, plus a blank import in `cmd/loader`. No `switch kind` statements in the engine.
- New optional capabilities (discovery, preview, flush) are separate interfaces detected with type assertions, not methods added to `Source` or `Sink` that every module must stub.
- Sources are **read-only** (Business Rule 8). Code review rejects any Modbus write function code, OPC UA Write/Call, or DML/DDL against a source.

## Error Handling

- Return meaningful errors. Wrap with `fmt.Errorf("<operation> <object>: %w", err)`, for example `open sqlserver source: …`.
- Use sentinel errors or typed errors for conditions that callers branch on, and check them with `errors.Is` / `errors.As`. Never compare error strings.
- Log failures with context at the point where they are **handled**, not at every level they pass through. The runner logs; sources return.
- Avoid swallowing errors. `_ = x.Close()` is allowed only for best-effort cleanup on a path that has already failed, with a comment if it isn't obvious.
- Never `panic` in library code. `main` may exit non-zero on startup failures.
- Errors returned by the API are sanitised: no DSNs, secrets or stack traces. Use problem+json with a `request_id` (see [02 – API](02-architecture.md#api-requirements)).
- A failure to persist audit state (run status, watermark) **fails the run** rather than being logged and ignored (see D-04).

## Testing Rules

- Create unit tests for every change. A bug fix starts with a failing regression test that names the defect ID, for example `TestDBHandler_WithAttrs_PersistsLoadID_D01`.
- Use table-driven tests, the stdlib `testing` package, and `github.com/google/go-cmp/cmp` for diffs. No assertion frameworks.
- Mock external dependencies with **hand-written fakes** in `internal/testutil` that implement the narrow interfaces. No generated mocks.
- Cover error paths: every `if err != nil` branch on a business path has a test that triggers it through a fake.
- Integration tests use build tag `integration` and `testcontainers-go`, live under `test/integration/` or next to the adapter, and clean up after themselves.
- Tests are deterministic. Inject clocks, avoid `time.Sleep` (poll with a deadline instead), and use no network access outside containers.
- `go test -race ./...` must pass. Coverage is ≥ 80% per package, excluding `cmd/` (CI gate in `pipeline.yaml`).
- Add fuzz tests (`testing.F`) for parsers and anything that touches SQL identifiers.

## Documentation Rules

- Document public interfaces. Every exported identifier has a godoc comment that starts with its name, and every package has a `doc.go` stating its responsibility and invariants.
- Explain complex logic: GUID byte swapping, watermark snapshot ordering, Modbus word order. Explain *why*, not *what*.
- Update `README.md` and the relevant `docs/` file **in the same change** as the code: new environment variables go in 02 Configuration, new endpoints in 02 API, new tables in 02 Data Model, and closed defects in 03.
- Record architectural decisions as ADR files in [`ADR/`](../ADR/) (format and index in [05 Decisions](05-decisions.md)) **before** implementing: new dependencies, protocols, storage, auth, deployment, or cross-cutting concerns.
- Each module that has non-obvious operational behaviour gets `docs/modules/<module>.md`.

## Security Rules

- Credentials are referenced, never inlined (`$env:` today, `$secret:` from Phase 3). Never echo them in logs, errors or API responses.
- Validate and quote all identifiers that come from metadata or the API. Clamp all limits (preview ≤ 10 rows, page size ≤ 500).
- New network listeners default to `127.0.0.1` and require TLS and authentication when bound elsewhere.
- Add dependencies only with justification in the PR description. `govulncheck` and `trivy` must be clean (CI gate).

## Workflow for AI agents

1. Before changing code, search for existing implementations (code-search MCP `search_code` / `find_similar_function`, or `rg`). Do not duplicate helpers.
2. Before any architectural change, check the ADRs: use the ADR MCP server (`find_decision`, `list_adrs`) or read [`ADR/`](../ADR/). If no ADR covers the change, draft one in `ADR/` with status *Proposed* and stop for review.
3. Keep changes small and focused: one concern per commit, and formatting-only changes in separate commits.
4. Run `gofmt`, `golangci-lint run` and `go test -race ./...` before declaring a change done. For adapter changes, also run `go test -tags integration ./...`.
5. State assumptions explicitly in the PR description and in the relevant docs.

## Definition of Done

- [ ] The code follows this document. Lint and format are clean.
- [ ] Unit tests (including error paths) are added. Coverage gate passes. Race detector is clean.
- [ ] Integration tests are added or updated for adapter changes.
- [ ] Godoc and `docs/` are updated. An ADR is added or updated if the change is architectural.
- [ ] The defect register in 03 is updated if a defect was fixed or found.
- [ ] No secrets in code, logs, fixtures or commit history.

---

# Output Required From AI

When asked to design or implement a feature, provide the following in order:

1. Solution overview, including the requirement IDs it satisfies (FR-xxx, BR-xxx).
2. Architecture diagram (Mermaid). Update [02](02-architecture.md) if the component structure changes.
3. Folder structure changes.
4. Database schema changes, as a goose migration (up and down).
5. API design changes, as an `openapi.yaml` diff plus examples.
6. Interface definitions (Go), placed in the consuming package.
7. Implementation plan as a checklist in [03](03-implementation-plan.md).
8. Source code.
9. Unit tests, plus integration tests for adapters.
10. Deployment instructions, including new environment variables, migration steps and rollback.

Do not omit implementation details.
Minimise assumptions.
Explicitly document any assumptions made, in the PR and in the relevant requirements section.
