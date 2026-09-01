# Documentation

Nise & Go is currently in design and pre-alpha development. These pages describe the intended V0.1 contract. They are not evidence that a feature has already been implemented or security-reviewed.

## Start here

- [Project status](project-status.md)
- [Philosophy](philosophy.md)
- [Architecture](architecture.md)
- [Repository layout](repository-layout.md)
- [Generated application layout](generated-application-layout.md)
- [Runtime packages](runtime-packages.md)
- [Configuration](configuration.md)
- [Database queries and sqlc](database-queries.md)
- [Direct pgx escape hatch](direct-pgx.md)
- [Database query instrumentation](database-query-instrumentation.md)
- [API routing and middleware](api-routing.md)
- [OpenAPI server bindings and frontend client](openapi.md)
- [Pagination](pagination.md)
- [Idempotency for sensitive commands](idempotency.md)
- [Database migrations](database-migrations.md)
- [Database transactions](database-transactions.md)
- [PostgreSQL integration testing](database-testing.md)
- [Background jobs](jobs.md)
- [Outbound email](mail.md)
- [Object storage](storage.md)
- [The upload lifecycle](uploads.md)
- [In-app notifications](notifications.md)
- [Multitenancy and row-level security](multitenancy.md)
- [Observability](observability.md)
- [Metrics and tracing](metrics.md)
- [Operations: runtime HTTP lifecycle](operations-runtime.md)
- [Generated frontend](frontend.md)
- [Security model](security.md)
- [Password hashing](passwords.md)
- [Sessions](sessions.md)
- [Audit log](audit.md)
- [Authorization](authorization.md)
- [Enrollment](enrollment.md)
- [Authentication throttling](throttling.md)
- [Reauthentication](reauthentication.md)
- [Second factor: TOTP and recovery codes](second-factor.md)
- [Security headers and Content Security Policy](security-headers.md)
- [CLI and distribution](cli-and-distribution.md)
- [No telemetry](no-telemetry.md)
- [CLI output contract](cli-output.md)
- [Performance baselines](performance-baselines.md)
- [`nise new`](commands/new.md)
- [`nise dev`](commands/dev.md)
- [`nise db`](commands/db.md)
- [`nise doctor`](commands/doctor.md)
- [`nise agents`](commands/agents.md)
- [`nise test`](commands/test.md)
- [`nise generate`](commands/generate.md)
- [`nise check`](commands/check.md)
- [Toolchain](toolchain.md)
- [Dependency allowlist](dependencies.md)
- [Supported platforms](supported-platforms.md)
- [Checks](checks.md)
- [Linting](linting.md)
- [Deployment](deployment.md)
- [Versioning and compatibility](versioning.md)
- [Public roadmap](roadmap.md)
- [Architecture Decision Records](adr/README.md) ([template](adr/template.md))

## Documentation rules

- Current behavior and planned behavior must be labeled clearly.
- Examples should use the golden profile unless a page explicitly discusses a later profile.
- Security and performance claims require evidence and a reproducible method.
- Commands and configuration belong in reference pages once the interface stabilizes.
- Consequential design changes require an ADR.
