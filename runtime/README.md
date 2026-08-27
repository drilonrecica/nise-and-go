# runtime

Stable Go packages that generated applications import. This is the only part of the repository with a public Go API.

Rules:

- Keep the surface small; genuinely shared infrastructure only.
- Explicit constructors. No globals, service locator, or reflection DI.
- Every package must be usable without importing the rest of Nise.
- After `1.0`, changes here follow semantic versioning (`docs/versioning.md`).

Populated by Slice 1 (M2-001 onward).
