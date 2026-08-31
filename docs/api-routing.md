# API Routing and Middleware

Generated applications reserve `/api/v1` for the JSON API. API routes are
registered relative to that prefix through `httpapi.Deps.RegisterAPI`; a
handler registered as `/widgets/{id}` is therefore served at
`/api/v1/widgets/{id}`. The callback may be nil while an application has no
operations.

The API and document surfaces are sibling router chains, not one chain nested
inside the other:

| Surface | Paths | Security policy | Terminal behavior |
|---|---|---|---|
| API | `/api/v1`, `/api/v1/`, and every descendant | `secure.NewAPIPolicy` | unmatched requests are RFC 9457 API `404` responses |
| Document | health and operator endpoints, then every other path | `secure.NewDocumentPolicy` | the embedded single-page application |

This boundary is exact: `/api/v10` is a document path, while an unknown
`/api/v1/...` path never falls through to the SPA. The complete middleware
core is composed around the API router as an ordinary `http.Handler`, even
before the first operation is registered. This matters because chi does not
construct an otherwise-empty router's own middleware chain; relying on that
chain would return an API error without API security headers or request
instrumentation. Adding a wildcard fallback route was also rejected because
it would convert a known route's wrong-method response from `405` to `404`.

## Methods chi does not know

chi dispatches by an internal table of HTTP methods. A request whose method is
not in that table — WebDAV's `PROPFIND`, an invented `PURGE`, or the single
character `0` — is answered by `Mux.routeHTTP` **before** any mounted handler
runs. Left alone, that would be the only response the server produces with no
security headers, no Content-Security-Policy, no request or correlation ID, no
log line, and no metric, reachable by anyone willing to send an unusual verb.

The root dispatcher therefore installs an unsupported-method handler that sends
the request into the matching surface's own core. The core's inner router
reaches the same unknown-method branch one level down, so each surface still
produces its own `405` — an RFC 9457 Problem for the API, chi's plain response
for the document surface — this time with every invariant the rest of the
server holds. The API `405` still reports the methods the path does allow,
because the handler restores the routing path the way chi's own `Mount` does.

The boundary test is a path-segment test, not a string prefix: `/api/v10`
remains a document path here exactly as it does for a known method.

## Fixed middleware order

Both sibling chains use the same core order, outermost first:

1. security headers and the surface-specific CSP;
2. request/correlation IDs and request-scoped logging;
3. panic recovery;
4. HTTP metrics;
5. the surface's application middleware slot;
6. route dispatch.

Security is outermost so it overwrites any attempted downstream weakening and
also covers rejections, recovered panics, and `404` responses. Logging wraps
recovery so the recovery record carries the failing request's IDs. Metrics is
inside recovery because its panic path records the request and deliberately
re-panics for recovery to handle.

Nesting the API beneath the document chain was rejected: response middleware
unwinds from the inside out, so the outer document security middleware would
overwrite the stricter API CSP. A small unprotected root dispatcher selects
one complete sibling chain instead.

API `404`, `405`, and recovered `500` responses use the same controlled
Problem Details catalog as generated OpenAPI failures. Request and correlation
IDs in each body match the headers assigned by the outer logging middleware.
The custom `405` writer derives `Allow` from chi's route tree—including
registered extension methods—and matches it without executing a handler,
preserving normal method-discovery semantics. API recovery stages a bounded
response until normal return so a write-then-panic can become one clean
Problem. A flushed/hijacked stream is already irreversible; a later panic
aborts that connection instead of appending JSON. Document and operator
failures retain their existing formats.

## Application extension points

`httpapi.MiddlewareSlots` has separate `Document` and `API` slices. Entries run
in slice order, with the first entry outermost, but the entire slot remains
inside the fixed core. A nil entry fails router construction rather than
panicking on the first request.

Use the API slot for cross-operation concerns that genuinely apply to every
API operation. Route- or operation-specific middleware belongs in the
registration callback or generated strict-handler wiring. Do not call
`Use` on the unprotected root dispatcher: it exists only to choose the
surface, and putting behavior there would erase the independently testable
security boundary.

The generated `internal/platform/httpapi/router_test.go` locks the ordering,
separate policies, exact prefix behavior, unknown-method dispatch,
request-scoped panic logging, and metrics behavior.
`internal/platform/httpapi/transport_test.go` is the end-to-end contract over
the same handler: routes, method and content negotiation, strict input, both
pagination contracts, idempotency key handling, the response bound, and a fuzz
target that holds every response on the API surface to a status in range, the
security headers, valid request identity, the Problem media type on failure,
and no Go implementation detail in the body. Because `router.go` and its test are application-owned,
projects may deliberately change this contract together when their needs
diverge from the golden profile.
