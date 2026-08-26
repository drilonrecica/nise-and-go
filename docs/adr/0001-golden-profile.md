# ADR 0001: Adopt One V1 Golden Profile

- **Status:** Accepted
- **Date:** 2026-08-26

## Context

Supporting multiple routers, databases, frontend modes, and deployment targets would multiply templates, tests, documentation, security paths, and upgrade combinations before the core product is proven.

## Decision

V1 supports Go, chi, PostgreSQL, pgx, sqlc, Goose, and SvelteKit with Svelte 5. The authenticated frontend is statically built and embedded into one Go binary.

Gin, PocketBase, SQLite, HTMX, and Kubernetes are outside the V1 profile.

## Consequences

- The golden path can be deep, tested, and documented.
- Users outside the profile must adapt the output or use another tool.
- A later profile must be a coherent tested product, not a set of arbitrary toggles.

