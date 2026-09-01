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

## Performance budgets

`test/budget/` and, in generated projects, the round-trip budgets in
`internal/features/auth/budget_test.go`.

The distinction the whole thing is built around is **what a shared runner can
reproduce**.

Wall-clock time cannot. A GitHub-hosted runner shares a machine, its CPU
frequency is not ours to know, and the same commit measured twice can differ
by a factor of three. A gate on it fails for reasons unrelated to the change —
and a gate that fails for unrelated reasons is one people learn to re-run
until it passes, which also teaches them to re-run the ones that matter.

Allocation counts, byte sizes, and database round trips **are** reproducible.
They are properties of the code rather than of the machine.

| Budget | Gates |
|---|---|
| Allocations per call | The paths every request touches: permission checks, session token parsing, cursor encode/decode, log redaction |
| Generated tree size | File count and total bytes, per recipe, plus a bound on any single file |
| Generated tree shape | The set of top-level entries — every one is a claim about what a project is |
| Database round trips | Named reference flows: issuing a session, authenticating one |

Round trips are the number that goes wrong quietly. A loop reading one row per
iteration is indistinguishable from a batch read in any test that only checks
the result, and the difference between them is a page that loads and a page
that times out on real data.

Everything else — latency, throughput, memory under load — is measured and
recorded in [performance baselines](performance-baselines.md) and reviewed by
a person.

### The check on the check

`TestABudgetActuallyCounts` exists because the round-trip budgets counted
**zero** when they were first written: the test harness opened its pool
without a query tracer, so every budget passed at any cost and nothing else in
the file noticed.

Any test that asserts a threshold needs one of these. A gate that observes
nothing passes exactly as quietly as a gate that is satisfied.

### Raising a budget

Each is set just above what is measured, deliberately — a budget with room for
a doubling is not a budget, it is a note about the past. Exceeding one is not
automatically a bug; it is a fact that needs a sentence. Raise the number in
the same commit as the change, and say what the change bought.

## Fuzzing

`make fuzz` runs every `Fuzz*` target in the repository for `FUZZTIME` each
(30 seconds in CI, `make fuzz FUZZTIME=10m` when somebody means it).

| Target | Package | What it protects |
|---|---|---|
| `FuzzParseToken` | `runtime/session` | Session token parsing — the value every authenticated request carries |
| `FuzzCursorDecode` | `runtime/pagination` | Cursor decoding, which is signed data arriving from a client |
| `FuzzBindingIsInjective` | `runtime/pagination` | The filter fingerprint a cursor is bound to |
| `FuzzParsePageRejectsWithoutPanicking` | `runtime/pagination` | Every list query parameter |

Generated projects carry their own, over the same surfaces plus what they add:
`FuzzDecode` (the strict JSON body decoder), `FuzzTransportSurface`,
`FuzzValidateKey` and `FuzzFingerprint` (idempotency), and
`FuzzValidateKeyNeverAcceptsAnEscape` and
`FuzzSanitizeFilenameAlwaysProducesOneUsableComponent` (upload metadata).

### They assert properties, not examples

`FuzzBindingIsInjective` does not check that a particular filter set produces
a particular hash. It checks that the same filters always fingerprint the
same way, that different resources never share a fingerprint, and that adding
a filter always changes one — which is what a cursor's binding actually rests
on, and what a naive concatenation gets wrong (`status=open` and
`statu=sopen` encode identically without length prefixes).

`FuzzSanitizeFilenameAlwaysProducesOneUsableComponent` checks that the output
is never empty, spans exactly one path component, and is **idempotent**. That
last property found a real bug within seconds: the function trimmed `._-` and
*then* truncated to 200 bytes, so a cut landing on a separator produced a name
ending in `-` or `.` — a value that differed from what sanitizing it again
would give, and, with a trailing dot, not a legal Windows filename.

Round-trip and idempotence properties are what make a fuzz target find things
a table of examples never will.

### A crash becomes a permanent test case

A failure writes a corpus file into `testdata/fuzz/<Target>/`. It is printed
by the failure, and it must be committed alongside the fix — the input that
found the bug then runs as an ordinary test case under `make test` forever.

## Security scanning

`.github/workflows/security.yml`, and `make security` locally. Three scans,
and the split between them is deliberate.

