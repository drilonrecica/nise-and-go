# Toolchain

This page describes planned and current V0.1 policy for the versions of Go,
Node, pnpm, and generator tools that build Nise itself. It does not describe
the toolchain of a generated application, which is documented with the first
generated project.

## Go

- Go directive in `go.mod`: `go 1.26.0`. `go build`/`go vet` leave a bare
  `go 1.26` directive alone, but `go mod tidy` and `go get -tool` rewrite it
  to the three-component `go 1.26.0` once `tool` directives are present;
  this repository keeps the `go mod tidy`-produced form rather than fighting
  a value that reverts on the next tidy (see
  [ADR 0008](adr/0008-toolchain-and-dependencies.md)).
- Verified development toolchain: Go 1.26.5, linux/amd64.
- `GOTOOLCHAIN=local`. Nise does not let `go` silently download a newer
  toolchain; contributors and CI install the pinned version explicitly.

## Pinned Go tools

Nise pins its Go generator and migration tools with Go 1.24+ `tool`
directives in `go.mod`, so `go get -tool` updates them reproducibly and
`go.sum` records their exact resolved graph:

- `github.com/sqlc-dev/sqlc/cmd/sqlc`
- `github.com/pressly/goose/v3/cmd/goose`
- `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen`

Run them with `go tool <name>` (for example `go tool sqlc version`) or
`go run <import path>@<version>` for a one-off outside the pin. These are
tool-only dependencies: they must never be imported by `runtime/`,
`internal/`, or `cmd/`. See [dependency allowlist](dependencies.md).

### golangci-lint is pinned by version, not by `tool` directive

`golangci-lint` is deliberately **not** a `go.mod` `tool` directive. Adding it
that way was measured and rejected: on a `go.mod` that already pins sqlc,
goose, and oapi-codegen (`go.sum` at 500 lines), adding
`github.com/golangci/golangci-lint/v2/cmd/golangci-lint` as a fourth `tool`
grew `go.sum` to 1,277 lines (+777 lines, +155%) and the `go.mod` indirect
`require` block from 101 to 294 entries, pulling in linter-specific
dependency trees (viper, cobra, dozens of individual lint-rule packages,
`mvdan.cc/gofumpt`, `mvdan.cc/unparam`, and more) that have nothing to do with
sqlc, goose, or oapi-codegen. That contradicts the standard-library-first
policy for tool-only weight, so `golangci-lint` is pinned by documented
version instead:

- **Pinned version: `golangci-lint` v2.11.3** (installed and verified at
  `/usr/bin/golangci-lint`, built with `go1.26.0`).
- Contributors install it via their platform package manager or the
  `golangci-lint` install script, matching this pinned version.
- CI installs the same pinned version explicitly (a later task wires this
  into `.github/workflows/`; this page is the source of truth for the
  version CI must use).
- Bumping the pinned version is a deliberate edit to this page (and to CI)
  with a reason, the same way a dependency bump requires an entry in
  [dependencies.md](dependencies.md).

## Frontend: Node and pnpm

- Package manager: **pnpm** (see `DECISIONS.md` / blueprint §12).
- Node major: **22 LTS**. Minimum verified version: **22.22.2** (the version
  installed and verified in the reference development environment; no older
  22.x version has been verified, so none is claimed as a floor).
- pnpm: **10.33** (minor line), verified at **10.33.0**.
- Corepack is the supported activation path for pnpm (`corepack enable`,
  `corepack use pnpm@10.33.0`), not a global `npm install -g pnpm`.
- Generated projects commit `pnpm-lock.yaml`.
- Generated `package.json` carries a `packageManager` field
  (`pnpm@10.33.0`) so Corepack pins the exact version per project.

## `.tool-versions`

The repository root `.tool-versions` file pins `golang`, `nodejs`, and `pnpm`
in the plain-text format shared by asdf and mise:

```text
golang 1.26.5
nodejs 22.22.2
pnpm 10.33.0
```

`.tool-versions` is **advisory** for contributors who use asdf or mise; it is
not consulted by `go build`, `go test`, or any Nise command. CI pins the same
versions explicitly in its own workflow configuration rather than reading
this file, so the two must be kept in sync by hand when either changes.

## Containers

PostgreSQL and other supporting development services run in containers
(blueprint §12). Docker 29.6.2 and Podman 5.8.4 were verified in the
reference development environment; Nise does not pin a specific container
runtime version because neither is embedded in a generated artifact or
required at a specific version for correctness. Any container runtime that
supports the project's Compose file is sufficient.

## Verification

- `go version` → `go1.26.5 linux/amd64` (or the pinned patch version).
- `go env GOTOOLCHAIN` → `local`.
- `go tool` → lists the pinned tool directives alongside the Go toolchain's
  built-in tools.
- `go tool sqlc version`, `go tool goose --version`,
  `go tool oapi-codegen -version` → each prints a version, proving the pin
  resolves.
- `golangci-lint --version` → matches the version pinned above.
