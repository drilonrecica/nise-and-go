# ADR 0011: Runtime Public API Surface and Stability Policy

- **Status:** Accepted
- **Date:** 2026-08-27

## Context

`runtime/` is the only Go API Nise offers to the applications it generates ([ADR 0007](0007-module-path-and-owner.md), `docs/repository-layout.md`). Everything else — `cmd/`, `internal/`, `templates/`, `test/`, `examples/` — is private and may change in any release.

Two failure modes threaten that boundary. The surface can grow until an application cannot be understood without reading Nise, which fails this project's escape-hatch test: a developer who knows Go and Svelte should be able to understand, change, and eventually detach an application without reverse-engineering Nise internals. Or it can be split so finely, or named so vaguely, that nobody can tell which package owns a concern, and shared helpers accumulate in whichever package was touched last.

Six milestone-2 tasks create packages here at roughly the same time. They need the names and the import rules fixed before they start, not discovered afterwards.

## Decision

### The package list

`runtime/` itself is a documentation-only package. It declares `package runtime` and exports nothing, deliberately: an exported identifier there would collide with the standard library's `runtime` in every file that imports both. The surface is its subpackages.

| Package | Owns |
|---|---|
| `runtime/config` | Typed environment configuration, `_FILE` secret variants, fail-closed production validation |
| `runtime/logging` | Structured JSON and human-readable logs over `log/slog`, request and correlation IDs, central redaction |
| `runtime/health` | Startup, liveness, and readiness checks and their handlers |
| `runtime/lifecycle` | Process modes `all`, `web`, and `worker`; HTTP server construction; graceful shutdown and bounded draining |
| `runtime/observability` | HTTP, database-pool, and job metrics, and the optional tracing interface |
| `runtime/secure` | Security headers and Content Security Policy (ADR 0013, pending) |

No other package may be added to `runtime/` without an ADR.

`net/http` middleware lives in the package that owns the concern it implements. There is no middleware aggregator package and no `runtime/middleware`. The ordered middleware chain is generated application code in `internal/platform/httpapi/router.go`, which is where an application can read the order and insert its own handlers ([ADR 0009](0009-generated-application-layout.md)).

### Names considered and rejected

- `runtime/httpx`. HTTP server construction belongs with the shutdown that has to stop it, which is `runtime/lifecycle`; timeouts, draining, and mode selection are one concern, not two. A package whose name is a stem plus `x` also attracts anything HTTP-shaped.
- `runtime/http`, `runtime/log`, `runtime/context`. Each shadows a standard-library import path in the import block of files that use both.
- `runtime/metrics` and `runtime/tracing` as separate packages. Tracing is a narrow optional interface with no mandatory collector; on its own it would be a package with one type in it.
- `runtime/server` and `runtime/app`. Both invite a god object that owns configuration, logging, routing, and shutdown, which is the framework shape Nise exists to avoid.

### Import rules

- A `runtime/` package may import the standard library and `runtime/internal/...`.
- A `runtime/` package may import another `runtime/` package only along an edge named here: `runtime/lifecycle` may import `runtime/config`, `runtime/logging`, and `runtime/health`. Every other pair is forbidden.
- `runtime/logging` must not import `runtime/config`. Fusing them would make two public packages inseparable for every application. The application wires configuration into the logger through the logger's constructor.
- Shared implementation lives in `runtime/internal/...`, which the Go toolchain makes invisible to applications. Central redaction is the expected first resident.
- No `runtime/` package may import `internal/`, `cmd/`, `templates/`, `test/`, or `examples/`. The dependency direction is one way.
- The reverse direction is permitted, and bounded. `internal/` and `cmd/` may import `runtime/` **only to reuse a rule the runtime enforces unconditionally**, so that the CLI never validates a divergent copy of a rule the application will actually be held to. It is not a general licence for the CLI to build on runtime types. Today the only such import is `internal/check` → `runtime/config` and `runtime/lifecycle`, for `ParseEnvironment` and `ParseMode`: `nise check --env-file` validates an environment file without starting the application, and a second copy of those closed value sets would drift into validating something other than what the process will accept. Adding another such import means adding its justification to this list.
- Reusing a rule this way is preferred over duplicating it, but duplication remains correct where there is no single source of truth to drift from. `internal/cli/clierr`'s redaction key list is the standing example: it is a deliberately smaller, independently-scoped list serving a different input population, not a copy of `runtime/logging`'s that is expected to track it. See the reasoning at `internal/cli/clierr/redact.go`.

### What may never appear in `runtime/`

Generators and templates. CLI presentation: ANSI codes, spinners, prompts, terminal detection, `os.Exit`. Network clients, update checks, and telemetry of any kind. Global mutable state, `init()` side effects, package-level singletons, registries populated by import, and reflection-driven dependency injection. Anything that reads the project recipe — that is CLI work.

### Stability

`0.x` is explicitly unstable. Breaking changes to `runtime/` are permitted in any `0.x` release and must ship with a `CHANGELOG.md` entry and migration notes (`docs/versioning.md`). From `1.0`, documented exported identifiers in `runtime/` follow semantic versioning. Generator internals never do.

Every exported identifier in `runtime/` carries a doc comment. An exported identifier without one is a defect, not an undocumented-and-therefore-private API; Go has no such category.

Adding, removing, or retyping an exported identifier in `runtime/` is a contract change. It requires a doc comment on the identifier, a corresponding update to `docs/runtime-packages.md`, and an explicit line in the commit message saying what the surface gained or lost. A change that only touches unexported code is not a contract change.

## Consequences

- The six milestone-2 tasks can be written in parallel against fixed import paths, and an application's import block names the concern it depends on.
- The forbidden edges mean some wiring lands in the application rather than in Nise — a logger is constructed from configuration values by the generated `internal/app` code. That is the intended direction: explicit constructors over hidden coupling.
- `runtime/lifecycle` is the largest package in the list and is the one most likely to need splitting. It is the package to watch.
- The empty `package runtime` looks odd in a package listing and exists purely to carry documentation. Keeping it empty forever also keeps the standard-library name collision theoretical.
- Six public packages is a real commitment for a project with no production users yet. Revisit the list — as one ADR, not six — after the reference application is built, which is the first evidence that a package is missing or unused.
