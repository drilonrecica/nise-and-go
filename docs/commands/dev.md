# `nise dev`

Runs the development loop for a generated project: a PostgreSQL container, the
Go server with rebuild-on-change, and the Vite dev server, all behind **one
origin** the browser talks to.

Run it from anywhere inside a project (it walks up to find `nise.json`):

```sh
nise dev
```

Press Ctrl-C to stop. Everything `nise dev` started is stopped with it.

The decision behind the topology, the alternative that was rejected, and the
constraint it places on the later cookie-prefix decision are in
[ADR 0012](../adr/0012-dev-loop-proxy-topology.md).

## Topology

```text
                 you open this
                       │
                       ▼
  browser ──▶ http://127.0.0.1:8080        nise dev's reverse proxy
                       │
      ┌────────────────┴────────────────┐
      │                                 │
  /api  /healthz  /internal        everything else
      │                                 │
      ▼                                 ▼
  Go server 127.0.0.1:8081        Vite 127.0.0.1:5173
      │                            (document, modules,
      │                             HMR websocket)
      ▼
  PostgreSQL 127.0.0.1:5432
  (container, health-gated)
```

**One origin.** The browser only ever sees `http://127.0.0.1:8080`. That is also
the origin the Go server believes it is serving, because the proxy forwards the
inbound `Host` header unchanged — so cookies, `Origin`, `Referer`, and
`Sec-Fetch-*` behave in development exactly as they will in production, with no
development-only exemption anywhere.

**The proxy is `nise dev` itself**, not the application. The generated binary
contains no proxy code. A consequence you will notice: rebuilding the Go server
does not disturb the browser's connection or Vite's hot-reload socket, because
neither goes through the Go child.

### What goes where

| Request path | Served by |
|---|---|
| `/api`, `/api/…` | the Go server |
| `/healthz`, `/healthz/…` | the Go server |
| `/internal`, `/internal/…` | the Go server |
| everything else | Vite |

Matching is on path-segment boundaries: `/api` matches `/api` and `/api/v1/x`,
never `/apiary`. These prefixes are exactly the subtrees the generated
`internal/platform/httpapi/router.go` mounts ahead of its catch-all.

## Ports

| Role | Default | Flag |
|---|---|---|
| Proxy — the one you open | `8080` | `--port` |
| Go server, behind the proxy | `8081` | `--app-port` |
| Vite, behind the proxy | `5173` | `--vite-port` |
| PostgreSQL, on loopback | `5432` | `--db-port` |

The defaults are fixed, not auto-selected: a URL you can bookmark is worth more
than one that never collides. `8080` matches the application's own default
`PORT` and the port `deploy/compose.yaml` publishes, so the development URL and
the production URL differ only in host.

Everything binds `127.0.0.1`. Nothing `nise dev` starts is reachable from
another machine.

If a port is taken, `nise dev` says so before it starts anything, names the flag
that moves it, and gives you the command that finds the holder on your platform
(`ss` on Linux, `lsof` on macOS, `netstat` on Windows). The database port is the
one exception: it is not probed, because the normal case is that this project's
own container is already listening on it.

## Flags

