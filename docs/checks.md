# Checks

This page lists every automated check on this repository, what defect class
it protects against, and how to run it locally. The hermetic checks in
`.github/workflows/ci.yml` run the same commands `make check` does. CI also
runs the explicitly service-backed `make migration-test` gate against
PostgreSQL 17.6; it cannot be folded into a command that works without an
external database.

**A check is never weakened to make a change pass.** If a check is wrong,
fix the check in its own change with its own justification. If the code is
wrong, fix the code. "The check was in the way" is not a reason to disable,
skip, or narrow a check.

## Running everything

```sh
make check
```

Runs, in order: `fmt-check`, `vet`, `lint`, `test`, `test-race`,
`generate-diff`, `docs-check`. `make help` lists every target with a one-line
description.

To run the real PostgreSQL migration gate as well, point it at an
administrative database on disposable test infrastructure:

```sh
TEST_DATABASE_URL='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable' \
  make migration-test
```

## The checks

### `make fmt-check` — formatting

Protects against: inconsistent whitespace and formatting reaching `main`,
which turns every subsequent diff into a noisy whitespace-plus-content diff.

Runs `gofmt -s -l` over `cmd/`, `internal/`, and `runtime/` and fails if it
lists any file. `make fmt` applies the fix in place (`gofmt -s -w`).

### `make vet` — go vet

Protects against: constructs that compile but are almost certainly wrong
(bad `Printf` verbs, unreachable code, struct tag typos, copying a `sync`
lock by value) — the standard library's own bar for "this is a bug, not a
style preference."

Runs `go vet` over the same three package roots as `fmt-check`.

### `make lint` — golangci-lint

Protects against: a deliberately small set of real defect classes not
covered by `go vet` — unchecked errors, dead assignments, dead code, unclosed
HTTP response bodies, broken `errors.Is`/`As` wrapping, common security
mistakes, misspelled words in comments/strings, and exported identifiers
missing doc comments. `.golangci.yml` names, for every enabled linter, the
specific defect it exists to catch — see that file, not this page, for the
per-linter justification.

`golangci-lint` is pinned by **documented version**
(`docs/toolchain.md`, currently v2.11.3), not a `go.mod` `tool` directive —
[ADR 0008](adr/0008-toolchain-and-dependencies.md) has the measured reason
(a fourth `tool` directive grew `go.sum` from 500 to 1,277 lines pulling in
an entire linter-rule dependency forest unrelated to sqlc/goose/oapi-codegen).
Because it is not a `tool` directive, both `make lint` and CI must install it
explicitly:

- `make lint` itself checks the installed `golangci-lint version` output
  against the pinned version in the `Makefile` and fails with a clear message
  on a mismatch, rather than silently linting with the wrong ruleset.
- CI's `lint` job installs the exact pinned version with
  `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3`.
  This resolves and checksum-verifies the release through the Go module
  proxy — it does not touch this repository's own `go.mod` or `go.sum`,
  because `go install pkg@version` builds against the target module's own
  build graph, not this repository's.
- Bumping the pinned version means editing `docs/toolchain.md`, the
  `GOLANGCI_LINT_VERSION` value in the `Makefile`, and the same value in
  `ci.yml`, together, with a reason — the same discipline as a dependency
  bump.

Contributors install a matching `golangci-lint` locally via their platform
package manager or the project's install script; `docs/toolchain.md` is the
source of truth for which version.

#### Test-file exclusion: gosec G304 and G306

`.golangci.yml` excludes gosec rules `G304` (file inclusion via variable) and
`G306` (WriteFile permissions) inside `_test.go` files only, scoped by path
(`_test\.go`), linter (`gosec`), and rule ID (`G304|G306` matched against the
issue text) simultaneously — every other gosec rule, and G304/G306
themselves in non-test code, stay at full strength.

