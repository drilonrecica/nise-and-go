# runtime

The Go packages generated applications import. This is the only part of the repository with a public Go API. It is not yet stable: `0.x` is explicitly unstable, and breaking changes are permitted in any `0.x` release (see Rules below and [docs/versioning.md](../docs/versioning.md)).

The package list, the import rules, and the stability policy are [ADR 0011](../docs/adr/0011-runtime-public-api.md). The index and per-package status are [docs/runtime-packages.md](../docs/runtime-packages.md). `doc.go` carries the same contract for `go doc`.

Rules:

- Keep the surface small; genuinely shared infrastructure only.
- Explicit constructors. No globals, service locator, or reflection DI.
- Every package must be usable without importing the rest of Nise. A `runtime/` package imports another only along an edge named in ADR 0011.
- Shared implementation, if any is ever needed, goes in `runtime/internal/`, which applications cannot import. No such package exists today.
- Every exported identifier carries a doc comment. Adding, removing, or retyping one is a contract change: update `docs/runtime-packages.md` and say so in the commit message.
- `0.x` is explicitly unstable; breaking changes ship with a changelog entry and migration notes. After `1.0`, changes here follow semantic versioning (`docs/versioning.md`).

`runtime` itself is documentation only and exports nothing. The six subpackages are implemented; [docs/runtime-packages.md](../docs/runtime-packages.md) is the status board.
