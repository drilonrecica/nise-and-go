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
