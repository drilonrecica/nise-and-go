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
- Nise never replaces, relocates, or re-execs its own binary. There is no
  self-update path, and no command leaves a staged replacement behind.
- Nise keeps **no state of its own**. Running any command against a fresh
  home directory leaves nothing named for nise anywhere in it — no
  configuration file, no cache, no "last update check" timestamp, no queued
  event.

See `docs/cli-and-distribution.md`'s "Updates and privacy" section for the
product-level version of this promise: no self-update, no automatic update
check, no telemetry, and update information only through a future explicit,
user-invoked command.

## How it is enforced

Five automated proofs live under `test/nonetwork/` and run in CI on every
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
   listener recorded nothing. This is the proof that matters most, within
   a specific, stated boundary: it catches a subprocess nise spawns that is
   itself proxy-aware — Go's own `net/http`, and most language HTTP
   clients, `curl`, `git`, and similar tools that honor
   `HTTP_PROXY`/`HTTPS_PROXY` — even when that subprocess's own output is
   discarded, because the connection attempt lands on the recording
   listener regardless of what the caller does with the result.

   It does **not** catch a proxy-unaware subprocess. A tool that dials
   directly — `nc`, a raw socket client, or any process that never
   consults proxy environment variables — bypasses the trap entirely: the
   static check sees no forbidden Go import (the network code lives
   outside this binary's own compiled graph), the proxy trap is never
   contacted because the tool never asked where to send the connection,
   and if that subprocess's result is discarded, the network-namespace run
   (below) leaves no exit-code or output difference to notice either — a
   successful connection outside the namespace and a failed one inside it
   can both be silently swallowed by a caller that never checks. This is a
   real, not hypothetical, limit: nise does not currently invoke any tool
   of this shape (`doctor`'s `go version`/`node --version`/`docker
   --version` checks are simple version probes, chosen and reviewed for
   exactly this property), but a *future* change that wired in a
   proxy-unaware subprocess whose result nise ignored would not be caught
   by any of the three proofs on this page.

   Stated honestly, on the gap that remains even for proxy-aware code:
   pointing `HTTP_PROXY` at a trap only constrains Go code that builds an
   `http.Client`/`http.Transport` without overriding `Proxy` — the
   default, but not the only possibility. A raw `net.Dial` ignores proxy
   environment variables entirely. `test/nonetwork/netns_test.go`
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
   the exact file and line. `127.0.0.1`, `localhost`, `0.0.0.0`, and `::1`
   are recognized as loopback and never need an allowlist entry, matched
   by exact string equality — deliberately not by prefix, since a prefix
   match would let `127.0.0.1.evil-telemetry.example` walk through as
   "loopback" and skip the check it exists to run.

   Stated honestly, on what this pattern-based match does not reach: the
   host-extraction regex only recognizes DNS-label-shaped hosts (letters,
   digits, hyphens, dots) and stops at the first character outside that
   set — a port's `:`, a path's `/`, a query's `?`. It does not parse a
   bracketed IPv6 literal (`http://[::1]/...` is not matched at all, so
   the `::1` allowlist-adjacent entry above is presently unreachable
   rather than exploitable — a non-issue for what this check protects,
   since loopback is never the real-destination case it looks for) or a
   host built at runtime through string concatenation or a
   `fmt.Sprintf` template, which no source-text grep can see. Those are
   both gaps this proof shares with any pattern-based static check; they
   are why the dynamic and static-import proofs above, and the artifact
   proofs below, exist as separate independent layers rather than this one
   being asked to catch everything.

4. **No self-replacement, and no state of its own.**
   `test/nonetwork/selfupdate_test.go` is the half of this page that is not
   about the network at all. Before a self-updater can reach one it has to
   replace the running binary, and before an update checker or a telemetry
   client can reach one it has to remember something between runs — so those
   two mechanisms are checked directly, and mechanically:

   - Every non-interactive command runs from a **private copy** of the binary,
     which is hashed before and after. A self-update's entire purpose is to end
     with different bytes on disk than it started with; if the bytes never
     change, no command has one, whatever the source says. Size and mode are
     compared separately from the hash, so a permission change staged for a
     later replacement is visible too, and the directory is checked for a
     second file appearing beside the binary.
   - Every command runs against an **empty home directory**, with `HOME`,
     `USERPROFILE`, `APPDATA`, `LOCALAPPDATA`, and all four `XDG_*` variables
     pointed into it, and the test asserts nothing named for nise appears
     anywhere underneath. An update checker that does not record when it last
     looked is not a checker but a beacon, and a telemetry client has to buffer
     events between flushes; both need a durable file, and nise has none.
   - A static grep refuses `os.Executable`, `syscall.Exec`, `os.UserHomeDir`,
     `os.UserConfigDir`, and `os.UserCacheDir` in production source without an
     allowlist entry giving the reason. None of those is forbidden in
     principle; nise simply has no reason to reach for one, so an appearance
     should be argued rather than discovered.

   The home-directory check is deliberately "nothing **named for nise**"
   rather than "the directory is untouched": `nise doctor` runs `go version`,
   and the Go toolchain writes its own cache. That is the same distinction
   this page already draws for a container image pull below — a spawned tool
   doing what you asked is a different claim from nise keeping state.

5. **The artifact, not the source.**
   The three proofs above read this repository. Two more read what is actually
   built and shipped:

   - `go version -m` on the compiled binary lists every module linked into it,
     including one pulled in transitively by a dependency nobody read — which
     is how an analytics SDK realistically arrives. That list is checked
     against the same telemetry markers.
   - A project is **generated** into a scratch directory and every one of its
     files is scanned. `templates/` is the input; the generated tree is the
     output, and they are not the same file set — the generator composes,
     renames, and writes files that exist in no template, and a generated
     frontend manifest names dependencies no `.go` or `.tmpl` file mentions.

Run all five, plus everything else in `test/nonetwork`, with:

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
- **`nise db status` and `nise db migrate` explicitly build and run the
  generated application.** Nise does not open a network connection itself,
  but the `go build` subprocess may resolve modules missing from the local
  cache through the developer's configured Go proxy. The resulting
  application then connects to the operator-configured `DATABASE_URL` because
  that is the command's requested purpose. `docs/commands/db.md` documents the
  process and credential boundaries.
- **There is currently no Nise-owned network utility at all**
  — no `nise upgrade`, no update checker, nothing. `docs/cli-and-distribution.md`
  describes one as planned for a later milestone: an explicit check that
  reports install-channel-appropriate instructions, never run implicitly.
  When it exists, it becomes the one documented exception to "no command
  performs an implicit network request" — implicit being the operative
  word, since running it is still something you typed.
