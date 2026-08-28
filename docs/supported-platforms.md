# Supported Platforms

This page distinguishes three different questions that are easy to conflate:
what the `nise` CLI is *released* for, what developing Nise itself is
*supported* on, and what CI actually *exercises* on every push and pull
request. A platform only belongs in the third list if a CI job genuinely runs
on it — this page does not claim testing that does not happen.

## Release platforms (planned, M10)

The release target is Linux, macOS, and Windows, each on amd64 and arm64 —
six binaries per release:

| OS | amd64 | arm64 |
|---|---|---|
| Linux | yes | yes |
| macOS | yes | yes |
| Windows | yes | yes |

No release workflow exists yet. Building and publishing these six binaries is
M10 scope; see [checks.md](checks.md) for why `ci.yml` deliberately does not
attempt it.

## Development-supported platforms

Building and testing Nise itself — not a generated application — is supported
on Linux, macOS, and Windows:

- The pinned toolchain (Go, Node, pnpm; see [toolchain.md](toolchain.md)) runs
  natively on all three.
- `nise dev` requires a container runtime for PostgreSQL, which is the one
  supporting service the development loop does not run on the host.
  Docker and Podman both satisfy this on Linux; Docker Desktop satisfies it on
  macOS and Windows. Nise does not pin a specific container runtime version
  (see toolchain.md).
- The maintainer's day-to-day development environment is Linux — it is the
  environment the exact verified versions in `docs/toolchain.md` (Go 1.26.5,
  Node 22.22.2, pnpm 10.33.0, Docker 29.6.2, Podman 5.8.4) were captured on —
  but Linux is not the *only* supported development platform. macOS and
  Windows contributors are expected to work, subject to the CI caveat below:
  a contributor's own macOS or Windows machine has not been verified against
  the exact patch versions in toolchain.md the way the Linux reference
  environment has.
- The `Makefile` targets (`make check` and its parts) are written in POSIX
  `/bin/sh` with no bash-only builtins specifically so they work unmodified
  on Linux and macOS. They are not asserted to run under Windows' default
  `cmd.exe` or PowerShell; a Windows contributor uses a POSIX shell (WSL,
  Git Bash, or similar) to run `make` targets locally. This is a development-
  workflow detail, independent of Windows being a CI-exercised and
  release-target OS in its own right.

## What CI exercises

`.github/workflows/ci.yml`'s `test` job runs on exactly these GitHub-hosted
runners:

| Runner label | OS | Architecture |
|---|---|---|
| `ubuntu-latest` | Linux | amd64 |
| `macos-latest` | macOS | arm64 (Apple Silicon) |
| `windows-latest` | Windows | amd64 |

This is three of the six release-target combinations. CI does **not**
exercise Linux/arm64, macOS/amd64 (Intel), or Windows/arm64 — there is no job
that builds or runs tests on those three combinations today. If a future
release build targets one of those three and it breaks, CI as it stands would
not have caught it; that gap is closed by M10 (the release workflow), not by
this task, which is explicitly out of scope here (see checks.md).

The `lint`, `generation-diff`, and `docs-check` jobs run on `ubuntu-latest`
only: they are static-analysis and file-content checks whose result does not
depend on OS or architecture, so running them three times would not exercise
anything the single Linux run doesn't already cover.

## Verification

- `.github/workflows/ci.yml`'s `test` job `strategy.matrix.os` lists exactly
  `ubuntu-latest`, `macos-latest`, `windows-latest` — matching the table
  above.
- `make check` runs unmodified in a POSIX `/bin/sh` on Linux and macOS (the
  two platforms this task's agent had available to verify directly).
