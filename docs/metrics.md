# Metrics and the optional tracing interface

`runtime/observability` ([ADR 0011](adr/0011-runtime-public-api.md)) is how a generated application collects the essential HTTP metrics the blueprint calls for (blueprint §15: "Essential HTTP, database-pool, and job metrics"), exposes them without a mandatory collector, and optionally wires in distributed tracing behind a narrow interface. This page documents the current, implemented behavior — it is not a design proposal.

`runtime/observability` imports only the standard library. Adding it introduces no new dependency to this repository's `go.mod` — see [dependencies.md](dependencies.md).

## The exposition decision

Metric **collection** ([`Registry`](../runtime/observability/registry.go), `Counter`, `Gauge`, `Histogram`) is a separate concern from metric **exposition** (how the collected values leave the process). Application code that constructs metrics and calls `Inc()`/`Observe()` never touches an exposition format directly.

The one exposition format this package ships is Prometheus text format, via `observability.NewHandler` and `observability.WriteText`. Two formats were considered:

- **Prometheus text format** — plain, newline-delimited text (`name{labels} value`), defined by the [Prometheus exposition format spec](https://prometheus.io/docs/instrumenting/exposition_formats/). It needs no dependency to *emit*: the implementation here is built from `fmt`, `io`, `sort`, and `strings`. It is also what the deployment targets this project already supports actually expect on the wire — a Prometheus server, or any OpenMetrics-compatible scraper (Grafana Agent, VictoriaMetrics, the Datadog agent's Prometheus check) polls an HTTP endpoint for exactly this text shape.
- **`expvar`** — the standard library's own mechanism, but it publishes a JSON tree with no metric type (counter vs. gauge vs. histogram), no label model, and no cumulative-histogram-bucket convention. Feeding it to Prometheus-family tooling would need a translation layer Prometheus text format does not.

Prometheus text format won. **This decision is not locked in**: `Registry`, `Counter`, `Gauge`, and `Histogram` know nothing about exposition, so adopting the official Prometheus client library later as the collection engine — or adding a second exposition format alongside this one — changes what sits behind the `Registry` constructors, not the call sites that construct metrics or record values against them.

## The metrics

### HTTP (`observability.NewHTTPMetrics`)

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `http_requests_total` | counter | `method`, `route`, `status_class` | Total HTTP requests. |
| `http_request_duration_seconds` | histogram | `method`, `route`, `status_class` | Request duration; buckets from `DefaultHTTPDurationBuckets` (5ms–10s). |
| `http_requests_in_flight` | gauge | `method` | Requests currently being served. **Labeled by method only, not route** — see below. |

Wire it into your router with `HTTPMetrics.Middleware(routeTemplate)`, where `routeTemplate` is a `func(*http.Request) string` your application supplies, typically reading the matched route from your router after routing has resolved (for chi: `chi.RouteContext(r.Context()).RoutePattern()`, read only after calling the wrapped handler — chi does not finish populating the route context until then). `runtime/observability` does not import chi itself: doing so would make chi a dependency of the framework's own `runtime/` tree, which the current milestone's zero-runtime-dependency target forbids. A request the router never matched, or a nil `routeTemplate`, is labeled `"unmatched"`.

**Why `http_requests_in_flight` has no route label.** A chi-style router only finishes resolving the route template once the handler chain has run, but the in-flight gauge must increment *before* calling the wrapped handler and decrement *after*. There is no route template available yet at the point the gauge needs to move. Rather than fake it with a two-phase, request-scoped label update (fragile, and one more thing that can desynchronize on a panicking handler), the in-flight gauge is labeled by method only. `http_requests_total` and `http_request_duration_seconds` are recorded once the handler returns *or panics* (see below), when the route template is known, so they carry the full label set.

**A panicking handler is still counted.** `Middleware` records the count and duration observation from a deferred function, not only on a normal return, so a handler that panics is recorded exactly like one that returns normally — labeled with a seventh, fixed `status_class` value, `"panic"`, regardless of anything the handler wrote to the response before panicking. `Middleware` does not recover the panic: it records the observation and then lets the panic continue propagating to whatever recovery middleware, if any, sits further out in your chain. A crash is precisely the request class an operator most wants visible in `http_requests_total`; silently dropping the observation on panic would make the worst-case request the one category never counted.

**Streaming and WebSocket upgrades work through `net/http.ResponseController`.** The wrapper `Middleware` puts around your `http.ResponseWriter` (to capture the status code) implements an `Unwrap() http.ResponseWriter` method, which is the standard library's own mechanism ([`net/http.ResponseController`](https://pkg.go.dev/net/http#ResponseController), available at this project's `go 1.26` directive) for reaching `Flush`, `Hijack`, `SetReadDeadline`, and friends through a chain of wrapping `ResponseWriter`s. A handler behind `Middleware` that needs to stream a response (Server-Sent Events) or hijack the connection (WebSocket upgrade) must reach those methods through `http.NewResponseController(w).Flush()` / `.Hijack()` rather than a direct type assertion on `w` — a direct assertion on the wrapper itself does not see through to the underlying writer, `ResponseController`'s `Unwrap`-chain walk does. This is what M8-009 (SSE notifications) is expected to use once it lands.

