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

## Release policy

- Entries describe user-visible changes, not every commit.
- Security fixes are described without publishing exploitable details before users can update.
- Breaking `0.x` changes include an explicit migration section.
- Release tags and artifacts published through GitHub Releases are canonical.
