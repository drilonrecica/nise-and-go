# ADR 0008: Toolchain Pinning and Dependency Allowlist

- **Status:** Accepted
- **Date:** 2026-08-27

## Context

Nise generates a Go + chi + PostgreSQL + SvelteKit application and ships a
`nise` CLI that must run predictably on every contributor's machine and in
CI. Two related risks need a settled answer before any implementation code
lands:

1. Without pinned tool versions, `sqlc`, `goose`, and `oapi-codegen` code
   generation is not reproducible across machines, and Node/pnpm drift
   produces different `pnpm-lock.yaml` output for the same source.
2. Without a dependency allowlist with reasons, "standard library first"
   (blueprint §6) is a slogan, not an enforceable rule, and framework-owned
   `go.mod`/`package.json` accrete convenience dependencies over time.

The repository already has one Go module at the root
(`github.com/drilonrecica/nise-and-go`, ADR 0007) with no `go.work`
workspace and no Go source files yet — only `cmd/`, `internal/`, `runtime/`,
and `modules/` placeholders. The verified development environment is Go
1.26.5, Node 22.22.2, pnpm 10.33.0, Docker 29.6.2, Podman 5.8.4, with
`golangci-lint` 2.11.3 and `sqlc` present as pre-installed binaries, and
`GOTOOLCHAIN=local`.

## Decision

### Single Go module, not a `go.work` workspace

The repository uses one Go module rooted at `github.com/drilonrecica/nise-and-go`.
`cmd/nise`, `internal/`, `runtime/`, and `modules/` are packages within it,
not separate modules. This matches the project recipe recording "CLI version
and runtime versions" as one compatibility unit (blueprint §12,
`versioning.md`): a single module guarantees the CLI and the runtime
packages an application imports are always built from one consistent,
version-locked dependency graph, and there is nothing today that needs an
independent release cadence.

**Reversal trigger:** revisit this as a `go.work` workspace (or split
repositories) only when `runtime/` genuinely needs to release on its own
schedule, independent of `cmd/nise` — for example if application
maintainers need a runtime security patch without taking an unrelated CLI
change, and coupling the two starts causing real upgrade friction. Blueprint
§5 sets the same bar: "split only when independent release cycles are truly
required."

### Go directive stays at the `go mod tidy`-produced value

Adding `tool` directives and running `go mod tidy` rewrote the `go.mod` `go`
directive from the two-component `go 1.26` to the three-component
`go 1.26.0`. Plain `go build`/`go vet` do not touch it — only `go mod tidy`
and `go get -tool` do, and they redo the rewrite every time they run. Rather
than hand-edit it back to `go 1.26` after every future `go mod tidy` (a fight
this repository cannot win), `go.mod` keeps the tidy-produced `go 1.26.0`.
It expresses the same minimum language version and the installed toolchain
(Go 1.26.5) still satisfies it under `GOTOOLCHAIN=local`.

### Go tool pinning: `tool` directives for generators, version pin for the linter

Go 1.24+ `tool` directives in `go.mod` pin the three code generators Nise
depends on for reproducible builds and `go get -tool`-driven updates:

- `github.com/sqlc-dev/sqlc/cmd/sqlc`
- `github.com/pressly/goose/v3/cmd/goose`
- `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen`

`golangci-lint` was evaluated the same way and rejected as a `tool`
directive based on measured evidence, not assumption. With only the three
generators pinned, `go.sum` is 500 lines. Adding
`github.com/golangci/golangci-lint/v2/cmd/golangci-lint` as a fourth `tool`
directive and running `go mod tidy` grew `go.sum` to 1,277 lines (+777
lines, +155%) and the `go.mod` indirect `require` block from 101 to
294 entries — an entire linter-rule dependency forest (viper, cobra,
per-rule analyzer packages, `mvdan.cc/gofumpt`, `mvdan.cc/unparam`, and
more) with no relationship to sqlc, goose, or oapi-codegen. That change was
reverted. `golangci-lint` is instead pinned by documented version in
[`docs/toolchain.md`](../toolchain.md) (currently v2.11.3, matching the
verified installed binary), with CI expected to install that exact pinned
version explicitly. Bumping it is a deliberate, documented edit, the same
discipline as a `go.mod` dependency bump.

These three tools are tool-only dependencies: `runtime/`, `internal/`, and
`cmd/` must never import them or their transitive graph as runtime code.

### Frontend: pnpm, Node 22 LTS, Corepack

pnpm is the package manager for every generated frontend (`DECISIONS.md`,
blueprint §12). Generated projects commit `pnpm-lock.yaml` and carry a
`packageManager` field in `package.json` so Corepack activates the exact
pinned pnpm version (`corepack enable`, `corepack use pnpm@<version>`)
without a separate global install step. The pinned Node major is 22 LTS,
with 22.22.2 recorded as the minimum verified version — no older 22.x
release has been checked, so none is claimed as a floor.

### `.tool-versions` is advisory; CI pins explicitly

A root `.tool-versions` file (asdf/mise plain-text format) pins `golang`,
`nodejs`, and `pnpm` to the exact verified versions above for contributors
who use asdf or mise. It is advisory only — no Nise command or build step
reads it. CI pins the same versions explicitly in its own workflow
configuration, so `.tool-versions`, `docs/toolchain.md`, and CI must be kept
in sync by hand when any version changes.

### Dependency allowlist with reasons

[`docs/dependencies.md`](../dependencies.md) is the enforceable form of
"standard library first": it states the rule, lists every dependency
currently allowed into Nise-maintained code with a reason and a removal
condition, records the generated-application allowlist derived from
`DECISIONS.md` (chi, pgx, River, sqlc/goose/oapi-codegen output, and the
frontend stack), and restates the denylist of rejected categories (generic
cache, message broker, plugin system, reflection DI, schema DSL/ORM,
mandatory OpenTelemetry collector, CLI framework, AI/model-provider SDK).
Adding or removing a framework dependency requires updating that page and
saying so in the commit message.

For M0, M1, and M2 the target remains zero runtime dependencies: as of this
ADR, `cmd/`, `internal/`, and `runtime/` contain no Go source files, so the
target holds trivially, and it becomes an enforceable constraint the moment
the first package is added.

## Consequences

- Reproducible generation: `go tool sqlc`, `go tool goose`, and
  `go tool oapi-codegen` resolve to exactly the versions recorded in
  `go.sum`, on any machine, without a separate install step beyond
  `go build`/`go mod download`.
- `go.sum` stays proportionate to what Nise actually needs at build time
  (500 lines for three generators) instead of absorbing an unrelated
  linter's full dependency graph; `golangci-lint` version drift is instead
  visible as a single documented line in `docs/toolchain.md` and in CI
  configuration.
- Contributors on asdf or mise get one authoritative `.tool-versions` file,
  at the cost of a manual sync obligation between that file,
  `docs/toolchain.md`, and CI whenever a pinned version changes — no
  Nise command currently enforces that the three stay in sync.
- The single-module decision means every `runtime/` change and every
  `cmd/nise` change ship in the same tagged release; there is no way to
  patch one without the other until the reversal trigger above is met and a
  new ADR records the split.
- The dependency allowlist is a review gate, not a build-time check in this
  task: nothing yet fails CI if a dependency is added without a
  corresponding `docs/dependencies.md` entry. A later task may want to make
  that mechanical (for example, a script diffing `go.mod`/`package.json`
  against the allowlist).
- Migration/compatibility impact: none yet, since no Nise-maintained Go
  package exists to depend on anything. This ADR fixes the policy before
  that code is written, which is the intended sequencing.
