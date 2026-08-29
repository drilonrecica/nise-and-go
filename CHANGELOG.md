# Changelog

All notable released changes will be documented here.

The format is based on Keep a Changelog. Tags use semantic-versioning format from the first release; compatibility guarantees begin at `1.0`. During `0.x`, breaking changes are allowed and must include migration notes.

## Unreleased

### Added

- Initial public project documentation.
- Add the public `runtime/transaction` package and generated pgx/sqlc wiring
  for use-case-owned business transactions.

## Release policy

- Entries describe user-visible changes, not every commit.
- Security fixes are described without publishing exploitable details before users can update.
- Breaking `0.x` changes include an explicit migration section.
- Release tags and artifacts published through GitHub Releases are canonical.
