# `nise test`

`nise test` runs a generated project's own Go, frontend unit/component, and
end-to-end test suites, without inventing a command a project did not declare.

```
$ nise test
$ nise test --go
$ nise test --go --e2e
$ nise test --json
$ nise test --go -- -run TestOrderTotals ./internal/features/invoice/...
```

See [CLI output contract](../cli-output.md) for `--json`, `--verbose`,
`--quiet`, the exit-code contract, and the `--` passthrough behavior this
command relies on.

## Where it looks

`nise test` looks for `nise.json` in the current directory or any
parent — the same project-root convention `nise doctor` and `nise agents`
use — and operates on that project's root from there.

## Suites and subset selection

| Flag | Selects |
|---|---|
| (none) | The Go suite and the frontend unit/component suite. |
| `--go` | The Go suite. |
| `--component` | The frontend unit/component suite. |
| `--e2e` | The end-to-end suite. |

Passing any of `--go`, `--component`, or `--e2e` selects **exactly** the
suites named, with no suite added implicitly alongside them —
`nise test --e2e` runs only the end-to-end suite.

**Why the bare command runs Go and frontend, not end-to-end:** the Go suite
and generated Vitest API-client suite are both hermetic—no network or running
application—and fast enough for a tight local edit/test loop. M6 may run the
same frontend selection in Vitest browser mode. The end-to-end suite spins an actual browser
against a real running application; it is slower and more prone to
environmental flakiness, so it is opt-in via `--e2e` rather than part of
every plain `nise test`.

### How each suite is detected — never invented

`nise test` only ever runs a command the project itself declared. A suite
that is not present is reported `skipped`, with the exact reason, not run
and not treated as a failure:

- **Go**: runs when the project root has a `go.mod`, as
  `go test ./cmd/... ./db/... ./internal/...` — exactly the generated `Makefile`'s
  `GO_PACKAGES`, and the target list
  [Generated application layout](../generated-application-layout.md)'s
  own "Go targets" section prescribes, since `frontend/` sits inside the
  same Go module and a bare `./...` would also walk
  `frontend/node_modules/`. The `db` package's source/embed parity test is
  therefore part of every standard Go suite.
- **Frontend/component**: runs when `frontend/package.json` exists and its
  `scripts` object declares one of `test:unit` or `test:component` (the
  first one found, in that order), as `pnpm --dir frontend run <script>`.
- **End-to-end**: runs when `frontend/package.json` declares one of
  `test:e2e` or `e2e`, as `pnpm --dir frontend run <script>`.

`test:unit` is present in current generated projects and runs the Node-only
typed-client contract. `test:component`, `test:e2e`, and `e2e` remain the
documented seams M6 uses when browser component and Playwright suites land.

## Passing arguments through to a suite (`--`)

A literal `--` on the command line stops `nise` from recognizing any
further global flag, and everything after it reaches `nise test`
untouched (see [CLI output contract](../cli-output.md#-stops-global-flag-recognition)).
`nise test` appends whatever survives after `--` to the argv of every
suite it actually runs in that invocation:

```
$ nise test --go -- -run TestOrderTotals ./internal/features/invoice/...
```

runs `go test ./cmd/... ./db/... ./internal/... -run TestOrderTotals
./internal/features/invoice/...`. If more than one suite is selected in
the same invocation, the same trailing arguments are appended to each
one's own command — a Go-specific flag like `-run` passed alongside
`--component` would simply be rejected by `pnpm`/`vitest` the way any
tool rejects a flag it does not understand; scope the trailing arguments
to a single `--go`/`--component`/`--e2e` invocation to avoid that.

## Output

**Human mode** streams each running suite's own stdout and stderr through
unchanged, line by line, as the suite runs, followed by one summary line
per selected suite:

```
go test: ok  	fixture/internal/x	0.002s
[PASSED] go (go test ./cmd/... ./db/... ./internal/..., 0.31s)
[PASSED] component (pnpm --dir frontend run test:unit, 0.42s)
```

**`--json` mode** never interleaves a suite's raw output into the JSON
stream (a child's own stdout is, after all, whatever that child felt like
printing — not itself valid JSON). It emits exactly one structured
summary document instead:

```json
{"suites":[{"name":"go","status":"passed","command":"go test ./cmd/... ./db/... ./internal/...","durationSeconds":0.31,"exitCode":0},{"name":"component","status":"passed","command":"pnpm --dir frontend run test:unit","durationSeconds":0.42,"exitCode":0}]}
```

`status` is one of `passed`, `failed`, or `skipped`. `reason` is present
only for `skipped`. `command`, `durationSeconds`, and `exitCode` are
present only for a suite that actually ran.

## Exit codes and failure reporting

Every selected suite always runs (or is evaluated for skipping) — a
failing suite never stops later suites from being attempted, and every
suite's result is always reported, in both modes. The overall exit code:

| Situation | Exit code |
|---|---|
| Every selected suite passed or was skipped | `0` |
| One or more selected suites failed | `1` (`ExitError`); the error names the **first** suite that failed, in run order (Go, then component, then end-to-end) |
| No `nise.json` found, or it does not parse | `3` (`ExitPrecondition`) |
| The run was interrupted (see below) | `1` (`ExitError`), code `test.interrupted` |

## Signal handling

`nise test` installs its own `SIGINT` (Ctrl-C) handling for the duration
of the run. A currently running suite's child process is killed
immediately — it is never left running after `nise test` itself exits —
and any suite that had not started yet is reported `skipped` with the
reason "interrupted before this suite started", rather than silently
omitted. A second Ctrl-C after `nise test` has already finished handling
the first behaves normally (restores the default terminate-the-process
disposition), so an unresponsive nise process is never a way to get stuck
past a single Ctrl-C.

## Safety notes

- Generated PostgreSQL integration tests use the isolated `dbtest` harness.
  Set `TEST_DATABASE_URL` to an administrative database on disposable test
  infrastructure; absence skips locally and fails under `CI=true` or `CI=1`.
  See [PostgreSQL integration testing](../database-testing.md).
- Every suite's command is one of this file's own fixed argv shapes
  (`go test <fixed targets>` or `pnpm --dir frontend run <script name
  read from package.json>`); no shell is invoked, and no part of the
  command line is built by string-concatenating untrusted input.
- `nise test` performs no network request of its own; whatever a suite's
  own tooling does (for example `pnpm` resolving packages, or an
  end-to-end suite hitting a locally running server) is that tool's own
  behavior, unrelated to `nise test` itself.
