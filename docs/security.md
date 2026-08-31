# Security Model

This document records the planned baseline, not an audit or guarantee.

## Authentication

- Opaque sessions stored in PostgreSQL. Implemented; see [sessions](sessions.md). Only the token digest is stored, the absolute deadline is fixed at issue, and revocation is immediate.
- Browser identifier in a `__Host-` prefixed Secure, HttpOnly, SameSite=Lax cookie.
- Session-bound CSRF token plus Origin and Fetch Metadata checks. Implemented; see [sessions](sessions.md). The token is derived from the session token, so it has no separate key, column, or lifetime, and all three checks must pass for a state-changing request.
- Future native clients use opaque, hashed, revocable bearer device tokens—not JWTs.
- Argon2id password hashing with versioned, benchmarked parameters and rehash-on-login, plus a length-only policy with a local known-compromised check. Implemented; see [password hashing](passwords.md). The parameter history is application-owned Go code, retiring a set is explicit, and the unknown-account login path pays the same cost as a real one.
- Closed registration by default through administrators or invitations. Implemented; see [enrollment](enrollment.md). Acceptance is one transaction, every unusable-invitation reason is the same error, and the first-administrator bootstrap withdraws earlier links so only the newest works.
- Optional TOTP and recovery codes. Implemented as a compile-time module; see [second factor](second-factor.md). A correct password produces a single-use challenge rather than a session, an accepted code's step is stored so it cannot be replayed, and recovery codes are single-use digests replaced as a set.
- Default session policy of 12 hours idle and 30 days absolute, configurable, with reauthentication for sensitive actions. Implemented; see [reauthentication](reauthentication.md). The proof is stored per session, an undeclared action denies, and reauthenticating shares the sign-in throttle.

## Authorization

- Granular permissions are the primitive. Implemented; see [authorization](authorization.md). The catalog is closed, roles may only bundle declared permissions, there are no wildcards, and nothing lets a check ask about a role.
- Roles are named permission bundles.
- Policies default to deny. Implemented: the zero permission set holds nothing, an unresolved request holds nothing, and requiring an empty permission list is a denial.
- Use cases enforce authorization; hidden buttons are not security controls. Implemented; see [authorization](authorization.md). `Require` takes a context rather than a permission set, denies on every unresolved path, and a resolution failure denies rather than erroring.
- Sensitive actions may require recent reauthentication and an audited reason. Reauthentication implemented; see [reauthentication](reauthentication.md). The closed matrix covers only actions that create or widen access — withdrawal is deliberately never gated, because the withdrawal is the response to a compromise.

## Tenant isolation

The optional organization module combines application checks with PostgreSQL RLS. The application role, connection-pool behavior, job tenant context, and administrative bypass path require adversarial integration tests.

## Input and abuse resistance

- Strict request decoding and controlled size limits.
- Layered throttling for authentication and sensitive endpoints. Implemented for authentication; see [throttling](throttling.md). The check runs before password hashing, both buckets are always counted, and refused attempts still cost a slot.
- Generic authentication failures. Implemented: one error for no account, wrong password, and disabled account, with the distinguishing outcome returned only for the audit record.
- Quarantine and validation for uploads.
- Central redaction for credentials, cookies, tokens, and sensitive fields.
- Separate persistent audit records and operational logs. Implemented; see [audit log](audit.md). The table is append-only, its retention sweep cannot be pointed at anything newer than 30 days, and a detail key the logger would redact is refused outright.
- Security headers and a Content Security Policy for the embedded frontend are part of the V0.1 baseline; the exact policy is documented once the frontend build stabilizes.

## Supply chain

- Pinned tools and workflows.
- Locked Go and JavaScript dependencies.
- `govulncheck`, OSV scanning, and secret scanning.
- Release checksums, SBOMs, and build attestations.

## Operator responsibility

Nise cannot compensate for leaked secrets, exposed databases, missing updates, untested backups, permissive proxy, CDN, or firewall rules, insecure custom code, or incorrect authorization policies. Each real application requires its own threat model.

