# CLI and Distribution

This page describes planned V0.1 behavior.

The public developer interface is one cross-platform binary named `nise`.

## Planned command families

```text
nise new
nise dev
nise db status
nise db migrate
nise doctor
nise generate
nise check
nise test
nise db backup
nise db restore
nise db verify-backup
nise upgrade
```

Exact commands may change during `0.x`.

## Output behavior

Interactive terminals may use restrained ASCII branding, color, spinners, and short animations. Non-interactive output is immediate and deterministic. The CLI will provide no-color and machine-readable modes, never hide actionable errors behind effects, and never print secrets.

## Canonical releases

GitHub Releases is the canonical artifact source. Planned supported installation paths are:

1. Precompiled release archives.
2. Versioned `go install github.com/drilonrecica/nise-and-go/cmd/nise@vX.Y.Z`.
3. An official Homebrew tap.

The Go module path is `github.com/drilonrecica/nise-and-go` ([ADR 0007](adr/0007-module-path-and-owner.md)).

Nise will not be published as an npm wrapper. V0.1 will not provide a `curl | sh` installer.

## Release artifacts

Six archives per release — Linux, macOS, and Windows, each on amd64 and arm64
— plus a `checksums.txt` covering all of them:

```
nise_v0.1.0_linux_amd64.tar.gz
nise_v0.1.0_linux_arm64.tar.gz
nise_v0.1.0_darwin_amd64.tar.gz
nise_v0.1.0_darwin_arm64.tar.gz
nise_v0.1.0_windows_amd64.zip
nise_v0.1.0_windows_arm64.zip
checksums.txt
```

Each archive holds the binary, `LICENSE`, and `README.md`. Windows gets a zip
because a `.tar.gz` there is an invitation to install something to open it.

Every binary is static (`CGO_ENABLED=0`), so it runs on a distribution older
than the one that built it and needs no system library at the destination. All
six are built with `-trimpath` and the commit's timestamp rather than the
build's, which makes two builds of one tag byte-identical — the property that
makes a checksum somebody else computes worth anything.

Built by GoReleaser at a pinned version, installed through the Go module proxy
rather than through a third-party action. See
[checks.md](checks.md#releases) for the workflow and
[supported-platforms.md](supported-platforms.md) for the platform table.

Software bills of materials and build attestations are M10-006.

## Verifying a download

```sh
sha256sum --check --ignore-missing checksums.txt
```

## Updates and privacy

Nise does not self-update, collect telemetry, or perform automatic update checks. An explicit check may report instructions appropriate to the detected installation channel. The project recipe records CLI and runtime versions for compatibility checks.

## AI

Nise has no built-in AI, BYOK, model-provider adapter, or autonomous patch behavior. It may generate static architecture and agent-instruction files for use by external tools.
