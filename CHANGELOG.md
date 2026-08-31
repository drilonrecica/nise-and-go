# Changelog

All notable released changes will be documented here.

The format is based on Keep a Changelog. Tags use semantic-versioning format from the first release; compatibility guarantees begin at `1.0`. During `0.x`, breaking changes are allowed and must include migration notes.

## Unreleased

### Added

- Initial public project documentation.
- Add the public `runtime/transaction` package and generated pgx/sqlc wiring
  for use-case-owned business transactions.
- Add the public `runtime/pagination` package: authenticated, versioned,
  query-bound cursor tokens with key rotation, and strict `limit`/`after`/
  `before` parsing. Generated applications gain the `CursorPage` OpenAPI
  contract, the `CURSOR_SIGNING_KEY`, `CURSOR_RETIRED_KEYS`, and `CURSOR_TTL`
  settings, and `invalid_pagination`/`cursor_expired` Problem types.
- Add explicit offset reporting pagination: a separate `ReportPage`
  page/size/total contract with overflow-safe parsing, a configurable offset
  ceiling, and the `report_too_deep` Problem type. It cannot be combined with
  cursor parameters.
- Add idempotency for sensitive commands: an `idempotency_keys` migration and a
  transaction-owned executor that records a response in the same transaction as
  the command, replays it byte-for-byte on retry, resolves concurrent attempts
  with an advisory lock, refuses a key reused for a different request, and
  expires records on a bounded sweep. Adds the `invalid_idempotency_key`,
  `idempotency_conflict`, and `idempotency_key_reuse` Problem types.

- Add the public `runtime/password` package: Argon2id hashing in the standard
  PHC encoding, versioned parameter sets with rehash detection and explicit
  retirement, constant-cost dummy verification for the unknown-account path,
  and a parameter benchmark. Generated applications gain an application-owned
  parameter history in `internal/platform/passwords` and a
  `<app> password benchmark` command. Adds `golang.org/x/crypto` as the first
  and only `runtime/` dependency outside the standard library.

- Add the public `runtime/session` package and the generated `auth` feature:
  opaque 32-byte session tokens stored only as SHA-256 digests, an absolute
  deadline fixed at issue, a bounded idle-window touch, revocation with a
  recorded reason, and a bounded expiry sweep. Adds the `users` and `sessions`
  migration and the first feature-colocated sqlc store.
- Add `session.CookiePolicy`. Development uses the same `__Host-`, `Secure`,
  `HttpOnly`, `SameSite=Lax` cookie as production, because `http://localhost` is
  a secure context; the single escape hatch for a non-localhost development
  origin drops the prefix and `Secure` together and cannot be used in
  production (ADR 0018).
- Add `internal/platform/httpauth`: the session cookie boundary and a resolver
  middleware that attaches a request-scoped identity without ever rejecting a
  request, clears a credential that did not resolve, and logs the refusal reason
  but never the credential. Adds the `SESSION_IDLE_TIMEOUT`,
  `SESSION_ABSOLUTE_TIMEOUT`, `SESSION_TOUCH_INTERVAL`, `SESSION_COOKIE_NAME`,
  and `SESSION_COOKIE_INSECURE` settings.
- Add the public `runtime/forwarded` package and the `TRUSTED_PROXY_COUNT` and
  `TRUSTED_PROXY_NETWORKS` settings. A declared hop count is read from the right
  of `X-Forwarded-For`, so a client-forged prefix is never read; an optional
  peer-network allowlist gates who may be a proxy; both default to trusting
  nothing, and `TRUST_PROXY_HEADERS` without a declared proxy now fails startup
  (ADR 0019).

### Fixed

- Generated applications answered a request whose HTTP method is outside chi's
  own method table — `PROPFIND`, `PURGE`, or any other unusual verb — with a
  bare `405` produced before the middleware chain ran: no security headers, no
  Content-Security-Policy, no request or correlation ID, no log line, and no
  metric. Such requests are now dispatched into the matching surface's core and
  answered with that surface's ordinary `405`. Found by the new transport fuzz
  target.

## Release policy

- Entries describe user-visible changes, not every commit.
- Security fixes are described without publishing exploitable details before users can update.
- Breaking `0.x` changes include an explicit migration section.
- Release tags and artifacts published through GitHub Releases are canonical.
