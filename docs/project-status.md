# Project Status

## Current phase

Nise & Go is in design and pre-alpha development. No supported production release exists.

The V0.1 target is frozen around one golden profile:

- Go and chi.
- PostgreSQL, pgx, sqlc, and Goose.
- SvelteKit with Svelte 5 runes.
- OpenAPI-generated server and frontend boundaries.
- One binary containing the API, worker, migrations, and static authenticated frontend.

## What “frozen” means

The authoritative OpenAPI document, pinned strict chi and TypeScript
generation, explicit server adapter, lightweight browser client, and
`/api/v1` routing boundary are implemented. Strict decoding, Problem Details,
response shapes, pagination, and idempotency remain in progress; this is not
yet a complete public API surface.

V0.1 may implement and correct the documented foundation. It may not add another router, database, frontend framework, plugin system, AI integration, billing system, or deployment platform merely because it could be useful someday.

An addition must either:

1. Be required for a documented V0.1 capability to work safely; or
2. Replace an existing scope item.

## Validation path

1. Implement the deterministic CLI and golden-profile foundation.
2. Prove the complete surface through a realistic reference business application.
3. Freeze framework features.
4. Build POS Kosova against Nise.
5. Correct deficiencies proven by the real application.
6. Validate with a smaller second application.
7. Complete a real upgrade cycle before `1.0`.

## Unsupported claims

Until evidence exists, Nise must not claim to be:

- Production ready.
- Audited.
- The fastest Go framework.
- Maximally secure.
- Backward compatible.
- Suitable for a particular compliance regime.
