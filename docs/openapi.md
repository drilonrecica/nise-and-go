# OpenAPI Server Bindings and Frontend Client

`api/openapi.yaml` is the generated application's authoritative HTTP
transport contract. Its paths are relative to the versioned router boundary,
and its `servers` entry declares `/api/v1` for clients and documentation. A
path `/widgets` in the document is registered at `/api/v1/widgets`; do not put
the prefix into every path a second time.

The initial document defines only `GET /api/v1/`, a bodyless `204` API index.
It is a compile and routing fixture proving that the contract, generated
strict interface, application adapter, and versioned router are connected. It
is not a liveness probe and exposes no process or dependency state.

## Files and ownership

| Path | Owner | Purpose |
|---|---|---|
| `api/openapi.yaml` | application | hand-edited authoritative contract |
| `api/oapi-codegen.yaml` | application | strict chi generation choices |
| `internal/platform/httpapi/openapigen/openapi.gen.go` | Nise/tool | complete checked-in oapi-codegen output |
| `internal/platform/httpapi/api.go` | application | explicit strict handler adapter and registration |
| `internal/platform/httpapi/api_test.go` | application | route-level contract through the real middleware core |
| `frontend/src/lib/api/schema.d.ts` | Nise/tool | complete checked-in openapi-typescript output |
| `frontend/src/lib/api/client.ts` | application | small typed fetch wrapper over the generated paths |
| `frontend/src/lib/api/client.test.ts` | application | credentials, cancellation, typing, and error contracts |

The generated package retains oapi-codegen's standard
`Code generated … DO NOT EDIT.` header, including the pinned generator
version. This is the deliberate exception to Nise's own generated-file header:
rewriting an external generator's header would make an ordinary regeneration
dirty. The `openapigen/` directory remains the structural ownership marker.

The TypeScript model likewise retains openapi-typescript's standard generated
header. That header does not print a version, so reproducibility comes from
the exact package pin, the checked-in bytes, and the generator's `--check`
gate. Its `src/lib/api/schema.d.ts` path is the structural ownership marker.

The adapter has a compile-time assertion against
`openapigen.StrictServerInterface`. Adding an operation to the document and
regenerating therefore breaks the application build until the application
implements that operation. The generated non-strict `ServerInterface` is only
the chi/wire adapter created by `NewStrictHandlerWithOptions`; application
handlers implement the context-and-request-object strict interface.

## Server generation

The generated project pins oapi-codegen v2.8.0 in its `go.mod` tool block.
Both supported regeneration paths resolve that exact module version:

```sh
make api-generate
go generate ./internal/platform/httpapi
```

`make api-check` generates to a temporary file and compares bytes without
modifying the checked-in binding. It fails with the regeneration command when
the OpenAPI document, config, pinned tool, or checked-in output disagree. It is
part of generated `make all` and works without Git, so it also protects source
archives and freshly generated trees that have not been committed yet.

Generation enables only `models`, `chi-server`, and `strict-server`. It does
not emit a Go client—the browser uses the separately generated TypeScript
model and application-owned wrapper—and it does not embed a second copy of
the specification into the application binary. The
readable source document is authoritative; request-validation behavior is
implemented and tested at the transport boundary rather than implied by an
unused embedded document.

## Error and validation boundary

Strict oapi-codegen bindings provide typed operation requests and closed
response objects, but they do not by themselves enforce every input rule. Body
limits, media-type checks, strict JSON semantics, and contract validation are
separate transport tasks.

Until the Problem Details writer is wired, the application adapter replaces
oapi-codegen's default error rendering with fixed `invalid request` and
`internal server error` bodies. Generated decoder errors and handler causes
are never copied to a response. Later error work replaces this seam without
editing generated code.

## Frontend generation

The generated frontend pins openapi-typescript 7.13.0 and TypeScript 5.9.3.
The TypeScript pin is intentionally on the latest supported 5.x patch:
openapi-typescript 7.13.0 declares a TypeScript `^5.x` peer, and a real clean
pnpm install reports the previous speculative TypeScript 6 pairing as
unsupported.

The equivalent generation and non-mutating check commands are:

```sh
make api-types-generate
pnpm --dir frontend run api:generate
make api-types-check
pnpm --dir frontend run api:check
```

The check uses openapi-typescript's own `--check` mode. Both server and
frontend drift gates are part of generated `make all`; direct frontend build
and type-check scripts also refuse stale models. Generation failure is tested
not to damage the last valid checked-in output.

## Lightweight client boundary

`client.ts` imports `paths` from the generated model and exposes only methods
that exist for a generated path. Successful values are inferred from the
operation's 2xx responses; the initial bodyless API index is therefore typed
as `Promise<undefined>`. Invalid paths and unsupported methods fail TypeScript
checking.

Every request uses the fixed same-origin `/api/v1` base, explicit
`credentials: 'same-origin'`, and an optional caller-provided `AbortSignal`.
The client intentionally has no configurable credential mode and no runtime
client dependency. Native bearer-token support is later work and must not
weaken the browser cookie default.

Non-2xx responses become `APIError` with the status, request ID, and parsed
JSON/text body. Fetch or success-body failures become `APITransportError`.
Both public messages are fixed and never interpolate bodies or causes;
`AbortError` is rethrown unchanged so cancellation remains distinguishable.
Problem Details adds a narrower typed public error body in M4-005 without
changing this transport boundary.

Vitest 4.1.11 runs the initial Node-only client suite through `test:unit`.
`svelte-kit sync` is part of that script so it succeeds immediately after a
fresh install. Browser component and Playwright suites remain M6 work.
