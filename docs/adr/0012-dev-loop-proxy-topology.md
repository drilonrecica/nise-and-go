# ADR 0012: Dev-Loop Proxy Topology

- **Status:** Accepted
- **Date:** 2026-08-28

## Context

A Nise application is one Go binary. In production it serves the API and the
`adapter-static` SvelteKit build — embedded with `go:embed` — from a single
origin ([ADR 0005](0005-static-embedded-frontend.md), [Generated application
layout](../generated-application-layout.md)). Development cannot work that way:
the point of a dev loop is that a `.svelte` edit is visible without a `pnpm
build` and a `go build`, which means Vite has to serve the frontend, which means
a second process listening on a second port.

Two processes and one browser is a topology question, and the working threat
model has carried it as an open question since the stack was chosen: **"Dev-loop
proxy topology and cookie origin (M1-011)."** A related question, **"Dev-mode
`__Host-`/`Secure` cookie behavior on plain-HTTP localhost (M5-015)"**, is
deliberately *not* answered here; this record states the constraint the topology
choice imposes on it.

The question is not stylistic. Sessions are opaque server-side cookies ([ADR
0006](0006-opaque-sessions-no-jwt.md)) and request-forgery defense will be
origin-based (M5-014, M5-013). Both are properties of *which origin the browser
thinks it is talking to*. A dev loop that gets that wrong does not fail loudly;
it produces an application that works on every developer's machine and breaks,
or is insecure, in production.

### The two candidates

**(a) Vite owns the origin.** The browser opens `http://localhost:5173`. Vite
serves the document, the modules, and the hot-module-reload socket, and
`server.proxy` in `vite.config.ts` forwards `/api` to the Go server on another
port.

**(b) Go owns the origin.** The browser opens `http://localhost:8080`. The Go
side serves `/api`, `/healthz`, and `/internal`, and forwards everything else to
Vite.

## Decision

**Topology (b): the Go side owns the origin.** The browser talks to one address
for its entire session, and that address belongs to the Go half of the
application — the same half that owns the origin in production.

The proxy that implements (b) lives in the `nise` CLI (`internal/dev`), not in
the generated application, and it listens on the port the developer opens:

```text
browser ──▶ http://127.0.0.1:8080          nise dev's proxy
                 │
                 ├─ /api  /healthz  /internal ──▶ Go application child  127.0.0.1:8081
                 └─ everything else           ──▶ Vite dev server       127.0.0.1:5173
```

`nise dev` supervises all three (plus a container-run PostgreSQL) and ends all of
them on one Ctrl-C. The operator-facing description is
[`nise dev`](../commands/dev.md).

### Why (b), criterion by criterion

**Single origin for cookies — the decisive criterion.** Both topologies give the
browser exactly one origin, so "single origin" alone does not separate them. What
separates them is *whose* origin it is, and what the Go server believes about it.

Under (a) the browser's origin is Vite's (`http://localhost:5173`) while the Go
server believes its own origin is `http://localhost:8080`. Cookies still round-trip
— Vite's proxy passes `Set-Cookie` through and the browser attributes it to
`localhost:5173` — but every request the Go server sees arrives carrying
`Origin: http://localhost:5173`, which is not the origin the application is
configured to serve. That is permanent and structural: it is a property of the
topology, not of anything a developer could set.

What it *looks like* at the server depends on one Vite setting, and the
distinction is worth stating precisely, because the conclusion is the same
either way and an overstated mechanism is the part of a record that gets quoted
and found wrong:

- With `server.proxy`'s `changeOrigin: true`, Vite rewrites `Host` to the
  upstream's, so the server sees `Origin: http://localhost:5173` against
  `Host: localhost:8080` — an explicit mismatch.
- With `changeOrigin: false` — **the default** — Vite forwards the browser's
  `Host`, so `Origin` and `Host` agree. They agree on the *wrong* value: both
  name Vite's origin, and neither names the origin the application is actually
  serving on. A check that compares `Origin` against `Host` therefore passes
  while verifying nothing, since both sides are supplied by the same request; a
  check against the application's own configured origin still fails.

