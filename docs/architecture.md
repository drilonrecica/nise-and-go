# Architecture

This page describes the planned V0.1 architecture.

## Deployment unit

An application builds into one Go binary containing:

- The chi HTTP API.
- Embedded Svelte static assets.
- The River background-job runner.
- Database migrations.
- Email templates and required application assets.

The same binary supports three modes:

- `all`: HTTP and jobs together for small deployments.
- `web`: HTTP only.
- `worker`: background jobs only.

## Modular monolith

Application code is organized by feature. A feature owns its handlers, use cases, queries, domain rules, and tests, plus its routes in the application's SvelteKit tree (the generated layout is documented with the first generated project). Shared infrastructure stays deliberately small.

HTTP handlers perform transport work and call use cases. Use cases own business transaction boundaries. Repositories and sqlc queries do not secretly begin or commit transactions.

## Data layer

- PostgreSQL is mandatory in V1.
- pgx supplies connections and pooling.
- sqlc generates typed query code.
- Goose manages readable SQL migrations.
- Direct pgx remains an explicit escape hatch for exceptional PostgreSQL operations.
- Tests use disposable PostgreSQL instances.

Optional multitenancy uses `org_id` plus PostgreSQL row-level security as defense in depth. RLS requires tests for pooled connections, background jobs, administrative paths, and cross-tenant failures.

## API boundary

- REST resources plus explicit domain-command endpoints.
- `/api/v1` version prefix.
- OpenAPI is authoritative for transport types.
- Strict oapi-codegen chi bindings.
- openapi-typescript frontend models and a lightweight fetch client.
- RFC 9457 Problem Details errors.
- Cursor pagination by default; offset pagination for appropriate reports.
- Idempotency for sensitive commands.

## Core services and optional modules

Every application includes sessions and authentication, authorization, an audit log, PostgreSQL-backed jobs, and mail. Optional capabilities are compile-time modules recorded in the project recipe and wired with explicit generated code: organizations (multitenancy with RLS), TOTP, in-app notifications with optional SSE, and file uploads/storage. There is no dynamic module loading.

## Runtime boundary

Nise is primarily build-time. Applications import only a small number of stable runtime packages for genuinely shared concerns. Business code must not depend pervasively on a Nise service locator, base class, or plugin interface.

## Frontend boundary

The authenticated SvelteKit application is statically built and embedded. Public marketing sites remain separate when SSR, content workflows, or SEO justify them.

