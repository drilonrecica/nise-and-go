# Runtime Packages

`runtime/` is the only Go API generated applications import. Everything else in the repository — `cmd/`, `internal/`, `templates/`, `test/`, `examples/` — is private and may change in any release.

The package list and the boundary rules are fixed by [ADR 0011](adr/0011-runtime-public-api.md), as amended by [ADR 0015](adr/0015-use-case-owned-transactions.md), [ADR 0016](adr/0016-authenticated-cursor-pagination.md), [ADR 0017](adr/0017-security-primitives-in-runtime.md), and [ADR 0019](adr/0019-trusted-proxy-configuration.md). This page is the index and the status board.

Import path prefix: `github.com/drilonrecica/nise-and-go/runtime/…` ([ADR 0007](adr/0007-module-path-and-owner.md)).

## Packages

| Package | Purpose | Status | Task |
|---|---|---|---|
| `runtime` | Documentation only. Declares the stability policy; exports nothing. | implemented | M2-009 |
| `runtime/config` | Typed environment configuration, `_FILE` secret variants, fail-closed production validation. | implemented | M2-001, M2-002, M2-003 |
| `runtime/logging` | Structured JSON and human-readable logs over `log/slog`, request and correlation IDs, central redaction. | implemented | M2-004, M2-005 |
| `runtime/health` | Startup, liveness, and readiness checks and their handlers. | implemented | M2-006 |
| `runtime/lifecycle` | Process modes `all`, `web`, and `worker`; HTTP server construction; graceful shutdown and bounded draining. | implemented | M2-007, M2-008 |
| `runtime/observability` | HTTP, database-pool, and job metrics, and the optional tracing interface. | implemented | M2-011 |
| `runtime/secure` | Security headers and Content Security Policy for the embedded frontend. | implemented | M2-012 |
| `runtime/transaction` | Driver-independent use-case transaction lifecycle, rollback bounds, and closed isolation/access options. | implemented | M3-005 |
| `runtime/pagination` | Authenticated versioned cursor tokens, key rotation, cursor parsing, and the separate offset reporting contract. | implemented | M4-007, M4-008 |
| `runtime/password` | Argon2id hashing, versioned parameter sets, rehash detection, constant-cost dummy verification, and parameter benchmarking. | implemented | M5-004 |
| `runtime/session` | Opaque session tokens, their stored digests, the idle/absolute/revoked lifecycle rules, and the browser cookie policy. | implemented | M5-001, M5-015 |
| `runtime/forwarded` | Trusted-proxy policy, and the real client address, scheme, and host behind it. | implemented | M5-014 |

`runtime/internal/…` is reserved for shared implementation. No such package exists yet. Every `runtime/` package compiles against the standard library alone, except `runtime/password`, which uses `golang.org/x/crypto/argon2` because Argon2id is not in the standard library and a hand-written password KDF is exactly the thing this project must not contain. If one is added, the Go toolchain makes it invisible to applications, and it will carry no compatibility promise.

Compile-time modules live under `modules/`, are selected at generation time, and are recorded in the project recipe ([ADR 0010](adr/0010-project-recipe-format.md)). They are a separate public surface with the same stability policy.

## Boundary rules

- `net/http` middleware lives in the package that owns the concern. There is no middleware aggregator package. The ordered sibling API/document chains and their application extension slots are generated application code in `internal/platform/httpapi/router.go` ([API routing and middleware](api-routing.md), [ADR 0009](adr/0009-generated-application-layout.md)).
- A `runtime/` package may import the standard library and, should one ever exist, `runtime/internal/…`.
- A `runtime/` package may import another `runtime/` package only along an edge named in ADR 0011. Today that is exactly one: `runtime/lifecycle` may import `runtime/config`, `runtime/logging`, and `runtime/health`. In particular, `runtime/logging` must not import `runtime/config`.
- No `runtime/` package imports `internal/`, `cmd/`, `templates/`, `test/`, or `examples/`.
- `runtime/` contains no generators, templates, CLI presentation, network clients, update checks, telemetry, global mutable state, `init()` side effects, or reflection-driven dependency injection.

## Stability

`0.x` is explicitly unstable. Breaking changes are permitted in any `0.x` release and ship with a changelog entry and migration notes ([Versioning and compatibility](versioning.md)). From `1.0`, documented exported identifiers here follow semantic versioning.

Every exported identifier in `runtime/` carries a doc comment. Adding, removing, or retyping one is a contract change: it requires the doc comment, a row change on this page, and an explicit line in the commit message stating what the surface gained or lost.
