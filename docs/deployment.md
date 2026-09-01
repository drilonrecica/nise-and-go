# Deployment

This page describes the planned production path.

## Artifacts

Applications produce:

- A standalone Go binary.
- A minimal non-root OCI image.

OCI deployment is the documented default. Coolify is the first blessed recipe, with portable Docker Compose examples. Kubernetes is outside V1 scope.

## The image

`deploy/Dockerfile`, built from the repository root:

```
docker build -f deploy/Dockerfile -t myapp .
```

Three stages. Node builds the frontend, the Go build embeds its output, and
the runtime stage carries the static binary and nothing else — no shell, no
package manager, no libc.

Measured on a generated project: **17 MB**, running as `nonroot`, with the
frontend embedded and `/bin/sh` genuinely absent.

### Run `pnpm --dir frontend install` first

Generation writes no `frontend/pnpm-lock.yaml`, because resolving one needs
the network and would make two `nise new` runs produce different projects. So
the first image build of a new project **fails**, deliberately, with a message
naming the fix.

It fails rather than resolving dependencies on the fly, and the message says
not to pass `--no-frozen-lockfile`: an image built from unpinned dependencies
is one whose contents nobody can state, and being able to state them is most
of why it was built in a container.

### Base images are pinned by digest

A tag moves. `golang:1.26` is a different image next month, so the same commit
would produce a different binary — and an image nobody can rebuild is one
nobody can audit after an advisory. The tag is kept beside the digest so a
reader can see what it is meant to be; the digest resolves.

Update one deliberately, with `docker buildx imagetools inspect <image>:<tag>`.

### Set the revision label at build time

```
docker build -f deploy/Dockerfile   --label org.opencontainers.image.revision="$(git rev-parse HEAD)"   --label org.opencontainers.image.created="$(date -u +%Y-%m-%dT%H:%M:%SZ)"   -t myapp .
```

The static annotations — title, description, base image and its digest — are
in the Dockerfile. The two that change every commit are not, because baking
them in would invalidate the layer on every build and a hard-coded date is
false the moment it is written.

### There is no HEALTHCHECK

The image has no shell and no HTTP client, so a `HEALTHCHECK` instruction
would need a second binary purely to call an endpoint your platform can call
directly. Point liveness at `/healthz` and readiness at `/readyz` — see
[operations](operations-runtime.md).

### `ALLOW_PUBLIC_BIND=true` is set, on purpose

A container that binds only to loopback is a container nothing can reach. The
application refuses a public bind in production without this exact opt-in;
setting it here is that check working rather than being bypassed, because
inside a container the published ports are the boundary.

## Recipes

`deploy/compose.yaml` runs the whole thing locally: the image, PostgreSQL, and
a migration step. `deploy/coolify.md` is the hosted recipe.

Both run the application with `APP_ENV=production`, deliberately. A recipe
that runs in development mode proves the image starts and proves nothing about
whether a deployment will — and this application's production checks are the
ones most likely to stop it.

Verified by running it: migrations applied 9 of 9, the application started
with no schema warning, `/healthz`, `/readyz`, and `/startupz` all answered
200, and the embedded frontend was served.

### Migrations are a separate step, not a startup task

Both recipes run `db migrate` to completion **before** the application starts,
and neither has the application migrate itself.

An application that migrates when it boots migrates once per replica,
concurrently, during a rolling deploy — and the second replica's migration
runs against a schema the first is halfway through changing. Run as its own
step, a failed migration stops the deploy with the migration's own error,
rather than starting an application that refuses to serve for a reason
somebody then has to go and find.

The application still refuses to start against a schema it does not
understand. That is the backstop, not the mechanism.

### The settings production will not start without

Both recipes set these, and the reason each is required is that its absence is
invisible until it matters:

- `CURSOR_SIGNING_KEY` — without it, each replica signs pagination cursors
  with a key it generated, and rejects the cursors the others issued.
- `MAIL_TRANSPORT=smtp` — the log transport sends nothing, silently.
- `ALLOW_PUBLIC_BIND=true` — the explicit opt-in for binding a public
  address, which inside a container is what published ports already are.
- `LOG_FORMAT=json`, `DEBUG=false`.

The compose file uses Compose's `:?` form for each secret rather than a
default, because a default for a signing key is a signing key shared by
everybody who copied the file.

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

A generated application takes, verifies, and restores its own encrypted backups:

```sh
./myapp db backup  -out /backups/myapp-2026-09-01.backup
./myapp db verify  -from /backups/myapp-2026-09-01.backup
./myapp db restore -from /backups/myapp-2026-09-01.backup -confirm myapp
```

`db verify` restores into a scratch database rather than checking a checksum,
because a backup nobody has restored is a hypothesis. Run it on a schedule.

These need `pg_dump` and `pg_restore`, which the runtime image deliberately
does not carry — run them from an operator machine, a scheduled job, or a
sidecar. See [Backups](backups.md) for the format, the encryption, and what
`-confirm` is for.

Retention, off-server copies, and PostgreSQL point-in-time recovery remain the
operator's, and are not automated here. VPS snapshots are defence in depth,
not the database backup plan.

## Releases and rollback

Deploy immutable tags and image digests. Retain the previous artifact and document rollback. Use expand/contract migrations when zero-downtime rollback matters.