| Target | Tool | Answers |
|---|---|---|
| `make vulncheck` | `govulncheck` | **Are we exploitable?** It resolves call graphs, so an advisory in a function nothing calls is informational rather than a failure. |
| `make osv` | `osv-scanner` | **Are we shipping something known-bad?** Everything required, called or not, across advisory sources beyond Go's own database. |
| `make secrets` | `gitleaks` | **Did we ever commit a credential?** The whole history, because a secret committed and then deleted is in every clone. |

The two vulnerability scanners run together rather than one instead of the
other. Precision without breadth misses a dependency that is bad but currently
unreached; breadth without precision fails on every advisory in the tree,
which produces a scanner whose output gets ignored.

### It runs on a schedule as well as on push

A vulnerability database changes without this repository changing. Scanned
only on push, a stable component learns about a new advisory the next time
somebody happens to commit — which can be months. The weekly run is what turns
"we scan our dependencies" into a claim about time.

### The tools are installed through the module proxy

All three are `go install` of a pinned version, not third-party actions. That
is one fewer workflow to pin by SHA and audit, the proxy checksum-verifies
what it fetches, and it is the mechanism the golangci-lint install already
uses.

### The gitleaks allowlist

Every entry in `.gitleaks.toml` names a file and says why its contents cannot
be a credential — the secret detector's own test fixtures, RFC 6455's example
`Sec-WebSocket-Key`, an idempotency key. An allowlist entry with no reason is
a hole somebody widened once and nobody can safely narrow again.

It also adds one rule gitleaks does not ship: a PostgreSQL connection string
with an inline password, which is the shape most likely to be committed here
by accident. That rule cannot distinguish a placeholder from a credential by
shape, so the placeholders are allowlisted by value — `password`, `secret`,
`s3cr3t`, `changeme`, `REDACTED`. A real pasted credential will not be one of
those words, which was checked by committing one to a scratch repository and
confirming it fires.

`--redact` is passed, so a finding does not put the secret it found into a
public log. The file and line are enough to act on; printing the value would
turn every true positive into a second leak.

## Releases

`.github/workflows/release.yml` runs on a `v*` tag. It runs `make check`
first — a tag can point at any object in history, including one CI never saw,
and "the tests passed on some ancestor" is not the claim a release makes —
then cross-compiles the six release binaries with GoReleaser and creates a
**draft** release. Nothing is installable until a human has looked at the
archives and the notes.

It is a separate workflow from `ci.yml` because it is triggered by something
different and needs `contents: write`, which no other job here has. GoReleaser
is installed through the module proxy at a pinned version rather than through
its own action, for the same reason as the scanners in `security.yml`: one
fewer third-party action to pin by SHA.

`workflow_dispatch` builds everything and publishes nothing, so the workflow
itself can be changed and proven without cutting a release.

Locally:

```sh
make release-check     # validate .goreleaser.yaml
make release-snapshot  # cross-compile all six, publish nothing
```

### What a release publishes about itself

Three artifacts, answering three different questions:

| Artifact | Answers | What it cannot answer |
|---|---|---|
| `checksums.txt` | Is this the file that was published? | Who published it — it sits beside the artifacts, and whoever can replace one can replace both |
| A GitHub build attestation, per file | Who built it, from which commit and workflow? | Whether that workflow was honest |
| `<archive>.spdx.json` | What is compiled into it? | Whether any of it is safe |

The attestation is keyless: signed with a short-lived certificate minted for
one workflow run and recorded in a public transparency log, so there is no
signing key for this project to hold, lose, or rotate. It covers the archives,
the SBOMs, **and** `checksums.txt` — a checksum file nobody attested is one
somebody can substitute along with the artifacts it covers.

SBOMs come from syft, pinned in the workflow. If syft were absent GoReleaser
would skip the SBOM step silently, which is why `test/release` asserts the
install step exists rather than only the configuration block.

`test/release` checks that the configuration, `docs/supported-platforms.md`,
the Makefile pin, and the workflow pin all agree; that the release stamps the
three variables `internal/version` reads; that all three artifact kinds are
produced and attested; and that every action in every workflow is pinned to a
full commit SHA, first-party ones included — "first-party" describes who wrote
an action, not what a moved tag would run.

### Validating a published release

Everything above checks an *input*. `ci.yml` checks the commit,
`goreleaser check` checks the configuration, `test/release` checks that the
configuration and the documentation agree. Nothing there checks the output.

