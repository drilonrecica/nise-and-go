# Deployment

This page describes the planned production path.

## Artifacts

Applications produce:

- A standalone Go binary.
- A minimal non-root OCI image.

OCI deployment is the documented default. Coolify is the first blessed recipe, with portable Docker Compose examples. Kubernetes is outside V1 scope.

## Reverse proxy

The application expects TLS to terminate at a reverse proxy such as the one Coolify provides. Client-address and protocol headers are honored only from explicitly configured trusted proxies. Browser sessions use `__Host-` cookies, so the browser-facing edge must serve HTTPS.

## Process modes

Small applications run `all` mode. Larger deployments may run the same image separately as `web` and `worker` processes.

## Configuration

- Typed environment variables.
- `_FILE` variants for mounted secrets.
- Fail-closed production validation.
- No committed production secrets.

## Database migrations

Production runs migrations as an explicit release step before starting the new application version. Application startup refuses an incompatible schema. Destructive changes document that binary rollback alone is unsafe.

## Health and shutdown

- Separate startup, liveness, and readiness checks.
- No internal details in public probe responses.
- Graceful HTTP and worker shutdown with bounded draining.

## Observability

- Structured JSON production logs and readable local logs.
- Request and correlation IDs.
- Essential HTTP, database-pool, and job metrics.
- Optional tracing without a mandatory collector.
- No request/response-body logging by default.

## Backup

The production baseline requires encrypted database backups, retention, off-server copies, and automated restore verification. VPS snapshots are defense in depth, not the database backup plan. Critical systems may add PostgreSQL point-in-time recovery.

## Releases and rollback

Deploy immutable tags and image digests. Retain the previous artifact and document rollback. Use expand/contract migrations when zero-downtime rollback matters.