So the defect is not "two headers disagree". It is that **under (a) the
application cannot verify a request against its own origin at all** — one
configuration makes that visible as a mismatch and the other hides it behind a
comparison that is vacuously true. Either way it lands squarely on the mechanism
M5 will use for request-forgery defense, and the only ways to live with it are
to disable the origin check in development, or to add `http://localhost:5173`
to an allowed-origin list that must never reach production. Both are
development-only weakenings of a security control — the exact category this
project forbids — and both make the check that matters most the one thing
development never exercises.

Under (b) the browser's origin **is** the Go server's origin — the one it is
listening behind and the one it would be configured with. `Origin`,
`Host`, and the application's own notion of where it is all name the same
thing, `Sec-Fetch-Site: same-origin` is genuine rather than an artifact, and the
CSRF check that will run in production is the same check, unmodified, in
development. There is nothing to exempt, because there is nothing that differs.

Two properties of the proxy make this true rather than approximately true, and
both are asserted by tests in `internal/dev/proxy_test.go`:

- **The inbound `Host` header is forwarded unchanged** to both upstreams —
  the equivalent of Vite's `changeOrigin: false`, chosen deliberately. Here it
  is the right choice precisely because the browser's origin already *is* the
  application's: forwarding `Host` preserves that, where rewriting it to the
  child's loopback address would hand the application a `Host` naming a port no
  browser ever typed.
- **`Set-Cookie` is never rewritten.** Not the `Path`, not `Secure`, not
  `SameSite`, not the `__Host-` prefix. Whatever the Go server sets, the browser
  receives byte for byte.

**HMR and websockets through the proxy.** Vite's hot-module-reload socket is a
websocket to `location.host`, which under (b) is the proxy. The proxy passes the
upgrade through (`httputil.ReverseProxy` handles protocol switching; a
hand-rolled handshake test covers it) and flushes immediately rather than
buffering. This was verified end to end against a generated project: a websocket
opened through `ws://127.0.0.1:8080/` receives `{"type":"connected"}` and then a
`js-update` message after a `.svelte` edit. Note that Vite must not be given an
explicit `hmr.port`; with none configured its client dials `location.port`, which
is the proxy, which is what keeps the socket on the one origin.

**Which process restarts on a Go change.** This is (b)'s classic weakness — if
the browser talks to Go, a Go rebuild drops the browser's connection — and it is
the reason the proxy is in the CLI rather than in the application. The proxy is
not the Go child. It outlives every restart of it, so a Go rebuild never touches
the browser's connection to Vite or its HMR socket. During the rebuild, requests
to `/api` return a purpose-written 502 that says the server is rebuilding;
everything else keeps working. Under (a) a Go rebuild has the same blast radius
on `/api` and a worse error surface.

**Error visibility when one side is down.** The proxy owns the failure page, so
"Vite is not running" and "the Go server is rebuilding" are two different pages,
each naming the upstream, the request, the usual cause, and the fix — and the
underlying transport error goes to the terminal, tagged, rather than to the
browser. Under (a) the same failure is `http-proxy`'s generic error, and the
`vite.config.ts` that produced it is an `[app]`-owned file the developer can
break.

**How closely dev mirrors production.** Under (a) the Go server never sees a
browser-originated document request at all in development, so nothing that wraps
one is exercised: not the middleware chain, not the SPA fallback, not the request
log, not the HTTP metrics. Under (b) every request the browser makes to an
application route traverses the real router, the real middleware order, and the
real handlers, in the real order the production binary uses. The only thing that
differs is the terminal handler behind `/*`, and that difference is confined to
one process that is never deployed.

### Where the proxy lives, and why not in the application

Topology (b) does not require the *application* to contain a proxy, and it must
not. A handler that forwards to an arbitrary loopback port, compiled into the
production binary and gated on an environment variable, is a fail-open hazard
with a server-side-request-forgery shape and no upside: one misread config value
away from a production binary that proxies. Keeping the proxy in the `nise` CLI
means the generated binary contains no dev-proxy code at all, and it is also what
gives the dev origin a lifetime longer than the Go child's.

