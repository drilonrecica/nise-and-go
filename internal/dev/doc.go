// Package dev implements the machinery behind `nise dev`: the single-origin
// development reverse proxy, the container-runtime probe and PostgreSQL
// lifecycle, the polling source watcher, and the child-process supervisor.
//
// # Topology
//
// The browser talks to exactly one origin, and that origin is served by the
// `nise dev` process itself:
//
//	browser ──▶ http://127.0.0.1:8080        (Proxy, in this package)
//	                │
//	                ├─ /api, /healthz, /internal ──▶ Go application child (127.0.0.1:8081)
//	                └─ everything else           ──▶ Vite dev server      (127.0.0.1:5173)
//
// The reasoning, the alternative that was rejected (Vite owning the origin
// and proxying /api to Go), and the constraint this imposes on the later
// cookie-prefix decision are in docs/adr/0012-dev-loop-proxy-topology.md.
// The operator-facing description is docs/commands/dev.md.
//
// # Why the proxy lives here and not in the generated application
//
// A reverse proxy that forwards to an arbitrary loopback port is a handler
// you never want compiled into a production binary, gated on nothing but an
// environment variable. Keeping it in the nise CLI — a tool that is never
// deployed — means the generated application contains no dev-proxy code at
// all, and it also means the developer-facing origin outlives every restart
// of the Go child, so a Go rebuild never drops the browser's connection to
// Vite's hot-module-reload socket.
//
// # Zero runtime dependencies
//
// Everything here is standard library. There is no fsnotify: the watcher is
// a tuned stat poller (see Watcher) whose measured cost on a generated
// project is documented in docs/commands/dev.md.
package dev