### Database pool (`observability.NewPoolMetrics`) — a seam, not an implementation

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `db_pool_max_conns` | gauge (sampled on scrape) | `pool` | Configured maximum. |
| `db_pool_open_conns` | gauge (sampled on scrape) | `pool` | Currently open, idle or in use. |
| `db_pool_idle_conns` | gauge (sampled on scrape) | `pool` | Currently idle. |
| `db_pool_in_use_conns` | gauge (sampled on scrape) | `pool` | Currently checked out. |

No pool package exists in this repository yet (the blueprint places pgxpool through sqlc at M3, blueprint §7). `observability.PoolStats` and `observability.PoolStatsFunc` are the seam a future pool implementation is expected to satisfy — most directly by adapting `pgxpool.Pool.Stat()`. Call `PoolMetrics.Register(poolName, statsFunc)` once per pool at startup; `statsFunc` is invoked live, on every scrape, not pushed on a schedule, because a pool already tracks these counts internally. `runtime/observability`'s own test suite (`pool_test.go`) exercises this seam with a hand-written fake pool — never a real one, because a real one does not exist yet.

### Background jobs (`observability.NewJobMetrics`) — a seam, not an implementation

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `jobs_completed_total` | counter | `queue`, `kind`, `outcome` | `outcome` is one of `success`, `failure`, `discarded`. |
| `job_duration_seconds` | histogram | `queue`, `kind` | Buckets from `DefaultJobDurationBuckets` (100ms–10min). |

No job runner exists in this repository yet (the blueprint places River through pgx at M8, blueprint §11). `JobMetrics.Observe(queue, kind, outcome, duration)` is the seam a future job runner is expected to call once per job completion. `job_test.go` exercises this seam with a hand-written fake job runner, for the same reason as the pool seam above: this package does not fabricate a system it does not yet measure.

## Cardinality is a security and cost boundary

A metrics endpoint an unauthenticated client can influence the shape of is a memory-exhaustion vector: label a counter by raw request path, and a script generating fresh UUIDs creates one new time series per request, without limit, until the process runs out of memory. Two rules hold across every metric in this package:

1. **Label by route template, never by raw path.** `HTTPMetrics.Middleware` labels `route` from your `RouteTemplateFunc`'s return value — `"/widgets/{id}"`, not `"/widgets/3fae2b91-..."` — so two requests differing only in a UUID collapse to the same series. `runtime/observability`'s test suite proves this directly: `TestHTTPMetricsRawUUIDPathDoesNotCreateNewSeries` (in `cardinality_test.go`) shows a correct `RouteTemplateFunc` producing exactly one series across many distinct UUID paths.

2. **Every labeled metric enforces a hard cap, with an explicit overflow bucket.** `VecOpts.MaxSeries` (default `DefaultMaxSeries` = 200) bounds the number of distinct label-value combinations a `CounterVec`, `GaugeVec`, or `HistogramVec` tracks individually. Once that cap is reached, **every further combination collapses into one additional shared series**, whose label values are all replaced by the fixed string `"overflow"` — so a Vec never holds more than `MaxSeries + 1` series in total, no matter what a caller passes as label values. This is the safety net behind rule 1: even a `RouteTemplateFunc` that is buggy or deliberately mislabels a metric with something unbounded cannot grow a metric's cardinality past that fixed ceiling. `TestCounterVecCardinalityCapHolds` and the second subtest of `TestHTTPMetricsRawUUIDPathDoesNotCreateNewSeries` (both in `cardinality_test.go`) prove this holds even when a `RouteTemplateFunc` echoes the raw, attacker-influenced path back.