A secondary consequence, recorded because a reader will notice it: this is why
`nise dev` works against a project generated by `nise new` without adding a
single file to `templates/`. No generated file changed for this ADR.

### The Content Security Policy interaction

This is the sharp edge, and it deserves a plain statement.

`runtime/secure`'s development policy is production-minus-HSTS: `default-src
'none'`, `script-src 'self'` plus build-time hashes, `connect-src 'self'` ([ADR
0013](0013-security-headers-and-csp.md), [Security headers](../security-headers.md)).
That is deliberate, and this ADR does not weaken it.

**Vite's development HTML cannot be served under that policy, and no option
exists to make it possible.** The document Vite serves carries an inline
`__sveltekit_dev` bootstrap script whose content is generated per request, so a
hash computed from the embedded artifact at startup — the mechanism the generated
router uses — cannot match it. Allowing it would require `'unsafe-inline'` in
`script-src`, which `runtime/secure`'s shared source validator refuses to produce
through any option, by design. There is no waiver for it because the weakening
does not exist in the API.

The resolution is therefore not to relax the policy but to **route around it**:

1. **Vite's responses do not pass through `secure.Middleware`.** They come from
   Vite, through the proxy, with Vite's own headers. Development documents
   carry no CSP. This is not an exemption granted to the application — the
   application never sees those requests — and it requires no dev-only branch in
   any generated file.
2. **Everything the Go server does serve in development carries the full
   development policy, unmodified.** Verified: `GET /healthz/ready` through the
   proxy returns the complete production header set minus
   `Strict-Transport-Security`.
3. **`nise dev --embedded` exercises the production path on demand.** It builds
   the frontend into the `go:embed` target and runs the Go server alone on the
   dev port, with no Vite and no proxy. The browser then gets the real artifact
   under the enforced Content Security Policy, with the real startup-computed
   script hash. The header set is still the development one — production minus
   HSTS, since `APP_ENV` remains `development` — and that is correct: the CSP is
   what a frontend change can break and it is byte-identical to production's,
   while `Strict-Transport-Security` on `http://localhost` would pin a
   developer's browser for a year. Verified: the `sha256-` source in the
   response's `script-src` is the SHA-256 of the inline script in the served
   document, and no `Strict-Transport-Security` header is present.

So development gets hot reload *or* an enforced CSP, one flag apart, and the
strict policy is never quietly loosened to buy both at once. `nise dev`
introduces **no** dev-only relaxation of any `runtime/secure` default; the
complete list of environment variables it sets for the application child is in
[`nise dev`](../commands/dev.md), and a test asserts that
`TRUST_PROXY_HEADERS` and `DEBUG` are not among them.

The residual risk is honest and worth naming: because the everyday loop serves
documents from Vite, a CSP violation introduced in frontend code is not observed
until somebody runs `--embedded`, runs the end-to-end suite, or deploys. The
mitigation is that `--embedded` exists, is documented, and is one flag; the
stronger mitigation is the browser assertion ADR 0013 already schedules for the
milestone-6 shell.

### The constraint this imposes on M5-015

M5-015 decides how `__Host-` and `Secure` cookies behave on plain-HTTP
localhost. This decision does not settle it, but it fixes the ground it will be
decided on:

1. **The dev origin is the Go server's own origin, over plain HTTP on
   loopback.** Whatever M5-015 decides is observed in development exactly as it
   will be in production, because the same server sets the cookie and the same
   server reads it back, with nothing in between that could rewrite it.
2. **The proxy is transparent to cookies, and must stay that way.** It does not
   rewrite `Set-Cookie`, does not rewrite the request path (so a `Path=/` or
   `__Host-` constraint means in development what it means in production), and
   forwards `Cookie` untouched. M5-015 may not be implemented by adding a
   rewrite here; that would reintroduce exactly the divergence topology (b) was
   chosen to remove.
