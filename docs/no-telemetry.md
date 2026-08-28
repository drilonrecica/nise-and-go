# No telemetry

Nise makes no implicit network request. No command phones home, checks for
an update on its own, or sends analytics — ever, without you typing a
command whose documented purpose is to reach the network. This is Global
Constraint 4 (`.superpowers/sdd/execution-plan/constraints.md`) and it is
enforced, not just claimed: `test/nonetwork` fails the build the moment it
stops being true.

## The guarantee

- Running `nise` — with no arguments, `--help`, `version`, `doctor`,
  `generate --help`, `test --help`, `check`, or `new` — opens no socket,
  performs no DNS lookup, and calls no analytics, crash-reporting, or
  update-check service.
- Nothing in this repository collects usage data, error reports, or any
  other telemetry, from Nise itself or from an application `nise new`
  generates.
- No hard-coded analytics hostname (PostHog, Segment, Sentry, Mixpanel,
  Google Analytics, or similar) appears anywhere in this source tree or in
  what it generates.

See `docs/cli-and-distribution.md`'s "Updates and privacy" section for the
product-level version of this promise: no self-update, no automatic update
check, no telemetry, and update information only through a future explicit,
user-invoked command.

## How it is enforced

Three automated proofs live under `test/nonetwork/` and run in CI on every
change (via `make test` / `make test-race` — see the note on wiring below):

1. **Static: the import graph.** `test/nonetwork/static_test.go` shells out
   to `go list -json` and inspects every package under `./cmd/...` and
   `./internal/...`. A package that directly imports a network-dialing
   standard library package (`net`, `net/http`, `net/rpc`, `net/smtp`,
   `crypto/tls`, and similar) must be named, with a written reason, in that
   file's `networkAllowlist`. A package that reaches one of those only
   *transitively*, through a named set of `runtime/` packages, must
   similarly be named in `transitiveNetworkAllowlist` — and that exception
   is scoped to the exact `runtime/` packages it names, not to all of
   `runtime/`, so a new network-capable dependency reached through any
   other path still fails the build. The same file also checks the
   companion assertion from ADR 0011: no package under `./runtime/...` may
   import anything under `internal/`.

2. **Dynamic: real subprocesses, a trap instead of a network.**
   `test/nonetwork/dynamic_test.go` builds the actual `nise` binary and
   runs it — as a real subprocess, not an in-process function call — for
   every non-interactive command, with `HTTP_PROXY`, `HTTPS_PROXY`, and
   `ALL_PROXY` pointed at a local listener that records every connection it
   receives and then refuses it, and `GOPROXY=off`. The test asserts that
   listener recorded nothing. This is the proof that matters most: it
   catches a subprocess nise spawns reaching out on its own (`doctor`'s
   `go version`, `node --version`, `docker --version` checks, for example),
   which no import-graph analysis can see.

   Stated honestly: pointing `HTTP_PROXY` at a trap only constrains Go code
   that builds an `http.Client`/`http.Transport` without overriding
   `Proxy` — the default, but not the only possibility. A raw `net.Dial`
   ignores proxy environment variables entirely. `test/nonetwork/netns_test.go`
   adds a stricter, best-effort layer for exactly that gap: it re-runs the
   same commands inside a Linux network namespace with no interface but a
   loopback that is never brought up, so *any* connection attempt —
   proxied or not — fails immediately at the kernel. It needs no root (an
   unprivileged user+network namespace, the same mechanism `unshare
   --user --net` uses), and skips itself cleanly with a stated reason on a
   sandbox that forbids that.

3. **Telemetry markers and hard-coded hosts.**
   `test/nonetwork/telemetry_test.go` greps every non-test `.go` and
   `.tmpl` file under `cmd/`, `internal/`, `runtime/`, and `templates/` —
   `templates/` because it is copied verbatim into every application
   `nise new` generates — for known analytics SDK names (`posthog`,
   `segment`, `sentry`, `mixpanel`, `google-analytics`, and similar) and
   for any hard-coded `https://` or `http://` host that is not in that
   file's own short, documented `httpsHostAllowlist`. Every allowlisted
   host is a documentation link, a spec citation, an install-instruction
   URL, an XML namespace, or a placeholder shown in an error message — never
   a real destination — and each entry says which. A violation fails with
   the exact file and line.

Run all three, plus everything else in `test/nonetwork`, with:

```sh
go test ./test/... -race -count=1
```

`make test` and `make test-race` already run `./test/...` (see the
`GO_PACKAGES` comment in the `Makefile`), so this suite runs as part of
`make check` and the same CI job that runs it.

## What this does not claim

- **`nise dev` is not an exception to be tracked here — it is the
  explicit, user-invoked purpose of that one command.** It runs a loopback
  reverse proxy in front of the application and Vite dev servers you asked
  it to supervise, and it does not exist until you type `nise dev` inside
  a project you created. `docs/commands/dev.md` and
  `docs/adr/0012-dev-loop-proxy-topology.md` describe that topology; it is
  outside `test/nonetwork`'s dynamic proof because it cannot run
  non-interactively (it needs a generated project and an installed
  frontend toolchain).
- **A container image pull is not telemetry, and it is not Nise's own
  network access.** `nise dev` supervises a PostgreSQL container through
  your installed container runtime (Docker or Podman). If that image is
  not already cached locally, the container runtime — not the `nise`
  process — fetches it, the same way it would for any `docker run` or
  `podman run` you typed yourself. This is a subprocess doing exactly what
  you asked it to do, through tooling you installed and configured
  yourself; it is a meaningfully different claim from "nise silently
  reached the network," and this page states that distinction precisely
  rather than blur it into one guarantee.
- **There is currently no explicit, user-invoked network command at all**
  — no `nise upgrade`, no update checker, nothing. `docs/cli-and-distribution.md`
  describes one as planned for a later milestone: an explicit check that
  reports install-channel-appropriate instructions, never run implicitly.
  When it exists, it becomes the one documented exception to "no command
  performs an implicit network request" — implicit being the operative
  word, since running it is still something you typed.
