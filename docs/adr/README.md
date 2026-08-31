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
- [0012: Dev-loop proxy topology](0012-dev-loop-proxy-topology.md)
- [0013: Security headers and Content Security Policy](0013-security-headers-and-csp.md)
- [0014: Keep sqlc configuration with each feature](0014-feature-colocated-sqlc.md)
- [0015: Keep transaction ownership in use cases](0015-use-case-owned-transactions.md)
- [0016: Authenticate pagination cursors and bind them to their query](0016-authenticated-cursor-pagination.md)
- [0017: Security primitives belong to the runtime, security policy to the application](0017-security-primitives-in-runtime.md)
- [0018: Development uses the same hardened session cookie as production](0018-development-cookie-policy.md)
- [0019: Trust a declared proxy chain, counted from the right](0019-trusted-proxy-configuration.md)
- [0020: Refuse common passwords locally; never ship a remote checker](0020-compromised-password-source.md)
- [0021: Require a recent proof for actions that widen access, and for nothing else](0021-reauthentication-matrix.md)
- [0022: A compile-time module is a framework package plus conditional templates](0022-compile-time-modules.md)
- [0023: Second-factor design — HMAC-SHA1, a stored secret, and a single-use challenge](0023-second-factor-design.md)
- [0024: Design native device tokens now; expose nothing yet](0024-device-token-design.md)

New records copy [template.md](template.md) and use the next four-digit number. Do not rewrite an accepted ADR to hide a reversal; add a new ADR that supersedes it.
