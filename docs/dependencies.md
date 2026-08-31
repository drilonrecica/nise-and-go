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
| `golang.org/x/crypto/argon2` (`v0.54.0`) | Framework runtime (`runtime/password` only) | Argon2id is not in the Go standard library, and a hand-written password KDF is exactly the class of code this project must not contain. `golang.org/x/crypto` is maintained by the Go team, was already in the module graph as a transitive dependency, and is imported by exactly one package. Nothing else in `runtime/` imports it. | The standard library gains Argon2id, or Nise stops using Argon2id for password hashing. |

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

**Framework runtime layer:** `golang.org/x/crypto/argon2`, imported by
`runtime/password` and by nothing else. Zero runtime dependencies was the
explicit target for M0, M1, and M2 and was met; M5 spends it exactly once, on
the one primitive the standard library does not provide and that must not be
written by hand. Every other `runtime/` package still compiles against the
standard library alone.

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
| `github.com/oapi-codegen/runtime` (`v1.7.0`) | The small runtime the generated bindings import to bind path and query parameters. It is not a framework choice: it is what the pinned generator's own output requires, and it enters the generated `go.mod` for the same reason that output does. It arrived with the first operation carrying a path parameter. |
| `golang.org/x/crypto` (`v0.54.0`) | Reached through `runtime/password`, which is where the generated application's Argon2id hashing lives. The application does not import it directly. |
| `github.com/riverqueue/river` (`v0.46.0`) | The background-job runner, used transactionally through pgx. Jobs are PostgreSQL-backed so a job can be enqueued in the same transaction as the business change that requires it — which is why there is no separate message broker in the denylist below. It adds no process to operate: the queue is the database the application already depends on. |
| `github.com/riverqueue/river/riverdriver` (`v0.46.0`) | River's storage-neutral driver interface. It enters the generated `go.mod` because the client's type parameter is spelled in terms of it; nothing generated imports it directly. |
| `github.com/riverqueue/river/riverdriver/riverpgxv5` (`v0.46.0`) | The pgx v5 implementation of that interface, which is what makes the job client share the application's own pool and its own transactions. Released in lockstep with River itself and only compatible in matched sets, so all three carry one pinned version. |
| sqlc-generated query code | Output of `sqlc generate`; tool-owned complete `store/` directories, checked in and never hand-edited. |
| goose migration files and runtime support | Handwritten SQL migrations run by Goose v3.27.3, embedded in the binary and applied as an explicit deployment step. |
| oapi-codegen-generated server code | Complete output of pinned oapi-codegen from the authoritative OpenAPI document; Nise/tool-owned under `openapigen/`, checked in. |

Version numbers remain omitted only for dependencies whose implementing task
has not yet pinned and verified a released tag.

### Frontend (generated application)

Generated `package.json` pins every dependency exactly. Versions are shown
below where the implementing task has also exercised that exact pair in a
clean install; the remaining planned packages are named without one.

