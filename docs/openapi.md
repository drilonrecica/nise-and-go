# OpenAPI Server Bindings and Frontend Client

`api/openapi.yaml` is the generated application's authoritative HTTP
transport contract. Its paths are relative to the versioned router boundary,
and its `servers` entry declares `/api/v1` for clients and documentation. A
path `/widgets` in the document is registered at `/api/v1/widgets`; do not put
the prefix into every path a second time.

The initial operation is `GET /api/v1/`. It returns the direct `APIRoot`
resource `{ "version": "v1" }`, proving that the contract, generated strict
interface, application adapter, typed frontend client, and versioned router
are connected. It is not a liveness probe and exposes no process or dependency
state.

## Successful response shapes

A single resource is the response body itself; do not wrap it in `data` or
`result`. A collection is a concrete object with required `items` and `page`
members. `items` is always an array (`[]` when empty), and `page` is always a
non-null object. Both the resource and collection schemas are closed with
`additionalProperties: false` so an accidental generic envelope is contract
drift rather than an undocumented convention.

`APIRootCollection` demonstrates this shape in generated Go and TypeScript
models without inventing a public list endpoint. Define each domain collection
explicitly in OpenAPI—for example, `InvoiceCollection`—instead of adding a
generic runtime response type. Because a generated Go slice can still be nil,
each concrete server-side collection constructor normalizes nil items to an
empty non-nil slice before writing; the API-root fixture makes that rule
executable. The neutral `Page` requires only `has_more`, for a collection with
no paging strategy of its own; cursor and offset reporting contracts add their
distinct metadata without combining incompatible pagination semantics.

## Cursor pagination

`CursorPage` is the page contract for a cursor-paginated collection: required
`has_more`, plus `next_cursor` and `prev_cursor`, which are **absent** rather
than null when there is no page in that direction. `Cursor` is a closed string
schema bounded at 4096 bytes, and the reusable `CursorLimit`, `CursorAfter`,
and `CursorBefore` parameter components are what a domain list operation
references so every collection names the same three parameters.
`APIRootCursorCollection` is the executable fixture, on the same terms as
`APIRootCollection`: it proves the generated Go and TypeScript shapes without
inventing a public list endpoint.

The token itself is authenticated, versioned, expiring, and bound to the query
it was issued for; `runtime/pagination` owns that format and the generated
application owns the per-collection decisions. See
[Pagination](pagination.md) and
[ADR 0016](adr/0016-authenticated-cursor-pagination.md).

## Offset reporting pagination

`ReportPage` is the separate contract for reports where a page number is
meaningful: required `page`, `size`, `total`, `total_pages`, and `has_more`,
with the reusable `ReportPageNumber` and `ReportPageSize` parameters.
`APIRootReportCollection` is its fixture. It is deliberately not a variant of
`CursorPage`: the parameters differ, the schemas are disjoint, and a request
combining them is refused rather than resolved.

Pagination failures across both contracts are `400` with the
`invalid_pagination`, `cursor_expired`, or `report_too_deep` Problem code.

## Idempotent commands

A command worth not repeating declares the reusable `IdempotencyKey` header
parameter. `internal/platform/idempotency` records the response in the same
transaction as the command, so a rolled-back command leaves no claim, and
replays it byte-for-byte on a retry with the same key. The three public
refusals are `400 invalid_idempotency_key`, `409 idempotency_conflict` for a
concurrent attempt, and `422 idempotency_key_reuse` for a key replayed with a
different request; `idempotencyKey`, `idempotencyFingerprint`, and
`idempotencyProblem` in `api.go` are the transport half. See
[Idempotency for sensitive commands](idempotency.md).

## Files and ownership