Between "GoReleaser exited 0" and "a stranger downloads a file" there is an
upload, a draft, a human pressing publish, and a set of release assets that
anybody with write access can add to or delete from. So
`.github/workflows/release-validate.yml` runs on the publish click and reads
what that stranger would actually get:

| Check | The failure it catches |
|---|---|
| Every asset's build attestation | An asset substituted or added after the build |
| The asset set is exactly 6 archives + 6 SBOMs + `checksums.txt` | A platform whose users cannot install; a file the workflow did not build |
| `checksums.txt` covers every artifact, and only those | A subset file, under which one unlisted archive passes `sha256sum --check` untouched |
| Every published hash, recomputed | A truncated or corrupted upload |
| Each archive holds exactly the binary, `LICENSE`, `README.md` | An archive that unpacks to the wrong thing, or to something extra |
| The binary carries an executable mode | An install that produces a file nobody can run |
| `nise --json version` against the tag and the tag's commit | A binary built from the wrong commit, or with no `ldflags` applied |
| `nise new` with the released binary | A perfectly checksummed file that is not a working CLI |

The attestation is verified **first**, and `test/release` pins that order by
position rather than by presence — both steps existing in the wrong order is
the bug, and a test for presence alone passes on it.

The work is done by `cmd/release-validate`, ordinary tested Go in this
repository rather than shell in a YAML file. It is hermetic: no network, no
toolchain, no subprocess, just files on disk. That is what lets its tests
build a release, break it one way at a time, and assert the exact finding —
including the failure that matters most, which is a validator that *could not
look* reporting nothing wrong. Being unable to look exits 2; a broken release
exits 1.

### Rollback validation

"Roll back to the previous version" is a promise the release notes make and
nothing keeps. The previous release's assets can be deleted, replaced, or
converted back to a draft, and none of that is visible until somebody needs
to roll back — which is, by definition, a bad moment to find out.

So the validation workflow runs over **two** releases as a matrix: the one
just published, and the most recent published release that is neither a draft
nor a prerelease. Rolling back onto a release candidate is not a rollback
anybody wants, and a draft's assets are not public at all. The matrix is
`fail-fast: false`, because a rollback target that broke is worth knowing
about even when the release that just went out is perfect.

A repository with one release has no rollback target. That is stated in the
log and validated as a single tag, rather than failing.

`workflow_dispatch` validates any published release on demand: a rollback
rehearsal before you need one, a retry after an unrelated failure, or a check
that a release from a year ago is still installable.

