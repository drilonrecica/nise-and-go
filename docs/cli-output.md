# CLI output contract

This page documents the `nise` CLI's output modes, exit codes, and JSON
envelope shape — the contract every `nise` command follows, implemented in
`internal/cli` and its subpackages (`internal/cli/output`, `internal/cli/
term`, `internal/cli/clierr`). It restates [CLI and distribution](
cli-and-distribution.md)'s presentation constraints as a concrete,
implemented reference; where the two disagree, this page describes current
behavior and that page describes the plan.

## Global flags

Five flags are reserved across every command and every subcommand, and are
recognized wherever they appear in the argument list — before or after the
command name:

| Flag | Effect |
|---|---|
| `--json` | Machine-readable output only; see [JSON mode](#json-mode). |
| `--quiet` | Suppress informational/progress output. Never suppresses an error. |
| `--verbose` | Show additional detail, including the underlying error chain. |
| `--no-color` | Disable ANSI color even on an interactive, color-capable terminal. |
| `--help`, `-h` | Show help for the resolved command instead of running it. |

`--quiet` and `--verbose` together is a usage error (exit 2), not a silent
precedence — a command needs to know which one the user actually meant, and
guessing which one wins would surprise someone eventually. A command's own
flags must not reuse these five names; global flags are stripped from the
argument list before a command ever sees its own flags.

`--version` is also recognized anywhere in the argument list, as a legacy
alias for the `version` command, preserved from the CLI's original
single-command entry point. Prefer `nise version`.

## Precedence

Resolved in this order:

1. **`--quiet` + `--verbose` together** is rejected immediately as a usage
   error, before anything else runs.
2. **`--json`** selects JSON mode. In JSON mode, color and animation are
   never applied — not even if `CLICOLOR_FORCE` is set — because JSON mode
   has no prose vocabulary to color in the first place (see [Output
   modes](#output-modes)).
3. **`--no-color`** forces color off in human mode, overriding every
   environment signal including `CLICOLOR_FORCE`.
4. Absent `--no-color`, color and animation follow terminal capability
   detection (see [Terminal capability detection](#terminal-capability-
   detection)).
5. **`--quiet`** suppresses informational/progress output (`Line`,
   `Verbosef`, `Success`, `Banner`) in human mode. It never suppresses a
   command's actual result or an error.
6. **`--verbose`** shows additional detail: `Verbosef` output, and the
   underlying error chain under an error's cause and recovery action.

## Output modes

Every command prints through exactly one `internal/cli/output.Writer`,
built once per invocation for the resolved mode. There are two modes.

### Human mode (default)

Prose to stdout, errors to stderr. Color and a restrained ASCII
banner/spinner are used only when stdout is an interactive terminal,
animation is not disabled (see below), and the command is not quiet.
Non-interactive stdout (a pipe, a redirect, a file) means no animation and
no ANSI regardless of any flag, unless color is explicitly forced by
`CLICOLOR_FORCE` — animation itself is never force-enabled off a terminal,
only color is.

### JSON mode (`--json`)

Exactly one JSON document per line (JSON Lines / NDJSON), always on
**stdout** — errors included. A single `nise <cmd> --json` invocation today
emits exactly one line (its result or its error), but the one-document-
per-line shape is chosen so a future command that reports several discrete
events (for example `doctor` checking several tools) can emit one line per
event without changing the contract.

**Decision: errors are JSON documents on stdout, not stderr, in JSON
mode.** A consumer piping `nise --json <cmd> | jq` should never have to
also watch stderr to discover that the command failed — the failure is
just a JSON document with a different top-level shape
(`{"error": {...}}"` instead of the command's normal result shape). Stderr
is unused in JSON mode.

JSON mode carries no human prose: there is no JSON equivalent of `Line`,
`Success`, or `Banner`. Only a command's actual result (via `Result`) and
its error (via `Error`) produce output.

**This guarantee is structural, not conventional — no ANSI escape byte can
reach stdout in JSON mode, by construction:**

1. The JSON writer's prose methods (`Line`, `Verbosef`, `Success`,
   `Banner`) are no-ops. There is no color-formatting code in the file
   that implements JSON output at all.
2. Every document the JSON writer emits passes through a byte-level
   `stripANSI` pass immediately before it reaches stdout, deleting any ESC
   (`0x1b`) byte regardless of where it came from. This is deliberate
   defense in depth: even a hypothetical future command that mistakenly
   handed a pre-colored human string to a JSON result cannot leak it.
3. A source-scanning test (`internal/cli/output/json_source_test.go`)
   greps the JSON writer's own source file for an ANSI escape-sequence
   literal on every test run, so a future change that pastes color-
   handling code into that file fails the build's test suite immediately.
4. A runtime test (`TestJSONWriterNeverEmitsANSI`) feeds the JSON writer a
   result value with an ANSI code already embedded in a string field and
   asserts the escape byte does not survive to stdout.

Six later commands (`doctor`, `new`, `dev`, `check`, `test`, `generate`)
are expected to print through this same writer; none of them can regress
this guarantee without also defeating both of the tests above.

## Exit codes

A stable, documented contract. Scripts and CI jobs may depend on these
exact numbers; changing what an existing code means is a breaking change.

| Code | Name | Meaning |
|---|---|---|
| 0 | `ExitOK` | The command completed successfully. |
| 1 | `ExitError` | A generic failure: the command understood the request and it did not succeed, for a reason that is not a usage mistake or a missing precondition. |
| 2 | `ExitUsage` | The command line itself was invalid: an unknown command or subcommand, an unknown or malformed flag, or an incompatible flag combination (`--quiet` with `--verbose`). |
| 3 | `ExitPrecondition` | The command line was valid but something the command depends on — a required tool, a reachable database, a writable directory — was missing or unusable before the command could attempt its work. |

`nise help`, `nise <command> --help`, and a bare `nise` with no arguments
all exit 0 — they are treated as a help request, not a usage error.

## The CLI error type

Every command returns an `internal/cli/clierr.Error` instead of a bare
`error` (a bare error returned by a future command is still handled: the
dispatcher wraps it via `clierr.From`, which defaults it to `ExitError`
with a generic recovery message). An `Error` carries:

- a short **cause** (what went wrong),
- a concrete **recovery action** (what to do about it),
- an **exit code**,
- an optional **machine-readable code** more specific than the exit code
  (for example a future `doctor.missing_tool`),
- an optional **documentation reference**,
- optional **structured details** (`key: value`, redacted automatically —
  see [Secrets in error output](#secrets-in-error-output)),
- an optional **wrapped underlying error**.

`Error` implements `Unwrap() error`, so `errors.Is` and `errors.As` work
through it exactly as they would through `fmt.Errorf`'s `%w`.

### Human rendering

Cause first, then the recovery action, then — **only under `--verbose`** —
one indented line per error in the underlying chain. Never a stack trace,
by default or otherwise.

```
$ nise db migrate
could not reach the database
start postgres and retry

$ nise db migrate --verbose
could not reach the database
start postgres and retry
  dial tcp 127.0.0.1:5432: connection refused
```

### JSON rendering

A stable envelope:

```json
{
  "error": {
    "code": "precondition_error",
    "message": "could not reach the database",
    "recovery": "start postgres and retry",
    "details": { "host": "127.0.0.1" },
    "docs": "docs/cli-output.md#usage-errors",
    "chain": ["dial tcp 127.0.0.1:5432: connection refused"]
  }
}
```

`code`, `message`, and `recovery` are always present. `details` and `docs`
are omitted when not set. `chain` is omitted unless `--verbose` was given —
the same rule human mode applies, kept consistent across both modes.

### Usage errors

Unknown command, unknown subcommand, and unknown/invalid flag all produce
an `ExitUsage` error. Unknown command/subcommand names get a "did you
mean" suggestion when a registered sibling is a close match by
[Levenshtein edit distance](https://en.wikipedia.org/wiki/Levenshtein_distance)
(no dependency — a small standard-library implementation in
`internal/cli/suggest.go`), scoped to the level where the lookup failed
(a bad subcommand of `nise db` is only ever compared against `db`'s other
subcommands, not the whole top-level command list).

```
$ nise doctro
unknown command "doctro"
Did you mean "doctor"? Run "nise help" to see available commands.
```

### Secrets in error output

No secret, token, cookie value, password, or connection-string password
may appear in CLI output. `internal/cli/clierr` cannot import
`runtime/logging` — the CLI has zero runtime dependency by design, and
several commands (`doctor`, `dev`) run before any `runtime/logging`
instance would even exist — so it keeps a small, **deliberately
duplicated** copy of `runtime/logging`'s deny-fragment idea in
`internal/cli/clierr/redact.go`. Do not "fix" this duplication by adding
the `runtime/logging` import; that import is exactly what was ruled out.
Structured detail values are redacted automatically by key
(`Error.WithDetail` cannot attach a detail without going through
redaction), and a small pattern additionally strips URL userinfo (for
example the password in a `postgres://user:pass@host/db` DSN) from the
free-text underlying error chain shown under `--verbose`.

## Terminal capability detection

Detected once per invocation, in `internal/cli/term`, and passed
explicitly to the output layer — never read from a global. Nothing in
`internal/cli` calls `os.Getenv` or checks `os.Stdout` directly outside
this one detection call.

- **Interactive terminal**: `Stat().Mode()&os.ModeCharDevice != 0` on
  stdout/stderr — the standard dependency-free character-device check.
  Verified on Linux and macOS. **Not verified**: native Windows consoles
  (`cmd.exe`, legacy `conhost`). Modern Go correctly reports a real
  Windows console handle as a character device, but this has not been
  exercised against one; ANSI processing on legacy Windows consoles (as
  opposed to Windows Terminal / ConPTY, which support ANSI natively) is a
  separate, unverified concern this package does not attempt to detect.
- **`NO_COLOR`**: any non-empty value disables color, per the
  [informal standard](https://no-color.org/) — presence, not content, is
  what matters.
- **`TERM=dumb`**: disables both color and animation.
- **`CLICOLOR`**: a value other than `"0"` allows color on a terminal;
  `CLICOLOR=0` disables it even on a terminal.
- **`CLICOLOR_FORCE`**: a value other than `"0"`/empty forces color on,
  even off a terminal — the strongest environment signal, beaten only by
  an explicit `--no-color` flag or JSON mode.
- **`CI`**: any non-empty value disables animation (banner, spinner), on
  the assumption that a CI log is read as static text, not watched live.

Color precedence, highest first: `CLICOLOR_FORCE` set → `NO_COLOR` set →
`TERM=dumb` → not a terminal → `CLICOLOR=0` → default on. `--no-color`
applied by the CLI beats all of the above.

## Accessibility and reduced motion

There is currently no dedicated `NO_ANIMATION` or reduced-motion
environment variable convention this CLI recognizes. Animation (the
restrained ASCII banner and spinner) is disabled whenever any of the
following is true: stdout is not an interactive terminal, `CI` is set,
`TERM=dumb` is set, `--quiet` is given, or the mode is `--json`. A user who
wants no motion on an otherwise-interactive, non-CI terminal should pass
`--quiet`, or redirect stdout to a file.

Effects never delay completed work. The spinner's `Stop` blocks only until
its animation goroutine acknowledges a stop signal — not until the next
animation frame — before printing the final result, so a completed
operation's output is never held behind a queued frame.
