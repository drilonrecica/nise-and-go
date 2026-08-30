# Dependency Allowlist

This page is the first dependency allowlist for Nise (see
[ADR 0008](adr/0008-toolchain-and-dependencies.md)). It governs code Nise
itself maintains: `cmd/`, `internal/`, `runtime/`, and `modules/`. It does not
govern application code an owner writes after generation — see
[repository layout](repository-layout.md) for that boundary. An application
owner may add any dependency they like; the Nise allowlist governs what Nise
itself maintains and what Nise generates by default, and nothing more.

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

The table records the dependency graph Nise itself maintains. Generated
applications have their separate allowlist below.

| Import path | Layer | Why it is allowed | What would remove it |
|---|---|---|---|
| `github.com/sqlc-dev/sqlc/cmd/sqlc` | Framework tool (`tool` directive) | Generates type-safe Go from SQL for the generated application's data layer. sqlc is the database default: queries are handwritten SQL colocated with the feature that owns them, and sqlc generates the typed Go for them, so there is no ORM, query builder, or schema DSL between the application and its SQL. Never imported by `runtime/`, `internal/`, or `cmd/` — invoked only as `go tool sqlc`. | Nise stops using sqlc as the generated-application query generator. |
| `github.com/pressly/goose/v3/cmd/goose` | Framework tool (`tool` directive) | Runs and generates handwritten SQL migrations. goose is the migration runner because migrations are primarily handwritten SQL, embedded in the application binary while staying readable SQL in the repository, and applied as an explicit deployment step rather than on startup. Never imported by `runtime/`, `internal/`, or `cmd/` — invoked only as `go tool goose`. | Nise stops using goose as the migration runner. |
| `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen` | Framework tool (`tool` directive) | Generates strict chi server bindings from the authoritative OpenAPI document. Never imported by `runtime/`, `internal/`, or `cmd/` — invoked only as `go tool oapi-codegen`. | Nise stops treating OpenAPI as authoritative for generated server code. |

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
`package.json`. It governs generated and Nise-maintained templates, not ad hoc
application choices: an application owner may add further dependencies
directly, and the Nise allowlist does not gate them. That is deliberate — a
generated application is an ordinary Go and Svelte project its owner is
expected to be able to change, extend, and eventually detach from Nise
entirely.

Versions are not recorded here where this task has not verified a released
version; those entries name the package only.

### Go (generated application)

| Import path | Why it is allowed |
|---|---|
| `github.com/go-chi/chi/v5` | The HTTP router. The V1 profile is Go + chi + PostgreSQL + SvelteKit; chi is a `net/http`-compatible router, so generated handlers stay ordinary `http.Handler`s and the middleware chain stays readable in application code. |
| `github.com/jackc/pgx/v5` (`v5.10.0`) | The PostgreSQL driver and pool underlying sqlc-generated code. It is also the documented escape hatch: exceptional bulk operations, PostgreSQL-specific behavior, and carefully optimized paths may use pgx directly, keeping the application's own transaction boundaries. The generated pool pins explicit limits rather than accepting pgx's CPU-dependent maximum. |
| `github.com/pressly/goose/v3` (`v3.27.3`, runtime plus generated-project `tool` directive) | Loads readable embedded SQL migrations through an instance-scoped provider. The generated migrator disables Goose's package-global registry, owns a dedicated pgx `database/sql` connection, and serializes explicit migration runs with PostgreSQL advisory locking. |
| `github.com/sqlc-dev/sqlc/cmd/sqlc` (`v1.31.1`, generated-project `tool` directive) | Generates each feature's typed `store/` package from its handwritten SQL and feature-local config. Generated commands pass `--no-remote`; no hosted sqlc service is required. |
| `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen` (`v2.8.0`, generated-project `tool` directive) | Generates checked-in strict chi bindings from `api/openapi.yaml`. The application-owned config enables models, chi server, and strict server only; `make api-check` proves the output byte-for-byte without mutating it. |
| `github.com/riverqueue/river` | The background-job runner, used transactionally through pgx. Jobs are PostgreSQL-backed so a job can be enqueued in the same transaction as the business change that requires it — which is why there is no separate message broker in the denylist below. |
| sqlc-generated query code | Output of `sqlc generate`; tool-owned complete `store/` directories, checked in and never hand-edited. |
| goose migration files and runtime support | Handwritten SQL migrations run by Goose v3.27.3, embedded in the binary and applied as an explicit deployment step. |
| oapi-codegen-generated server code | Complete output of pinned oapi-codegen from the authoritative OpenAPI document; Nise/tool-owned under `openapigen/`, checked in. |

Version numbers remain omitted only for dependencies whose implementing task
has not yet pinned and verified a released tag.

### Frontend (generated application)

Named without a version — none has been verified yet:

| Package | Why it is allowed |
|---|---|
| SvelteKit | The generated frontend framework. The authenticated application is built with adapter-static and embedded in the Go binary, so it ships as one artifact; protected routes are never prerendered and universal `load` functions run in the browser. |
| Tailwind CSS v4 | The generated styling system. Its output is an external stylesheet, which is what lets the Content Security Policy keep `style-src` free of `'unsafe-inline'` ([ADR 0013](adr/0013-security-headers-and-csp.md)). |
| Valibot | Frontend form validation and ergonomics, paired with the generated API client. Go remains authoritative: this is for the user's benefit in the browser, never the security boundary. |
| TanStack Table | The table primitive. It is wrapped behind Nise-owned Svelte 5 components rather than exposed directly, so a generated resource table has one shape an application can read and edit. |
| Paraglide | Compile-time internationalization for the generated frontend: messages compile to code, so unused translations do not ship. |
| openapi-typescript | Generates the frontend's types from the same authoritative OpenAPI document the Go server bindings come from, so client and server cannot drift. The client itself is a lightweight typed fetch wrapper, not a client framework. |
| Vitest | Svelte component tests, in browser mode, covering component behavior. |
| Playwright | A deliberately small end-to-end suite for critical user journeys — end-to-end tests are the slowest and most brittle layer, so they cover journeys, not coverage. |

## Denylist

These are explicitly rejected for Nise-maintained code and for what Nise
generates by default. An application
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