3. **The permitted resolutions are narrowed to two.** Either keep `Secure` and
   rely on browsers treating `http://localhost` as a secure context (which is why
   `__Host-` can work at all on loopback), or serve development over HTTPS.
   **A third option — stripping `Secure`, or dropping the `__Host-` prefix, when
   the environment is development — is ruled out by this ADR**, because it is a
   development-only weakening of a cookie security attribute and it would mean
   the attribute that matters is the one development never exercises. If M5-015
   concludes that neither permitted resolution works, it supersedes this
   paragraph in a new ADR rather than adding the exemption quietly.
4. **`X-Forwarded-Proto` is `http` in development, and is written by the proxy,
   not the client.** The proxy deletes any inbound `X-Forwarded-*` before setting
   its own, so a cookie policy that keys off forwarded-protocol cannot be steered
   by a request from the browser.

### What was explicitly excluded

- **Vite's `server.proxy`.** Rejected as topology (a) above. It also puts the
  routing rule in `frontend/vite.config.ts`, an `[app]`-owned file, where a
  developer's edit can silently change which process serves a path.
- **A dev proxy inside the generated binary.** Rejected: dev-only forwarding
  code in a production artifact.
- **Relaxing the development CSP so Vite's HTML can be served under it.**
  Impossible without `'unsafe-inline'` in `script-src`, which `runtime/secure`
  refuses to produce, and undesirable regardless.
- **A dev-only allowed-origin list or a disabled origin check.** Ruled out by
  the choice of (b), which removes the need for one.
- **Deciding M5-015.** Constrained above; not decided.
- **`fsnotify` for rebuild-on-change.** A stat-polling watcher costs 0.93 ms per
  sweep on a 320-file tree (`BenchmarkWatcherScan`), which is about 0.31% of one
  core at the 300 ms default interval, and the latency it adds is invisible next
  to the `go build` it triggers. That is not worth the framework's first runtime
  dependency ([ADR 0008](0008-toolchain-and-dependencies.md),
  [Dependencies](../dependencies.md)).

## Consequences

**Positive.**

- Development and production agree on the origin, so `Origin`, `Host`,
  `Referer`, and `Sec-Fetch-*` mean the same thing in both. The request-forgery
  defense M5 builds needs no development exemption.
- The whole production middleware chain — security headers, panic recovery,
  request logging, HTTP metrics — is exercised by every development request the
  application serves.
- The dev origin outlives Go restarts, so a rebuild never drops the browser's
  HMR socket.
- No generated file participates in the topology, so the choice can be revisited
  without a migration for existing projects.
- The routing rule is Go, in `internal/dev`, and unit-testable without
  containers, without Node, and without a browser.

**Negative.**

- `nise dev` is a supervisor, and supervisors are where process-lifetime bugs
  live. It carries the cost of process groups, signal propagation, and
  shutdown ordering, per platform. Windows has no `SIGTERM`, so the graceful
  stop degrades to a kill there; that is stated in the code and in the command
  docs rather than papered over.
- The everyday loop serves documents with no CSP, so a policy violation in
  frontend code is found by `--embedded`, by the end-to-end suite, or in
  production — not by the loop itself.
- Three ports instead of two.
- The proxy is a component Nise now owns and must keep correct against future
  Vite behavior — most concretely, if a future Vite sets `hmr.clientPort` by
  default, the HMR socket would leave the single origin and this ADR's HMR
  argument would need re-verifying.

**Neutral.**

- The default ports are 8080 (proxy), 8081 (Go), 5173 (Vite), 5432 (PostgreSQL).
  8080 matches the application's own default `PORT` and what
  `deploy/compose.yaml` publishes, so the development URL and the production URL
  differ only in host.

**What would have to be true to revisit this.** Any of: SvelteKit gains a
development mode whose HTML is servable under a strict CSP, which would remove
the reason the everyday loop bypasses the policy and make a Go-served development
document possible; Vite changes its HMR client so the socket no longer follows
`location.host`, breaking the single-origin property; or the generated
application stops serving its frontend and its API from one origin, which would
remove the production topology this mirrors.
