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
| `internal/platform/httpapi/httpjson/json.go` | application | bounded strict request decoder and response writer |
| `internal/platform/httpapi/httpjson/json_test.go` | application | malformed, duplicate, unknown, and oversized-body contracts |
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

The application-owned generation config vendors oapi-codegen v2.8.0's two
strict server templates inline using its supported `user-templates` option.
The request template adds an injected decoder option; the response template
passes an injected size checker into response visitors before headers commit.
Multi-content operations also use an injected exact media-type matcher and
explicitly reject an undeclared type instead of passing an empty body object
to application code. The application adapter supplies all three policies from
`httpjson`. Keeping
the templates inline makes `make api-generate` and package-scoped
`go generate` consume the same local bytes. They must be reviewed against the
pinned upstream defaults whenever oapi-codegen is upgraded.

## Error and validation boundary

Strict oapi-codegen bindings provide typed operation requests and closed
response objects. The generated adapter additionally routes every declared
JSON request body through one application policy:

- at most 1 MiB is read;
- the parsed media type must exactly match the declared JSON media type, with
  parameters such as `charset=utf-8` allowed;
- a non-empty body contains exactly one JSON value;
- object keys may not repeat, including case variants and nested objects;
- container nesting may not exceed 100 levels;
- unknown struct fields and normal Go type mismatches are rejected.

An optional empty or whitespace-only body remains absent. The same body on a
required operation is invalid. An exceeded request bound maps to `413`, a
wrong or missing media type on a non-empty JSON body maps to `415`, and other
request parsing failures map to `400`.

Duplicate detection canonicalizes Unicode simple-fold cycles into map keys,
so lookup cost grows with input size rather than comparing every attacker-
controlled key with every earlier key. Multi-content operations compare the
parsed media type exactly and case-insensitively; a prefix such as
`application/json-patch+json` never selects an `application/json` body.
An optional multi-content request with zero body bytes remains absent even
when none of its declared representations is JSON. A nonempty body still
requires a declared Content-Type and cannot use that absence path.

Generated JSON responses are already buffered by oapi-codegen and are refused
when the encoded body exceeds 1 MiB, before any success header is committed.
Hand-written JSON endpoints use `httpjson.Write` to get the same bound and
pre-commit behavior. Streaming and non-JSON responses are intentionally not
buffered by this policy.

Until the Problem Details writer is wired, the application adapter uses fixed
public text for `400`, `413`, `415`, and `500` responses. Generated decoder
errors and handler causes are never copied to a response. M4-005 replaces this
temporary rendering seam without changing the strict decoding policy.

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