| Flag | Default | Effect |
|---|---|---|
| `--port` | `8080` | The port the browser connects to. |
| `--app-port` | `8081` | Where the Go server listens, behind the proxy. Ignored with `--embedded`. |
| `--vite-port` | `5173` | Where Vite listens, behind the proxy. |
| `--db-port` | `5432` | The loopback port the PostgreSQL container publishes. |
| `--db-image` | `docker.io/library/postgres:17.6-alpine` | The PostgreSQL image. Always an exact tag. |
| `--no-db` | off | Do not start PostgreSQL at all. |
| `--stop-db` | off | Stop the container on exit instead of leaving it running. |
| `--container-runtime` | `auto` | `auto`, `docker`, or `podman`. |
| `--embedded` | off | Serve the embedded frontend build from the Go server; no Vite, no proxy, no hot reload. See [Two modes](#two-modes). |
| `--poll` | `300` | Source-watch poll interval, in milliseconds. |
| `--stop-grace` | `6000` | Milliseconds a child gets to shut down gracefully before it is killed. |

The five global flags (`--json`, `--quiet`, `--verbose`, `--no-color`,
`--help`) work as [the output contract](../cli-output.md) describes.

## Two modes

### Default: Vite serves the frontend

Hot module reload works. A `.svelte` edit is visible without a rebuild. The
document comes from Vite, so **it carries no Content Security Policy** — see
[Development and the CSP](#development-and-the-content-security-policy).

### `--embedded`: the Go server serves the frontend

`nise dev --embedded` runs the project's own `pnpm --dir frontend run build`,
which writes into the `go:embed` target, then runs the Go server alone on the
port you open. No Vite, no proxy.

This is the production **topology**, exactly: one binary serving the built assets
and the API from one origin. The headers are the same development set described
under [Development and the CSP](#development-and-the-content-security-policy) —
**production minus HSTS**, because `APP_ENV` is still `development` and
`runtime/secure` never pins `Strict-Transport-Security` on a development
deployment. What `--embedded` changes is that the **Content Security Policy is
now enforced against the real built artifact**, with the inline-script hash the
server computes from the embed at startup, instead of being bypassed by a
document Vite served.

That is the whole point of the mode: the CSP is the header a frontend change can
break, and it is byte-for-byte the production one. HSTS is the one header
production adds on top, and it is deliberately absent here — a stray HSTS header
pins `http://localhost` in your browser for a year and nothing the application
can do afterwards unpins it.

There is no hot reload in this mode, and a frontend change means re-running the
command. Use it to confirm a change survives the real policy before you push.

## What `nise dev` sets, and what it does not

The application child inherits your environment with exactly this overlay:

| Variable | Value | Why |
|---|---|---|
| `PORT` | the app port | So the proxy knows where to forward. |
| `BIND_ADDR` | `127.0.0.1` | Nothing is exposed off the machine. |
| `APP_ENV` | `development` | **Only when you have not set it.** Your own value wins. |
| `DATABASE_URL` | the real connection string | Unless `--no-db`. Never printed; see [The database](#the-database). |
| `NO_COLOR`, `FORCE_COLOR` | `1`, `0` | **Only when color is off**, to ask children not to emit ANSI. |

**No production default is weakened.** In particular `TRUST_PROXY_HEADERS` and
`DEBUG` are left exactly where you left them. `nise dev` chooses where the
application listens and which database it talks to, and nothing else about how
it behaves. This is asserted by a test, not just by this table.

`nise dev` **refuses to run** unless `APP_ENV` is unset, `development`, or
`test`. It refuses every other value, including `Production`, `prod`, and
`staging` — a typo must not read as permission. It starts an unauthenticated
proxy, publishes a database with a derived password, and restarts a binary on
every file change; none of that may happen against anything anyone would call
production.

## Development and the Content Security Policy

The generated application's development policy is **production minus HSTS**:
`default-src 'none'`, `script-src 'self'` plus build-time hashes, `connect-src
'self'` ([Security headers](../security-headers.md),
[ADR 0013](../adr/0013-security-headers-and-csp.md)). `nise dev` does not
loosen it, and there is no `nise dev` flag that can.

Vite's development HTML cannot be served under that policy. It carries an inline
bootstrap script generated per request, so no hash computed at startup can cover
it, and allowing it would need `'unsafe-inline'` in `script-src` — which
`runtime/secure` refuses to produce through any option, on purpose.

So the two are kept apart rather than reconciled:

- **In the default mode, the document comes from Vite with Vite's own headers
  and no CSP.** Those requests never reach the application, so nothing about the
  application's policy is relaxed to allow them. This is also what lets the HMR
  websocket work at all: under `connect-src 'self'` on a page with no CSP, there
  is nothing to block.
- **Everything the Go server serves in development carries the full development
  policy**, unmodified. `curl -D- http://127.0.0.1:8080/healthz/ready` shows it.
- **`--embedded` enforces the real policy against the real artifact.** That is
  the mode to run before you push a frontend change.

The cost, stated plainly: a CSP violation introduced in frontend code is not
caught by the everyday loop. It is caught by `--embedded`, by the end-to-end
suite, or in production. Run `--embedded` when you have touched the frontend.

## Hot module reload

HMR works in the default mode, over the proxy's own origin. Vite's client dials
`location.host`, which is the proxy, and the proxy passes the websocket upgrade
through.

**Do not set `server.hmr.port` or `server.hmr.clientPort` in
`frontend/vite.config.ts`.** Either one makes Vite's client dial Vite directly
instead of the proxy, which puts the websocket on a second origin and undoes the
single-origin property this whole topology exists for.

`nise dev` starts Vite with `--host 127.0.0.1 --strictPort`, appended to the
project's own `dev` script. Both matter: Vite's default host binds `[::1]` only
on a dual-stack machine (and the proxy dials `127.0.0.1`), and without
`--strictPort` Vite silently moves to another port when the chosen one is taken.

## Rebuild on change

`nise dev` watches `cmd/`, `internal/`, and `db/`, plus `go.mod` and `go.sum`,
for changes to `.go`, `.mod`, `.sum`, and `.sql` files. It never descends into
`.git/`, `node_modules/`, `.svelte-kit/`, `bin/`, `testdata/`, or the frontend
build output. `frontend/` is Vite's job.

When a change settles — the tree has been quiet for 250 ms — it runs `go build`
into a scratch directory outside your project, and only if that succeeds does it
replace the running server. **A compiler error leaves the running server alone**,
so you can keep clicking around the application while you read the error.

It is a **stat poller**, not `fsnotify`. The framework has zero runtime
dependencies, and the measurement says the trade is fine: one sweep of a
320-file tree takes **0.93 ms** (`go test -bench BenchmarkWatcherScan
./internal/dev/`), which at the 300 ms default is about **0.31% of one core**.
The latency it adds is at most one interval plus the debounce, which is
invisible next to the `go build` that follows. On a very large tree, raise
`--poll` rather than reaching for a dependency.

### Why a restart takes about five seconds

The generated application drains for five seconds on shutdown
(`runtime/lifecycle`'s default), and `nise dev` waits for it rather than killing
it, so every rebuild runs the application's real production shutdown path. That
is deliberate: a loop that SIGKILLs the server on every save is a loop where
nobody ever exercises the shutdown code.

If you would rather have the seconds back, `--stop-grace 500` kills the child
after half a second instead. Only the Go server is affected; the browser, Vite,
and HMR are untouched throughout.

## The database

`nise dev` runs PostgreSQL in a container and does not start the application
until it is actually accepting connections.

- **Runtime detection.** It probes `docker`, then `podman`, and decides which
  engine each binary *is* from what `--version` reports — never from the name,
  because `/usr/bin/docker` is a Podman shim on a great many machines. It then
  runs `<engine> info` to confirm the engine actually works, so a Docker client
  with no daemon behind it fails here rather than three steps later. Force one
  with `--container-runtime`.
- **Deterministic names.** Container `nise-dev-<project>-postgres`, volume
  `nise-dev-<project>-pgdata`, role and database both `<project>`. The same
  project always finds its own container; two projects never collide.
- **A pinned image tag**, never `latest`, and fully qualified with its registry
  (Podman has no implicit `docker.io`). A floating tag would put two developers
  on the same commit on different major versions of PostgreSQL.
- **A named volume**, so your data survives a container restart — and survives
  deleting the container, which is what makes `<engine> rm -f` a safe fix.
- **Health-gated.** `pg_isready` runs *inside* the container, over TCP, and must
  succeed twice in a row. Both details matter: on a cold volume the image runs
  `initdb` against a temporary server that listens on the Unix socket only, so a
  socket-scoped probe reports ready and the next connection gets "the database
  system is shutting down".
- **Published on `127.0.0.1` only.** The password is derived from the project
  name so the same project reconnects to its own volume every time, which is
  only acceptable because nothing off this machine can reach the port.
- **The password is never printed.** The startup block and the `--json` document
  both carry the redacted form:
  `postgres://demoapp:[REDACTED]@127.0.0.1:5432/demoapp?sslmode=disable`. The
  real string exists only in the application child's environment.

### The container keeps running after you exit

By default. A development database accumulates state you paid for — seed data, a
half-applied migration, a reproduction of a bug — and stopping it on every Ctrl-C
makes the next start slower for nothing. `nise dev` names the container and the
exact command on the way out:

```text
[db] container nise-dev-demoapp-postgres is still running — stop it with: podman stop nise-dev-demoapp-postgres
```

Pass `--stop-db` to have it stopped for you. Neither ever removes the volume.

## Output

Every line is tagged with its source, so three processes writing to one terminal
stay readable:

| Tag | Source |
|---|---|
| `[dev]` | `nise dev` itself: builds, restarts, proxy failures |
| `[app]` | the Go server |
| `[web]` | Vite |
| `[db]` | the database container |

Output is line-buffered per source, so a multi-line compiler error arrives as
one block rather than interleaved with Vite's.

`nise dev` emits no ANSI of its own. When color is off (a pipe, `NO_COLOR`,
`TERM=dumb`), it also asks the children not to emit any and strips what they
emit anyway.

`--quiet` suppresses everything except errors. `--json` emits exactly one
document — the topology, so a script can learn the ports — and no child output
at all:

```json
{"appPrefixes":["/internal","/healthz","/api"],"appUrl":"http://127.0.0.1:8081","containerRuntime":"podman 5.8.4","databaseContainer":"nise-dev-demoapp-postgres","databaseUrl":"postgres://demoapp:[REDACTED]@127.0.0.1:5432/demoapp?sslmode=disable","embedded":false,"project":"demoapp","proxyUrl":"http://127.0.0.1:8080","webUrl":"http://127.0.0.1:5173"}
```

## Shutdown

Ctrl-C (or `SIGTERM`) stops the proxy first — so the browser gets a refused
connection rather than a 502 from a dying child — then Vite, then the Go server,
then the database if `--stop-db` was given.

Each child runs in its own process group, which is what lets `nise dev` signal a
child's own grandchildren (the `vite` that `pnpm` forked) instead of orphaning
them, and what keeps a terminal Ctrl-C from racing four processes at once. On
Windows there is no `SIGTERM`: children are terminated directly, so
`--stop-grace` has no effect there.

## Exit codes

Standard, per [the output contract](../cli-output.md): `0` success, `1` a
failure, `2` a bad command line, `3` a missing precondition. Every precondition
failure below carries a machine-readable code under `--json`.

## Troubleshooting

| What you see | What it means | What to do |
|---|---|---|
| `no nise.json found in the current directory or any parent` | You are not inside a generated project. | `cd` into one, or `nise new`. |
| `APP_ENV is "…"; nise dev only runs in a development environment` | `APP_ENV` is set to something that is not `development` or `test`. | Unset it, or set `APP_ENV=development`. |
| `the proxy port 127.0.0.1:8080 is already in use` | Something holds the port. | Stop it, or `--port 8090`. The error names the command that finds the holder. |
| `no usable container runtime was found` | Neither Docker nor Podman is installed *and working*. Run with `--verbose` to see what each one said. | Start the daemon, fix socket permissions, or `--no-db`. |
| `… installed but not usable: … permission denied … docker.sock` (under `--verbose`) | Docker is installed but your user cannot reach its socket. | Add yourself to the `docker` group, or use `--container-runtime podman`. |
| `the existing database container publishes 5432, not 5433` | A container from an earlier run has a different port mapping. | `<engine> rm -f nise-dev-<project>-postgres` (the volume is kept), or match the old port with `--db-port`. |
| `the PostgreSQL container did not become ready` | The cluster failed to start. | `<engine> logs nise-dev-<project>-postgres`. A volume from an incompatible major version is the usual cause; remove the volume to start clean, losing its data. |
| `frontend/node_modules is missing` | The frontend has never been installed. `nise dev` will not run pnpm for you. | `pnpm --dir frontend install`. |
| A `502` page naming the `"app"` upstream | The Go server is rebuilding, or failed to start. | Reload in a second; if it persists, read the `[app]` and `[dev]` lines. |
| A `502` page naming the `"web"` upstream | Vite is not listening. | Read the `[web]` lines. |
| Pages load but HMR never fires | Usually `server.hmr.port`/`clientPort` set in `vite.config.ts`, which takes the socket off the proxy's origin. | Remove that setting. |
| A `.svelte` edit changes nothing | You are in `--embedded` mode, which has no hot reload. | Drop `--embedded`. |
| A CSP violation in the browser console that nobody saw before | You are in `--embedded` mode, where the real policy is enforced. | This is the mode working. Fix the violation; see [Security headers](../security-headers.md). |
| A `.go` edit does not trigger a rebuild | The file is outside `cmd/`, `internal/`, and `db/`, or under a skipped directory. | Move it, or raise the issue — the watch list is in `internal/dev/watch.go`. |

## Running the container integration tests

The unit tests need no container. The container-backed tests are behind a build
tag:

```sh
go test -tags dev_integration ./internal/dev/ -run Integration -count=1 -v
```

Pick an engine with `NISE_DEV_TEST_RUNTIME=podman` (or `docker`). They skip
cleanly when no runtime is available, use their own project name so they cannot
collide with yours, and remove the container and volume they create.
