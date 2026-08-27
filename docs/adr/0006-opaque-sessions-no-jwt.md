# ADR 0006: Use Opaque Server-Side Sessions, Not JWTs

- **Status:** Accepted
- **Date:** 2026-08-27

## Context

Stateless JWT sessions avoid a database lookup per request but make revocation, rotation, and idle timeouts difficult and push sensitive claims into the client. Nise applications already require PostgreSQL on every request path.

## Decision

Sessions are opaque identifiers stored in PostgreSQL; only token hashes are stored server-side. Browsers receive the identifier in a `__Host-` prefixed, Secure, HttpOnly, SameSite=Lax cookie. Future native clients use opaque, hashed, revocable bearer device tokens against the same session model. Nise does not issue or validate JWTs for sessions.

Sessions are rotated on privilege change, revocable individually and in bulk, and bounded by configurable idle and absolute limits.

## Consequences

- Revocation and rotation are immediate and trivial.
- Authentication availability depends on PostgreSQL, which the application already requires.
- Horizontal scaling shares session state through the database rather than through signed tokens.
- Revisit only if an application needs sessions validated without database access.