| Package | Why it is allowed |
|---|---|
| SvelteKit | The generated frontend framework. The authenticated application is built with adapter-static and embedded in the Go binary, so it ships as one artifact; protected routes are never prerendered and universal `load` functions run in the browser. |
| Tailwind CSS v4 | The generated styling system. Its output is an external stylesheet, which is what lets the Content Security Policy keep `style-src` free of `'unsafe-inline'` ([ADR 0013](adr/0013-security-headers-and-csp.md)). |
| Valibot | Frontend form validation and ergonomics, paired with the generated API client. Go remains authoritative: this is for the user's benefit in the browser, never the security boundary. |
| TanStack Table | The table primitive. It is wrapped behind Nise-owned Svelte 5 components rather than exposed directly, so a generated resource table has one shape an application can read and edit. |
| `@inlang/paraglide-js` (`2.25.0`) | Compile-time internationalization: `messages/*.json` compile to modules under `src/lib/paraglide/`, so a missing message is a build error rather than a key rendered raw, and a message nothing imports is not in the bundle. It is a build tool — nothing from it ships except the messages the application actually used. The URL strategy is deliberately off: this is one single-page application served from one path, and a locale segment would mean every route existed once per language. `disableAsyncLocalStorage` is on, because the generated server helper otherwise imports `node:async_hooks`, which a browser bundle has no use for. The message-format plugin is a pinned devDependency resolved from `node_modules`, deliberately not the CDN URL inlang's documentation uses: a build step that fetches an unpinned script from a content delivery network is a supply-chain surface, and a lockfile entry is not. |
| `@inlang/plugin-message-format` (`4.1.0`) | The inlang plugin that reads `messages/*.json`. Present so the settings file can name a local path instead of a CDN URL; see the row above. |
| openapi-typescript (`7.13.0`) | Generates checked-in frontend path/operation types from the same authoritative OpenAPI document as the Go server. Its own non-mutating `--check` gate prevents drift. The client is application-owned and adds no runtime client framework. |
| TypeScript (`5.9.3`) | Type-checks the generated frontend and client. Version 5.9.3 is the newest compatible 5.x patch for openapi-typescript 7.13.0's declared `^5.x` peer; the unsupported 6.0.3 pairing was rejected after a clean pnpm install reported it. |
| Vitest (`4.1.11`) | Runs the initial Node-only typed-client unit contract on Node 22. Browser component coverage remains a separate M6 addition. |
| `playwright` (`1.62.1`) | The browser driver behind both the component suite's browser mode and the end-to-end suite. One pin for both, so a project cannot end up driving two different browsers. End-to-end tests are the slowest and most brittle layer, so they cover journeys, not coverage. |
| `@vitest/browser` (`4.1.11`), `@vitest/browser-playwright` (`4.1.11`), `vitest-browser-svelte` (`3.0.0`) | Vitest's browser mode and the renderer that mounts a Svelte 5 component inside it. Components are tested in a real browser because a component's contract is what a browser does with it — focus order, the top layer, the accessibility tree — and a simulated DOM answers several of those differently from every real browser. |
| `axe-core` (`4.13.0`) | The accessibility rule engine the component suite asserts against, over the WCAG 2.0/2.1/2.2 A and AA rule sets. A test dependency: nothing from it ships. |

The generated `package.json` separates the two, and the `dependencies` block —
what actually ships in the bundle — is deliberately short. A Nise test pins it
as a closed list, because "what is in the bundle" is the number that gets away
from a project one convenience at a time.

| Bundled package | Why it ships |
|---|---|
| `valibot` (`1.1.0`) | Form validation in the browser, so somebody typing an address without an "@" is told before they wait for a round trip. It is a courtesy and never a boundary: the server's strict decoding is authoritative, and `src/lib/forms.svelte.ts` says so. Chosen over Zod for size, since this one does ship to every visitor. |
| `@tanstack/table-core` (`8.21.3`) | The column model behind `DataTable.svelte`: accessors, header groups, and cells. Sorting, filtering, and paging are the server's and every `manual*` option is on, so the table renders the page it was given and never reorders a row behind the API's back. The 8.x line rather than 9.x: 9's core delegates reactivity to a per-framework adapter feature, and constructing a table without one throws — verified — so 9 is unusable from a framework-agnostic Svelte wrapper until a Svelte adapter exists. |

Everything else the file pins is a build or test tool.

### Rejected for the generated frontend

| Package | Why it is not there |
|---|---|
| `bits-ui`, `shadcn-svelte` | The curated component set is application-owned Svelte rather than a wrapper over a headless library. [ADR 0013](adr/0013-security-headers-and-csp.md)'s policy has no `style-src-attr 'unsafe-inline'`, and a floating element positioned through a `style` attribute — which is what every Floating UI integration writes — is blocked by it. Taking the dependency would mean either weakening the policy or auditing somebody else's rendering internals on every upgrade. See [ADR 0025](adr/0025-owned-ui-primitives.md). |
| An icon package | An icon is path data on a 24×24 grid; the set stays as small as the application needs. |
| A CSS-class utility (`clsx`, `tailwind-merge`) | Template literals and Svelte's `class:` directive cover what the components do. |

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
