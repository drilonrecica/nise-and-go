# Nise & Go

> An opinionated, security-first, resource-efficient application foundation for Go, chi, PostgreSQL, and SvelteKit.

Nise & Go aims to provide a Rails-like development experience without hiding normal Go behind reflection, runtime magic, or a large framework abstraction. It combines deterministic generators, a small runtime, a complete Svelte application foundation, and one documented production path.

## Status

**Design / pre-alpha. Do not use this for production yet.**

The architecture and V0.1 boundary are documented, but the CLI and runtime are not yet released. Commands shown below describe the intended interface and may change during `0.x`.

## Golden profile

V1 deliberately supports one stack:

- Go with chi.
- PostgreSQL with pgx and sqlc.
- SvelteKit with Svelte 5 runes.
- Tailwind CSS v4 and curated, application-owned UI components.
- OpenAPI as the HTTP boundary.
- One Go binary containing the API, jobs, migrations, and static frontend assets.

Nise is not trying to support every router, database, frontend, or deployment platform.

## Intended experience

```console
nise new acme
cd acme
nise dev
nise generate resource customer
nise check
nise test
nise db migrate
```

Generation is deterministic. Generated business features and frontend components belong to the application and can be edited normally. Nise should remain primarily a build-time tool with a small, explicit runtime surface.

## What it is intended to include

- A cross-platform `nise` CLI.
- Feature-oriented modular-monolith structure.
- Typed configuration and fail-closed production validation.
- Strict REST/OpenAPI generation.
- Opaque server-side sessions and granular authorization.
- PostgreSQL migrations, typed SQL, and real integration tests.
- PostgreSQL-backed background jobs.
- Email, file-storage, audit, and notification foundations.
- A complete authenticated Svelte application shell.
- Security, quality, performance, release, backup, and deployment conventions.

## Principles

- One excellent golden path beats a shallow configuration matrix.
- Generated explicit code beats runtime magic.
- Secure defaults must fail closed.
- Performance claims require measurements.
- Application code remains application-owned.
- Real PostgreSQL behavior is tested against PostgreSQL.
- No built-in AI, BYOK, telemetry, or automatic update checks.
- No mandatory Redis, broker, tracing collector, or Kubernetes.

## Documentation

- [Documentation index](docs/README.md)
- [Project status](docs/project-status.md)
- [Philosophy](docs/philosophy.md)
- [Architecture](docs/architecture.md)
- [Generated frontend](docs/frontend.md)
- [Security model](docs/security.md)
- [CLI and distribution](docs/cli-and-distribution.md)
- [Deployment](docs/deployment.md)
- [Versioning and compatibility](docs/versioning.md)
- [Architecture decisions](docs/adr/README.md)

## Roadmap

V0.1 will implement the frozen golden-profile foundation and prove it through a realistic reference business application. Feature development then freezes while POS Kosova validates the framework against a real production application. Nise will not be called `1.0` until two real applications and at least one genuine upgrade cycle have tested its abstractions.

## Contributing

Nise & Go is a maintainer-driven personal project. Issues are welcome, but responses and fixes are not guaranteed. Unsolicited pull requests may be closed without review, especially large rewrites or unexplained generated code. Read [CONTRIBUTING.md](CONTRIBUTING.md) before spending time on a patch.

## Security

Do not report vulnerabilities in public issues. See [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)

