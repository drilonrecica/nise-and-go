# Security Model

This document records the planned baseline, not an audit or guarantee.

## Authentication

- Opaque sessions stored in PostgreSQL. Implemented; see [sessions](sessions.md). Only the token digest is stored, the absolute deadline is fixed at issue, and revocation is immediate.
- Browser identifier in a `__Host-` prefixed Secure, HttpOnly, SameSite=Lax cookie.
- Session-bound CSRF token plus Origin and Fetch Metadata checks. Implemented; see [sessions](sessions.md). The token is derived from the session token, so it has no separate key, column, or lifetime, and all three checks must pass for a state-changing request.
- Future native clients use opaque, hashed, revocable bearer device tokens—not JWTs.
- Argon2id password hashing with versioned, benchmarked parameters and rehash-on-login. Implemented; see [password hashing](passwords.md). The parameter history is application-owned Go code, retiring a set is explicit, and the unknown-account login path pays the same cost as a real one.
- Closed registration by default through administrators or invitations.
- Optional TOTP and recovery codes.
- Planned default session policy of roughly 12 hours idle and 30 days absolute, configurable, with reauthentication for sensitive actions.

## Authorization

- Granular permissions are the primitive.
- Roles are named permission bundles.
- Policies default to deny.
- Use cases enforce authorization; hidden buttons are not security controls.
- Sensitive actions may require recent reauthentication and an audited reason.

## Tenant isolation

The optional organization module combines application checks with PostgreSQL RLS. The application role, connection-pool behavior, job tenant context, and administrative bypass path require adversarial integration tests.

## Input and abuse resistance

- Strict request decoding and controlled size limits.
- Layered throttling for authentication and sensitive endpoints.
- Generic authentication failures.
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

