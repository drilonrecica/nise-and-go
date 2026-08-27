# Dependency Allowlist

This page is the first dependency allowlist for Nise (see
[ADR 0008](adr/0008-toolchain-and-dependencies.md)). It governs code Nise
itself maintains: `cmd/`, `internal/`, `runtime/`, and `modules/`. It does not
govern application code an owner writes after generation — see
[repository layout](repository-layout.md) and blueprint §5's escape-hatch
notes for that boundary.

## The rule

- Standard library first. A dependency is added only when it provides a
  specific, documented benefit the standard library cannot provide at
  reasonable effort.
- Convenience wrappers over trivial standard-library behavior are rejected,
  regardless of popularity.
- For M0, M1, and M2 the target is **zero runtime dependencies**:
  `cmd/`, `internal/`, and `runtime/` compile against the Go standard library
  only. Tool-only dependencies (linters, generators) are allowed through
  `go.mod` `tool` directives and are not runtime dependencies.
- Nise does not use a CLI framework (no cobra, urfave/cli, or kong). Command
  dispatch is hand-written over the standard library's `flag` package.

## Process

- **Adding** a framework dependency requires an entry in the table below
  (import path, layer, reason, removal condition) plus a line in the commit
  message that adds it, naming the dependency and the reason.
- **Removing** a dependency requires the same: update this page and say so in
  the commit message.
- A dependency change is a Critical review finding if this page was not
  updated in the same change.

## Currently allowed dependencies (Nise-maintained code)

As of this task, `cmd/`, `internal/`, and `runtime/` contain no Go source
files yet (only placeholder `README.md` files), so there is nothing for them
to import. The table records the dependency graph that already exists in
`go.mod` for other reasons.

| Import path | Layer | Why it is allowed | What would remove it |
|---|---|---|---|
| `github.com/sqlc-dev/sqlc/cmd/sqlc` | Framework tool (`tool` directive) | Generates type-safe Go from SQL for the generated application's data layer; sqlc is the database default (`DECISIONS.md`). Never imported by `runtime/`, `internal/`, or `cmd/` — invoked only as `go tool sqlc`. | Nise stops using sqlc as the generated-application query generator. |
| `github.com/pressly/goose/v3/cmd/goose` | Framework tool (`tool` directive) | Runs and generates handwritten SQL migrations; goose is the migration runner (`DECISIONS.md`). Never imported by `runtime/`, `internal/`, or `cmd/` — invoked only as `go tool goose`. | Nise stops using goose as the migration runner. |
| `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen` | Framework tool (`tool` directive) | Generates the strict chi server and client stubs from the authoritative OpenAPI document (`DECISIONS.md`, blueprint §8). Never imported by `runtime/`, `internal/`, or `cmd/` — invoked only as `go tool oapi-codegen`. | Nise stops treating OpenAPI as authoritative for generated server/client code. |

The remaining 101 entries in `go.mod`'s `require (... // indirect)` block
are transitive dependencies of the three tools above (for example
`github.com/jackc/pgx/v5`, `google.golang.org/grpc`, `modernc.org/sqlite`),
pulled in because `go mod tidy` records a tool's full build graph. They are
not individually vetted or imported by Nise-maintained packages; they exist
solely so `go build ./...` and `go tool <name>` can build the pinned tool
binaries reproducibly. `golangci-lint` was evaluated as a fourth `tool`
directive and rejected because of the dependency-graph weight it would add;
see [toolchain.md](toolchain.md) for the measured evidence and the version-
pin alternative used instead.

**Framework runtime layer:** none. Zero runtime dependencies is the explicit
target for M0, M1, and M2 (see the rule above).

**Framework tool layer:** the three `tool` directives listed above, plus
`golangci-lint` pinned by version rather than by `tool` directive.

## Generated-application allowlist

This is the dependency set Nise generates into a new project's `go.mod` and
`package.json`, derived from `DECISIONS.md`. It governs generated and
Nise-maintained templates, not ad hoc application choices — an application
owner may add further dependencies directly; the Nise allowlist does not
gate them (blueprint §5, "Applications may directly add dependencies Nise
does not use").

Versions are not recorded here where this task has not verified a released
version; those entries name the package only.

### Go (generated application)

| Import path | Why it is allowed |
|---|---|
| `github.com/go-chi/chi/v5` | The HTTP router (`DECISIONS.md`: "V1: Go + chi + PostgreSQL + SvelteKit"). |
| `github.com/jackc/pgx/v5` | The PostgreSQL driver underlying sqlc-generated code and the exceptional direct-pgx escape hatch (blueprint §5, §7). |
| `github.com/riverqueue/river` | The background-job runner, used transactionally through pgx (`DECISIONS.md`: "River through pgx"). |
| sqlc-generated query code | Output of `sqlc generate`; application-owned, checked in. |
| goose migration files and runtime support | Handwritten SQL migrations run by goose (`DECISIONS.md`). |
| oapi-codegen-generated server/client code | Output of `oapi-codegen` from the authoritative OpenAPI document; application-owned, checked in. |

Version numbers for the above are not yet verified against a specific
released tag and are intentionally omitted; a later task that first
generates a project must pin and record them here.

### Frontend (generated application)

Named without a version — none has been verified yet:

| Package | Why it is allowed |
|---|---|
| SvelteKit | The generated frontend framework (`DECISIONS.md`, blueprint §10). |
| Tailwind CSS v4 | The generated styling system (`DECISIONS.md`). |
| Valibot | Schema validation paired with the generated API client (`DECISIONS.md`). |
| TanStack Table | The table primitive behind the generated resource-table wrapper (`DECISIONS.md`). |
| Paraglide | Compile-time i18n for the generated frontend (`DECISIONS.md`). |
| openapi-typescript | Generates the typed frontend API client from the authoritative OpenAPI document (`DECISIONS.md`, blueprint §8). |
| Vitest | Svelte browser-component tests (blueprint §13). |
| Playwright | The deliberately small end-to-end suite for critical user journeys (blueprint §13). |

## Denylist

Restating blueprint non-goals: these are explicitly rejected for
Nise-maintained code and for what Nise generates by default. An application
owner remains free to add any of these to their own generated project.

- No generic cache abstraction (for example a Redis-backed cache layer as a
  framework default).
- No message broker (jobs run transactionally through PostgreSQL/River, not
  a separate broker).
- No runtime plugin system or dynamic generator/template overlay API in V1.
- No reflection-based dependency-injection container or service locator.
- No schema DSL or ORM (sqlc generates from SQL; no query-builder or
  active-record layer).
- No mandatory OpenTelemetry collector (tracing is optional, not a required
  dependency).
- No CLI framework (no cobra, urfave/cli, or kong; hand-written `flag`
  dispatch).
- No AI/BYOK/model-provider SDK anywhere in Nise.

## Verification

`go mod verify` reports `all modules verified` for every dependency
currently recorded in `go.sum` (see the report for this task for the exact
command output).
