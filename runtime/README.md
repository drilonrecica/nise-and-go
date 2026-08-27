# runtime

Stable Go packages that generated applications import. This is the only part of the repository with a public Go API.

The package list, the import rules, and the stability policy are [ADR 0011](../docs/adr/0011-runtime-public-api.md). The index and per-package status are [docs/runtime-packages.md](../docs/runtime-packages.md). `doc.go` carries the same contract for `go doc`.

Rules:

- Keep the surface small; genuinely shared infrastructure only.
- Explicit constructors. No globals, service locator, or reflection DI.
- Every package must be usable without importing the rest of Nise. A `runtime/` package imports another only along an edge named in ADR 0011.
- Shared implementation goes in `runtime/internal/`, which applications cannot import.
- Every exported identifier carries a doc comment. Adding, removing, or retyping one is a contract change: update `docs/runtime-packages.md` and say so in the commit message.
- After `1.0`, changes here follow semantic versioning (`docs/versioning.md`).

`runtime` itself is documentation only and exports nothing. Subpackages are populated by Slice 1 (M2-001 onward).
