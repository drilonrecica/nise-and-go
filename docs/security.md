# Security Model

This document records the planned baseline, not an audit or guarantee.

## Authentication

- Opaque sessions stored in PostgreSQL.
- Browser identifier in a `__Host-` prefixed Secure, HttpOnly, SameSite=Lax cookie.
- Session-bound CSRF token plus Origin and Fetch Metadata checks.
- Future native clients use opaque, hashed, revocable bearer device tokens—not JWTs.
- Argon2id password hashing with versioned, benchmarked parameters and rehash-on-login.
- Closed registration by default through administrators or invitations.
- Optional TOTP and recovery codes.

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
- Separate persistent audit records and operational logs.

## Supply chain

- Pinned tools and workflows.
- Locked Go and JavaScript dependencies.
- `govulncheck`, OSV scanning, and secret scanning.
- Release checksums, SBOMs, and build attestations.

## Operator responsibility

Nise cannot compensate for leaked secrets, exposed databases, missing updates, untested backups, permissive Cloudflare or firewall rules, insecure custom code, or incorrect authorization policies. Each real application requires its own threat model.