| Path | Owner | Purpose |
|---|---|---|
| `api/openapi.yaml` | application | hand-edited authoritative contract |
| `api/oapi-codegen.yaml` | application | strict chi generation choices |
| `internal/platform/httpapi/openapigen/openapi.gen.go` | Nise/tool | complete checked-in oapi-codegen output |
| `internal/platform/httpapi/api.go` | application | explicit strict handler adapter, registration, cursor binding and page issuing |
| `internal/platform/httpapi/api_test.go` | application | route, collection, and cursor contracts through the real middleware core |
| `internal/platform/httpapi/httpjson/json.go` | application | bounded strict request decoder and response writer |
| `internal/platform/httpapi/httpjson/json_test.go` | application | malformed, duplicate, unknown, and oversized-body contracts |
| `internal/platform/httpapi/problem/problem.go` | application | validated RFC 9457 catalog and bounded writer |
| `internal/platform/httpapi/problem/problem_test.go` | application | shape, escaping, identity, and cause-isolation contracts |
| `internal/platform/idempotency/idempotency.go` | application | transaction-owned idempotent command executor and its store |
| `internal/platform/idempotency/idempotency_test.go` | application | real-PostgreSQL execution, conflict, rollback, retry, and expiry contracts |
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

API failures use [RFC 9457 Problem Details](https://www.rfc-editor.org/rfc/rfc9457.html) with
`Content-Type: application/problem+json`. The OpenAPI `Problem` schema closes
the public shape with `additionalProperties: false`: the standard `type`,
`title`, `status`, and `detail` members, optional `instance`, and exactly three
application extensions—`code`, `request_id`, and `correlation_id`. The Go and
TypeScript generators consume that one schema; the application does not keep
a second wire struct. Required text and identity members are explicitly
nonempty in both the schema and the Go definition validator.

The application-owned `problem` package exposes validated catalog accessors
for `400` (four distinct codes), `404`, `405`, `409`, `413`, `415`, `422`, and
`500`. Built-in `type` identifiers
are root-relative full paths under `/problems/`, titles and public details are
stable, and HTTP/JSON statuses come from the same definition. Definitions
bound every text field and restrict machine codes. `httpjson.WriteMediaType`
buffers and applies the existing 1 MiB response limit before committing the
Problem status or headers.

Generated request/response callbacks, API routing failures, and API panic
recovery all use this writer. The document, health, and operator surfaces keep
their established non-API formats. A `405` retains every method in chi's
`Allow` set, including methods added through chi's extension-method registry.
Both IDs must come from the validated request context; the writer restores
their response headers from that payload, so downstream header replacement
cannot create a body/header mismatch. Missing identity fails closed rather
than emitting a partially trustworthy Problem.

API recovery stages at most 1 MiB until a handler returns. A pre-commit panic
therefore discards status, headers, and partial bytes before writing one clean
500 Problem. Explicit flush/hijack and output beyond that bound switch to
passthrough so streaming and large non-JSON responses are not held in memory.
A panic after that irreversible point aborts the connection with
`http.ErrAbortHandler`; it never appends Problem JSON to an existing response.

Wrapped decoder/handler errors and panic values never cross the wire. Expected
4xx failures are not error-logged. A generated 5xx callback records only the
stable code, status, and concrete error type; recovery similarly records the
panic type, never its value. Public text is encoded with the standard JSON
encoder, including HTML escaping, rather than concatenated into JSON.

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
operation's 2xx responses; the API index is therefore typed as
`Promise<APIRoot>` and returns the direct resource without an envelope.
Invalid paths and unsupported methods fail TypeScript checking.

Every request uses the fixed same-origin `/api/v1` base, explicit
`credentials: 'same-origin'`, and an optional caller-provided `AbortSignal`.
The client intentionally has no configurable credential mode and no runtime
client dependency. Native bearer-token support is later work and must not
weaken the browser cookie default.

Non-2xx responses become `APIError` with the status, request ID, and parsed
JSON/text body. Fetch or success-body failures become `APITransportError`.
Both public messages are fixed and never interpolate bodies or causes;
`AbortError` is rethrown unchanged so cancellation remains distinguishable.
The generated model includes the typed `Problem` shape. The lightweight
client deliberately retains its generic non-2xx transport value until the UI
adds opinionated Problem rendering in M6-007; this avoids making a transport
wrapper choose presentation behavior.

Vitest 4.1.11 runs the initial Node-only client suite through `test:unit`.
`svelte-kit sync` is part of that script so it succeeds immediately after a
fresh install. Browser component and Playwright suites remain M6 work.
