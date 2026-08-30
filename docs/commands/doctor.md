# `nise doctor`

`nise doctor` checks that the tools the golden profile needs are installed
and new enough, and — when run inside a generated project — that the
project's recipe (`nise.json`) parses and is compatible with the nise build
currently running.

It runs local executables only. It never contacts a network registry,
proxy, or update endpoint (see [Toolchain](../toolchain.md) and
[No telemetry](../no-telemetry.md)): every check below is either a version
flag on a tool
already installed on this machine, or a read of a local file.

```
$ nise doctor
$ nise doctor --json
```

See [CLI output contract](../cli-output.md) for `--json`, `--verbose`,
`--quiet`, and the exit-code contract in general.

## Exit code

`0` when every check is `ok`, `warn`, or `skipped`. `3`
(`clierr.ExitPrecondition`) when any check reports `fail` — a required
tool is missing, unreachable within its timeout, or older than the pinned
minimum.

## Output shape

Human mode prints one block per check, in the order listed below, then a
one-line summary (`N ok, N warn, N fail, N skipped`).

`--json` prints one JSON document per line (NDJSON) — one per check, each
shaped like:

```json
{"name": "go", "status": "ok", "found": "go 1.26.5", "required": "go 1.26.0 or newer"}
```

`status` is one of `ok`, `warn`, `fail`, `skipped`. `found` and `required`
are always present when the check ran; `remedy` is present whenever
`status` is not `ok` and there is a concrete action to take. If any check
fails, one further JSON document — the normal `{"error": {...}}` envelope
described in [CLI output contract](../cli-output.md#json-rendering) —
follows as the last line, matching the exit code.

## Checks

### `go`

Runs `go version` and requires at least the version pinned by the `go`
directive in `go.mod` (see [Toolchain](../toolchain.md#go)). `warn` is
never used here: a missing or too-old Go toolchain is always `fail`.

- **Remedy:** install the pinned Go version from https://go.dev/dl/.
  `GOTOOLCHAIN=local` means nise itself will never download one for you.

### `node`

Runs `node --version` and requires at least the minimum verified in
[Toolchain](../toolchain.md#frontend-node-and-pnpm).

- **Remedy:** install Node.js 22 LTS from https://nodejs.org/, or via nvm.

### `pnpm`

Runs `pnpm --version` and requires at least the version pinned in
[Toolchain](../toolchain.md#frontend-node-and-pnpm).

- **Remedy:** enable pnpm via Corepack: `corepack enable && corepack use
  pnpm@<pinned version>`.

### `container-runtime`

Runs `docker --version`, then `podman --version` if that fails. Either
satisfies the requirement — [Toolchain](../toolchain.md#containers) pins
no specific version of either, since neither is embedded in a generated
artifact. This check never assumes the binary named `docker` actually is
Docker: on many systems `docker` is a Podman compatibility shim, so the
tool's own self-reported name in its version output (not the binary name
doctor invoked) is what gets reported in `found`, e.g. `podman 5.8.4
(invoked as "docker", which is a podman shim on this machine)`.

- **Remedy:** install Docker (https://docs.docker.com/get-docker/) or
  Podman (https://podman.io/getting-started/installation) and ensure it
  is on `PATH`.

### `sqlc`, `goose`, `oapi-codegen`

All three run in the Nise framework repository. Inside a generated project,
they are skipped without execution: all are pinned there, but Go may download
and build them on first use. Explicit build, test, and generator commands are the user-authorized
boundary for any resolution that may need the module proxy.

**Outside a generated project — i.e. inside the Nise framework repository
itself:**

Runs its tool through `go tool <name>` (`go tool sqlc version`,
`go tool goose --version`, `go tool oapi-codegen -version`), which
resolves the exact version pinned by this repository's `go.mod` `tool`
directives and `go.sum` — see
[Toolchain](../toolchain.md#pinned-go-tools). Because that pin lives in
`go.mod`/`go.sum` rather than as a separate number on the toolchain page,
this verifies only that the tool resolves and prints a parsable version,
not a specific minimum — the pin itself is enforced by `go.sum`, not by
doctor re-deriving it.

- **`fail`** when the tool does not resolve through `go tool` or prints
  nothing parsable as a version.
  - **Remedy:** run `go get -tool <module path>` from the repository root
    (the exact command is included in the check's `remedy` field/line).
- **`ok`** when it resolves and parses.

**Inside a generated project:**

- **sqlc:** `skipped`, explaining that it is declared but not executed
  implicitly. Run `make sqlc-compile` to resolve and verify it
  explicitly.
- **Goose:** `skipped`, explaining that it is declared but not executed
  implicitly. Run `nise db status` against the intended database, or
  `go test ./db/... ./internal/platform/database/...` to resolve it and
  verify source/embed parity plus the generated runner.
- **oapi-codegen:** `skipped`, explaining that it is declared but not executed
  implicitly. Run `make api-check` to resolve the pin and verify the checked-in
  strict bindings against `api/openapi.yaml` without modifying them.

This preserves `doctor`'s no-implicit-network contract. In a clean
module cache, `go tool sqlc version` or `go tool goose --version` downloads
the pinned module before it can print a version; a diagnostic command must
not initiate that traffic.

### `recipe`

Looks for `nise.json` in the current directory or any parent (the same
project-root convention `internal/recipe` documents) and, if found,
parses it with `internal/recipe`.

- **`skipped`** when no `nise.json` is found anywhere above the current
  directory — `nise doctor` is frequently run against the host machine
  alone, not inside a generated project, and that is not an error.
- **`fail`** when a `nise.json` is found but does not parse: malformed
  JSON, an unknown field, a schema version newer than this build
  understands, or any other rule `internal/recipe.Load` enforces.
  - **Remedy:** for a schema version this build does not understand,
    upgrade nise. Otherwise, fix `nise.json` by hand or restore it from
    version control; see
    [ADR 0010: Project recipe format](../adr/0010-project-recipe-format.md).
- **`ok`** when the recipe parses. `found` includes the resolved path,
  schema version, profile, and recorded CLI/runtime versions.

### `recipe-compatibility`

Only meaningful once `recipe` is `ok`; otherwise it reports `skipped` with
a pointer back to the `recipe` check. When a recipe is available, this
check compares the currently running nise build's own version against the
recipe's recorded `cliVersion`.

- **`skipped`** when there is no recipe to compare, or when either version
  string (the running build's, or the recipe's `cliVersion`) does not
  parse as a version at all — most commonly a development build that
  reports `dev` rather than a real semantic version. Reporting `ok`
  without an actual comparison would be exactly the false-positive this
  command exists to avoid.
- **`ok`** when the running nise is at least the recipe's `cliVersion`.
- **`warn`** when the running nise is older than the recipe's
  `cliVersion` — the project was created or last upgraded by a newer
  nise than the one currently running. This does not block the golden
  profile from working today, so it warns rather than fails.
  - **Remedy:** upgrade the nise binary you are running to at least the
    recipe's `cliVersion`.
- **`warn`** (a second case) when the recipe's `cliVersion` field itself
  does not parse as a version — most likely a hand-edited recipe.
  - **Remedy:** check that `nise.json` was not hand-edited; regenerate it
    if unsure.

`runtimeVersion` is reported alongside the comparison (in `found`) for
context but is not itself compared: it records the Nise runtime-package
version an application's own `go.mod` targets, which this command does
not read (see
[Versioning and compatibility](../versioning.md#project-metadata)).

## Safety notes

- Every external command runs via `exec.CommandContext` with a bounded
  timeout (10 seconds per check) and bounded captured output (16 KiB): a
  wedged or runaway tool cannot hang `nise doctor` or exhaust memory.
- No command-line argument, environment variable, or file content ever
  reaches a shell — every invocation is a fixed argv vector this command
  defines itself, never built from user input.
- No check performs a network request. `go tool sqlc`, `go tool goose`,
  and `go tool oapi-codegen` resolve entirely from the local module cache
  these binaries were already built into; if that cache is missing the
  underlying `go` command may itself need network access to populate it —
  that is a property of `go tool`'s own first-run behavior, not something
  `nise doctor` initiates independently.