This is a scoping decision, not an instance of "a check is never weakened to
make a change pass" above. The two rules exist to catch a specific
production threat each: G304 catches a filesystem path built from untrusted
input, G306 catches production code writing a world- or group-readable file.
A `_test.go` file that builds its own path under `t.TempDir()` and writes a
fixture it is about to read back in the same test process has neither
property — the path is never attacker-controlled, and the file's
permissions and lifetime are the test's own. Leaving the rules on for tests
would not catch a real defect there; it would only force a `#nosec` onto
every table-driven test that touches `t.TempDir()`, and each rote
suppression comment added to a test dilutes the signal of a real one
appearing in production code — exactly what enabling a linter is supposed
to prevent, not cause. The exclusion was verified to still fire on
production code: a `0o644` `os.WriteFile` or a variable-derived `os.Open`
outside a `_test.go` file is still reported (verified against a throwaway
module during this task; see `.superpowers/sdd/execution-plan/task-4-report.md`
fix round 1 for the exact commands).

The exclusion was added only after this exact pair of rule IDs, in this
exact file-suffix scope, was individually justified above — not as a
reflex response to seeing findings disappear. A broader exclusion (all of
gosec in test files, or G304/G306 everywhere) was considered and rejected
for that reason.

### `make test` / `make test-race` — Go tests

Protects against: regressions and, under `-race`, data races. `go test -race`
must pass per Global Constraint 10 — no test is skipped or weakened to make
a change pass.

Both run over the same three package roots. Neither `test` nor `test-race`
is a substitute for the other: `test` runs faster and is what a contributor
runs on every save; `test-race` is slower and specifically targets
concurrency bugs `test` cannot detect.

### `make migration-test` — PostgreSQL migration paths

Protects against: a generated application that builds but cannot migrate a
clean database or one of its explicitly supported historical release schemas.

The target generates a fresh application, applies a local `replace` for this
checkout, and invokes that application's own race-enabled migration matrix.
It requires `TEST_DATABASE_URL`; the configured role must have `CREATEDB` on
disposable test infrastructure. CI supplies PostgreSQL 17.6 and calls this
target in a dedicated Linux job. Ordinary unit-test jobs skip the outer probe
unless `NISE_MIGRATION_MATRIX=1`, preventing three OS legs from each trying to
provision an undeclared database while making the dedicated gate non-skippable.

### `make generate` / `make generate-diff` — the determinism gate

Protects against: checked-in generated output silently drifting from what
its generator would produce today — the exact failure mode Global Constraint
5 (deterministic output) exists to prevent.

`make generate` runs `go generate` over `cmd/`, `internal/`, and `runtime/`.
As of this task, no package in this module contains a `//go:generate`
directive, so `make generate` is a real command that does nothing — not a
stub that merely looks like a check. `make generate-diff` depends on
`generate`, then fails if `git status --porcelain` is non-empty afterward.

**How a future generator lands under this gate automatically:** the moment
any package adds a `//go:generate` line, `go generate ./cmd/... ./internal/...
./runtime/...` (what `make generate` already runs) executes it, and
`generate-diff`'s existing `git status --porcelain` check then covers its
output — with no edit to the `Makefile` or to `ci.yml`. This is the whole
reason `generate` is implemented as `go generate` over the package roots
rather than as an empty placeholder recipe: the mechanism that will pick up
tomorrow's generator already exists today and is exercised today (see the
[verification](#verification) section below for evidence it currently
passes).

`generate-diff` must run on a clean working tree — CI always does (a fresh
checkout on every run); a contributor running it locally with unrelated
uncommitted changes will see those changes reported as a "diff," which is
correct and expected: the check cannot distinguish "changed by generation"
from "already dirty before generation ran." Commit or stash first.

### `make docs-check` — relative Markdown link checker

Protects against: a broken relative link in `docs/**` or a root `*.md` file
— documentation that silently points at a page that moved or was deleted.

