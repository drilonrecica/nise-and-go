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
- [Database migrations](database-migrations.md)
- [Observability](observability.md)
- [Metrics and tracing](metrics.md)
- [Operations: runtime HTTP lifecycle](operations-runtime.md)
- [Generated frontend](frontend.md)
- [Security model](security.md)
- [Security headers and Content Security Policy](security-headers.md)
- [CLI and distribution](cli-and-distribution.md)
- [No telemetry](no-telemetry.md)
- [CLI output contract](cli-output.md)
- [Performance baselines](performance-baselines.md)
- [`nise new`](commands/new.md)
- [`nise dev`](commands/dev.md)
- [`nise doctor`](commands/doctor.md)
- [`nise agents`](commands/agents.md)
- [`nise test`](commands/test.md)
- [`nise generate`](commands/generate.md)
- [`nise check`](commands/check.md)
- [Toolchain](toolchain.md)
- [Dependency allowlist](dependencies.md)
- [Supported platforms](supported-platforms.md)
- [Checks](checks.md)
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
