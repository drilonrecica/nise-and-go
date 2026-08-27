# CLI and Distribution

This page describes planned V0.1 behavior.

The public developer interface is one cross-platform binary named `nise`.

## Planned command families

```text
nise new
nise dev
nise doctor
nise generate
nise check
nise test
nise db migrate
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

- Linux, macOS, and Windows binaries.
- AMD64 and ARM64 where supported and tested.
- GoReleaser OSS through pinned GitHub Actions.
- SHA-256 checksums.
- Software bill of materials.
- GitHub build attestations.

## Updates and privacy

Nise does not self-update, collect telemetry, or perform automatic update checks. An explicit check may report instructions appropriate to the detected installation channel. The project recipe records CLI and runtime versions for compatibility checks.

## AI

Nise has no built-in AI, BYOK, model-provider adapter, or autonomous patch behavior. It may generate static architecture and agent-instruction files for use by external tools.