Rolling the Homebrew tap back is a separate, deliberate operation — see the
tap section below and
[cli-and-distribution.md](cli-and-distribution.md#going-back-to-an-earlier-version).

### The Homebrew tap

`.github/workflows/homebrew.yml` updates the official tap
([drilonrecica/homebrew-tap](https://github.com/drilonrecica/homebrew-tap))
when a release is **published** — the human's publish click on the draft, not
the tag push. A formula generated at tag time would point at download URLs
that do not exist until the draft is published, which is also why the tap is
not updated by GoReleaser's own Homebrew publishing: that runs during the tag
build, while the release is still a draft.

The formula is generated from the release's canonical artifacts rather than a
rebuild: the workflow downloads the published `checksums.txt`, verifies its
build attestation with `gh attestation verify`, and only then runs
`cmd/homebrew-formula`, which embeds those exact hashes. brew re-verifies
every archive it downloads against them, so attesting that one file
transitively covers all four archives the formula installs — and a formula
can never be generated from artifacts the release workflow did not produce.

The generator is ordinary tested Go in this repository. It writes the same
bytes for the same inputs, refuses prerelease tags (the tap is what
`brew install` gives everybody), refuses a `checksums.txt` missing any of the
four Homebrew platforms, and refuses to silently move the tap to an older
version — rolling back is a real operation, spelled `workflow_dispatch` with
`allow-downgrade`, so it only happens when somebody means it. The
`workflow_dispatch` path also retries a failed update against any
already-published release.

Pushing to the tap requires a `HOMEBREW_TAP_TOKEN` repository secret: a
fine-grained personal access token with contents read/write on
`drilonrecica/homebrew-tap` and nothing else. The default `GITHUB_TOKEN`
cannot write to another repository, and scoping the PAT to one repository's
contents means a leaked token cannot touch this repository, its releases, or
anything beyond the tap — whose every change is a public commit anyway.

`test/release` pins the properties that make this safe: the workflow triggers
on publish and never on a tag push, `.goreleaser.yaml` declares no
`brews`/`homebrew_casks` section, drafts and prereleases are refused on the
manual path too, and the attestation is verified before the generator runs.

## Installation channels

`.github/workflows/install-channels.yml` installs a published release the way
three different people would, on every platform that channel serves:

| Channel | Runners | Toolchain on the runner |
|---|---|---|
| Release archive + `sha256sum --check` | Linux, macOS, Windows | none, deliberately |
| `go install …/cmd/nise@vX.Y.Z` | Linux, macOS, Windows | Go |
| `brew install drilonrecica/tap/nise` | Linux, macOS | none |

The archive jobs set up **no** Go toolchain. A release binary that needs one
to run is not a release binary, and the only way to test that is not to have
one. Every channel then creates a project with what it installed — installing
is not the claim; working is.

### The failure this is really for

A broken channel is loud. The quiet failure is two channels that both work and
**disagree**.

They can, because they are three independent paths to one version string. The
archive binary is stamped by the release build's `-ldflags`. The `go install`
binary is compiled on the installing machine from a module zip and reads its
version out of `runtime/debug.BuildInfo`. Homebrew installs the archive, but
through a formula generated by a separate workflow that could point at the
wrong release.

That is not cosmetic. `nise new` writes the running binary's version into the
project recipe verbatim, and `nise doctor` compares it later — so two channels
naming one release differently make the same release generate two different
project trees.

This was a real defect, found by testing the channels rather than by reading
them. GoReleaser's `{{ .Version }}` is the tag with its `v` stripped, which is
correct for an archive's file name and wrong for the version a binary reports:
the archive channel said `0.1.0` and `go install` said `v0.1.0`. The release
now stamps `v{{ .Version }}`, matching what `go install` reports and what
`internal/version` documents, and `test/release` pins both halves — the stamp
carries the `v`, and the archive names still do not.

`cmd/channel-agreement` does the comparison: every channel must report the tag
exactly, and the channels that install the release archive must additionally
agree with each other on the **commit**. That last check is what catches a
stale Homebrew formula still pointing at the previous release — invisible from
inside the channel, which installs, runs, and reports a perfectly good version.

A channel that reported nothing is a failure, not one fewer thing to compare;
otherwise the comparison would pass most loudly when the most was broken. The
agreement job therefore runs `if: always()`.

### What the channels legitimately do not share

`go install` reports no commit and no build date. A module zip carries no VCS
metadata, so the Go toolchain has nothing to record — this is a property of the
channel, not a defect, and `cmd/channel-agreement` encodes it as such rather
than tolerating it silently. Provenance for that channel comes from the module
proxy's checksum database instead.

### It runs on a schedule as well as on publish

A channel does not only break at release time. The tap is another repository
and the module proxy is somebody else's cache; either can stop serving a
release that worked on the day it was published. The scheduled run installs
whatever is currently the latest published release — which is what somebody
following the documentation today would get.

## Branch and tag protection

The rules that decide what may reach the default branch, and whether a
published tag can move, are committed as GitHub ruleset exports under
`.github/rulesets/` — a branch-protection setting that lives only in a web form
is a claim nobody can review, and one nobody notices being turned off.

`test/release` reads them and fails when the eight required status checks stop
matching `ci.yml`'s jobs. That is the drift that actually happens: a job is
added to CI, nobody adds it to the ruleset, and it silently stops gating
anything. It also refuses a bypass actor (a hole in every rule beneath it) and
a non-strict status-check policy (under which a branch that passed CI *before*
an incompatible change landed can still merge).

Tags are protected against deletion and against being **moved**, which is the
classic supply-chain attack on a Go project: `go install …@v0.1.0` resolves a
tag, and a tag moved after publication serves different code under a version
people have already audited.

Nothing in this repository can prove a ruleset is active on GitHub. See
[repository-security.md](repository-security.md) for how to check that, for
what each release control actually withstands, and for the maintainer-recovery
procedure — including the part that has to be said plainly rather than implied
away.

## What CI deliberately does not do

- **No cross-platform test execution beyond three runners.** The release
  builds six platform pairs; CI runs tests on three of them. See
  [supported-platforms.md](supported-platforms.md).
- **No frontend build or test job.** See "Frontend targets are not wired in
  yet" above.

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
