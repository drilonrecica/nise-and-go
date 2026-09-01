# Documentation

Nise & Go is currently in design and pre-alpha development. These pages
describe the intended V0.1 contract. They are not evidence that a feature has
already been implemented or security-reviewed.

## New here?

**[Your first Nise application](tutorial.md)** — thirty minutes, from nothing
installed to a running application with a feature you added. Every command on
that page has been run in the order it is written.

Then, in whichever order the question comes up:

| Question | Page |
|---|---|
| What is this, and is it ready? | [Project status](project-status.md) |
| Why is it shaped like this? | [Philosophy](philosophy.md), [architecture](architecture.md) |
| What did `nise new` write, and what may I edit? | [Generated application layout](generated-application-layout.md) |
| How do I add a feature? | [`nise generate`](commands/generate.md) |
| How do I run it while I work? | [`nise dev`](commands/dev.md) |
| How do I put it somewhere? | [Deployment](deployment.md), [backups](backups.md) |
| What does it *not* protect me from? | [Threat model](threat-model.md), [security](security.md) |
| Show me a real one | [Workbench, the reference application](../examples/reference/README.md) |

## Everything else

The pages below are reference: complete, and not meant to be read in order.


- [Your first Nise application](tutorial.md)
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
- [Threat model](threat-model.md)
- [Repository security and maintainer recovery](repository-security.md)
- [Reference application](../examples/reference/README.md)
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
- [`nise version`](commands/version.md)
- [`nise upgrade`](commands/upgrade.md)
- [Toolchain](toolchain.md)
- [Dependency allowlist](dependencies.md)
- [Supported platforms](supported-platforms.md)
- [Checks](checks.md)
- [Linting](linting.md)
- [Deployment](deployment.md)
- [Backups](backups.md)
- [Versioning and compatibility](versioning.md)
- [Public roadmap](roadmap.md)
- [Architecture Decision Records](adr/README.md) ([template](adr/template.md))

## Documentation rules

- Current behavior and planned behavior must be labeled clearly.
- Examples should use the golden profile unless a page explicitly discusses a later profile.
- Security and performance claims require evidence and a reproducible method.
- Commands and configuration belong in reference pages once the interface stabilizes.
- Consequential design changes require an ADR.
