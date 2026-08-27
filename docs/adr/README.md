# Architecture Decision Records

ADRs record consequential decisions, their context, and their tradeoffs. They prevent the project from repeatedly reopening settled questions without new evidence.

## Status values

- Proposed
- Accepted
- Superseded
- Rejected

## Index

- [0001: Adopt one V1 golden profile](0001-golden-profile.md)
- [0002: Require deterministic generation](0002-deterministic-generation.md)
- [0003: Keep generated application code application-owned](0003-application-ownership.md)
- [0004: Build the framework before the first production application](0004-framework-first-freeze.md)
- [0005: Embed the authenticated frontend as a static single-page application](0005-static-embedded-frontend.md)
- [0006: Use opaque server-side sessions, not JWTs](0006-opaque-sessions-no-jwt.md)
- [0007: Repository owner and Go module path](0007-module-path-and-owner.md)
- [0008: Toolchain pinning and dependency allowlist](0008-toolchain-and-dependencies.md)
- [0009: Generated application directory layout](0009-generated-application-layout.md)
- [0010: Project recipe format](0010-project-recipe-format.md)
- [0011: Runtime public API surface and stability policy](0011-runtime-public-api.md)

New records copy [template.md](template.md) and use the next four-digit number. Do not rewrite an accepted ADR to hide a reversal; add a new ADR that supersedes it.

