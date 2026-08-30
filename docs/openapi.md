# OpenAPI Server Bindings

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

The generated package retains oapi-codegen's standard
`Code generated … DO NOT EDIT.` header, including the pinned generator
version. This is the deliberate exception to Nise's own generated-file header:
rewriting an external generator's header would make an ordinary regeneration
dirty. The `openapigen/` directory remains the structural ownership marker.

The adapter has a compile-time assertion against
`openapigen.StrictServerInterface`. Adding an operation to the document and
regenerating therefore breaks the application build until the application
implements that operation. The generated non-strict `ServerInterface` is only
the chi/wire adapter created by `NewStrictHandlerWithOptions`; application
handlers implement the context-and-request-object strict interface.

## Generation

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
not emit a Go client (the browser client arrives separately), and it does not
embed a second copy of the specification into the application binary. The
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