This is why `db_pool_*` and `jobs_*`/`job_*` metrics are labeled by `pool`, `queue`, and `kind` — fixed, startup-time identifiers an application chooses when it constructs a pool or registers a job type — and never by anything request- or job-argument-controlled. `JobMetrics.Observe`'s doc comment states the same rule for `queue`/`kind` explicitly, and `TestJobMetricsSeamCardinalityCapHolds` proves the cap holds there too if that rule is broken.

## The metrics endpoint is not public by default

`observability.NewHandler(reg)` returns a plain `net/http.Handler` rendering `reg` in Prometheus text format. **Nothing in this framework mounts it automatically.** No package under `runtime/` — not `runtime/lifecycle`, which builds the application's public HTTP server, and nothing else in `runtime/observability` itself — references the public router at all. Exposing metrics is an application's explicit choice, made in its own router wiring, and it should be made deliberately:

- **Preferred: a separate listener.** Bind `observability.NewHandler(reg)` to its own `net/http.Server` on a private network interface or `localhost`, so a Prometheus server (or another OpenMetrics-compatible scraper) on the same host or private network can reach it without it ever being exposed publicly.
- **If it must share a listener:** mount it on the public router only behind the same authorization middleware guarding your other operator-only endpoints (compare `health.NewReadinessReportHandler`, which carries the identical warning for the same reason — see [operations-runtime.md](operations-runtime.md)). Never mount it at a path an unauthenticated caller can reach: metrics reveal request volume, timing, and route topology that a health endpoint's fixed two-body contract deliberately withholds.

## The tracing seam

`observability.Tracer` and `observability.Span` name exactly four operations — matching the blueprint's own scope for this seam (blueprint §15: "Optional tracing through a narrow interface; no mandatory collector"):

```go
type Tracer interface {
    Start(ctx context.Context, name string) (context.Context, Span)
}

type Span interface {
    End()
    RecordError(err error)
}
```

`Start` both begins the span and propagates it: the returned `context.Context` is what carries the span through the rest of a call chain, exactly the way `context.WithValue` is used elsewhere in Go. `runtime/observability` imports no tracing SDK — OpenTelemetry or otherwise — anywhere in its own source.

### The default: `NoopTracer`

`observability.NoopTracer{}` is the zero-cost default every generated application gets unless it explicitly wires something else. Its `Start` returns the given context unchanged alongside a `Span` whose `End` and `RecordError` do nothing. Both are backed by a zero-size struct (`noopSpan{}`); converting a zero-size Go value to an interface does not heap-allocate, which is what makes `NoopTracer` genuinely free rather than merely cheap. `trace_test.go`'s `TestNoopTracerAllocsPerRun` proves it with `testing.AllocsPerRun`, over 1,000 iterations of `Start` + `RecordError` + `End`:

```
NoopTracer: 0 allocations/op over 1000 runs
```

### Wiring a real tracer

An application wires a real tracer (OpenTelemetry or otherwise) by implementing `observability.Tracer` itself, in its own package — importing whatever SDK it chooses there, never inside `runtime/observability` — and passing that implementation wherever application code accepts a `Tracer`. A minimal OpenTelemetry-backed adapter looks like:

```go
package tracing

import (
    "context"

    "github.com/drilonrecica/nise-and-go/runtime/observability"
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/trace"
)

type otelTracer struct {
    tracer trace.Tracer
}

func NewOTelTracer(name string) observability.Tracer {
    return otelTracer{tracer: otel.Tracer(name)}
}

func (t otelTracer) Start(ctx context.Context, name string) (context.Context, observability.Span) {
    ctx, span := t.tracer.Start(ctx, name)
    return ctx, otelSpan{span: span}
}

type otelSpan struct{ span trace.Span }

func (s otelSpan) End() { s.span.End() }

func (s otelSpan) RecordError(err error) {
    if err != nil {
        s.span.RecordError(err)
    }
}
```

This adapter, and the `go.opentelemetry.io/otel` dependency it pulls in, live in the *application's* module graph, never in this repository's. Nothing here changes when an application does this — `observability.Tracer` is the fixed contract on both sides.