Implementation choice: a `sh` + `git`/`grep`/`sed` recipe directly in the
`Makefile`, not a Go program under `internal/tools/` — this task's brief
offered both options, and `internal/` is out of this task's scope (owned by
concurrently running tasks; see the review notes in
`.superpowers/sdd/execution-plan/task-4-report.md`). A shell implementation
needs no test binary of its own and has no dependency beyond what every
other target already assumes (`git`, POSIX `sh` and coreutils).

What it does:

1. `git ls-files -- 'docs' ':(glob)*.md'` lists every **tracked** Markdown
   file under `docs/` (any depth, including `docs/adr/`) plus every tracked
   `*.md` file at the repository root — the `:(glob)` pathspec magic
   restricts the root wildcard to top-level files only, so it does not also
   match `internal/cli/README.md` or similar. Using `git ls-files` rather
   than a filesystem glob means the check only ever considers committed
   files: an in-progress, not-yet-committed doc from concurrent work is not
   flagged, and — same mechanism — the check can never be tricked by an
   ignored path, since `git ls-files` only lists tracked files in the first
   place.
2. Each file first has every backtick-delimited inline code span stripped
   (`sed -E 's/`[^`]*`//g'`), then `grep -oE '\]\([^)]+\)'` extracts every
   remaining `](target)` occurrence (covers both `[text](target)` links and
   `![alt](target)` images), and `sed` strips the leading `](` and trailing
   `)`. The backtick-stripping pass exists because a page that *documents*
   Markdown link syntax as literal text — this page is exactly such a page —
   would otherwise have its own examples misread as real links; a real
   link's target is never itself written inside backticks in this
   repository's docs, only (occasionally) its link text, e.g.
   `` [`docs/toolchain.md`](toolchain.md) `` in ADR 0008, which the strip
   leaves untouched because the backticks there wrap only the `[...]` part,
   not the `](...)` part.
3. A target starting with `http://`, `https://`, `mailto:`, `//`, or `#` is
   skipped (external links and same-page anchors are not this check's job).
4. Otherwise, everything from the first `#` onward is stripped (an in-page
   anchor suffix on a cross-file link, e.g. `foo.md#section`), and what
   remains is resolved **relative to the linking file's own directory** —
   the same rule every Markdown renderer uses, and the convention already
   used consistently by every link in this repository's docs today (for
   example `docs/dependencies.md` links `toolchain.md`, not
   `docs/toolchain.md`).
5. If the resolved path does not exist, the check reports it and fails.

Known limitation: reference-style links (`[text]: target` on its own line,
with no parentheses) are not extracted. None exist in this repository's docs
today; if one is added, this check will not see it. Extending the pattern to
cover that style is a reasonable future improvement, not a blocker for this
task's "at minimum verify inline link targets" requirement.

A stray literal `#` character inside a `Makefile` recipe line starts a
`Makefile` comment — even inside shell quotes — unless escaped. Rather than
sprinkle backslash-escapes through the recipe, the `Makefile` defines
`HASH := $(shell printf '\043')` once (octal 043 is `#`) and the recipe
refers to `$(HASH)` wherever the shell script itself needs a literal `#`.
Because `$(HASH)` contains no `#` in the `Makefile`'s own source text, `make`
never comment-strips it; by the time the shell runs, `$(HASH)` has already
been substituted with a real `#`.

## CI matrix

See [supported-platforms.md](supported-platforms.md) for the full breakdown
of release platforms vs. development-supported platforms vs. what CI
actually exercises. In short: `test` runs on `ubuntu-latest`, `macos-latest`,
and `windows-latest`; `lint`, `generation-diff`, and `docs-check` run on
`ubuntu-latest` only, because their result does not depend on OS.

## Frontend targets are not wired in yet

`docs/toolchain.md` pins Node 22.22.2 and pnpm 10.33.0 because a later task
(`nise new`, generating a SvelteKit project under `templates/`) needs them.
This repository does not yet contain a `package.json` anywhere, so `ci.yml`
does not set up Node or pnpm today — there is nothing for such a step to
build, lint, or test, and a no-op setup step would misleadingly imply
frontend CI exists. The Node/pnpm setup step belongs in the same change that
first adds a `package.json` to this repository.

## Why every Go target names its package roots explicitly

`frontend/node_modules/` (documented in
[generated-application-layout.md](generated-application-layout.md), and
eventually present under `templates/` in this repository too) sits inside
this Go module's directory tree but is not Go source Nise owns. `go build
./...`, `go vet ./...`, `go test ./...`, and `gofmt -l .` all walk into it:
tens of thousands of vendored JS files at best, and at worst an npm package
that happens to ship a stray `.go` file gets treated as a real package of
this module. Every Go-tooling target in the `Makefile` and in `ci.yml`
therefore names `./cmd/... ./internal/... ./runtime/...` (or, for `gofmt`,
the equivalent directory list `cmd internal runtime`) instead of `./...` or
`.`. `...` already matches every package created under those three roots
without a `Makefile` change, so `internal/cli`, `internal/generator`, and
every `runtime/*` package landing later in this slice are covered
automatically; `modules/`, `templates/`, `examples/`, and `test/` are named
explicitly only once a future task confirms each holds Go source and nothing
else that would repeat the `frontend/node_modules` problem.

## The generated application's own service tests

A generated project ships tests for its background jobs, mail, object
storage, and notifications. Most of them are hermetic and run under `go test`
with nothing installed. The ones that matter most are not, and that is the
point.

**Against a real PostgreSQL** (`TEST_DATABASE_URL`; skips locally, fails under
`CI=true`):

| Suite | What only a real database can show |
|---|---|
| `internal/platform/jobs` | That a failed job is actually retried and the attempt count advances; that a terminal failure is not; that **two clients never run the same job twice**; that a unique period is refused by the index rather than by the options; that `Stop` drains rather than abandons |
| `internal/features/uploads` | That a rolled-back staging leaves nothing, and that every adversarial finalization is refused |
| `internal/features/notifications` | That a rolled-back notification reaches nobody, and that a committed one reaches a live subscriber through PostgreSQL's own `NOTIFY` |

**Against a real S3-compatible service** (`TEST_S3_ENDPOINT`; skips
everywhere, including CI):

`internal/platform/storage`'s `TestS3AgainstARealService` is the only thing
that can confirm a SigV4 signature is correct. Every *wrong* implementation
also produces a well-formed signature; only a service disagrees. It skips
rather than fails when unset — unlike the database suites — because object
storage is an optional module many deployments configure as `local` and never
point at a service, while a database is required for the application to work
at all.

**Under `-race`:** the storage suite writes one key from two goroutines while
a third reads it, and the notification suite subscribes and unsubscribes from
sixteen goroutines while a seventeenth signals continuously. Both are shapes
that produce a rare, unreproducible crash in production and a deterministic
failure under the race detector.

## What CI deliberately does not do

- **No release workflow.** Building or publishing the six release binaries
  (Linux/macOS/Windows × amd64/arm64) is milestone M10. Nothing under
  `.github/workflows/` attempts it.
- **No vulnerability-scanning workflow.** Dependency/vulnerability scanning
  is M9-002. Nothing under `.github/workflows/` attempts it. (`gosec`, one of
  the linters `make lint` runs, scans source code for security-relevant
  patterns; it is not a dependency vulnerability scanner and does not
  substitute for M9-002.)

## Verification

The exact commands below and their output at the time this page was written
are recorded in `.superpowers/sdd/execution-plan/task-4-report.md`, including
a known set of pre-existing findings in already-committed code this task
does not own (`cmd/nise/main.go`, `internal/recipe/recipe_test.go`,
`runtime/config/loader.go`) that `make lint` correctly reports and this task
did not silence.

```sh
make fmt-check
make vet
make lint
make test
make test-race
make generate-diff
make docs-check
make check
python3 -c "import yaml,glob;[yaml.safe_load(open(f)) for f in glob.glob('.github/workflows/*.yml')]"
grep -RnE 'uses: .*@v[0-9]' .github/workflows   # expect no output
```
