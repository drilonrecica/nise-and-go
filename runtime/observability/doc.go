// Package observability provides the essential HTTP metrics blueprint §15
// requires (request count, duration, and in-flight requests), the
// documented seams a future database pool (M3) and background-job runner
// (M8) report through, and an optional, zero-cost-when-unused tracing
// interface. It has no dependency beyond the standard library and no
// mandatory collector: the blueprint's non-goals explicitly forbid a
// mandatory OpenTelemetry collector, and the dependency policy targets zero
// runtime dependencies through M2.
//
// # Collection is independent of exposition
//
// [Registry], [Counter], [Gauge], and [Histogram] are the collection API:
// they know nothing about how their values leave the process. [NewHandler]
// and [WriteText] are one exposition — Prometheus text format — built on
// top of that API using only [fmt], [io], and [strings]. Choosing a
// different exposition format later, or adopting the official Prometheus
// client library as the collection engine, changes what sits behind
// [Registry]'s constructors, not the call sites that construct metrics or
// record values against them.
//
// Prometheus text format was chosen over [expvar] because it needs no
// dependency to emit — it is newline-delimited plain text — and it is what
// the deployment targets this project already supports (a Prometheus
// server, or any OpenMetrics-compatible scraper such as Grafana Agent or
// VictoriaMetrics) actually expect on the wire. expvar's JSON shape would
// need a translation layer to feed the same tooling and carries no metric
// type (counter vs. gauge vs. histogram) or label model of its own.
//
// # Cardinality is a security and cost boundary
//
// Every labeled metric in this package is capped: [Registry.NewCounterVec],
// [Registry.NewGaugeVec], and [Registry.NewHistogramVec] each accept a
// MaxSeries in [VecOpts] (default [DefaultMaxSeries]) bounding the number
// of distinct label-value combinations tracked individually. One additional
// series, the overflow bucket, catches every combination past the cap. A
// Vec therefore never holds more than MaxSeries+1 series, no matter what a
// caller passes as label values — see [CounterVec.WithLabelValues] and the
// cardinality tests in cardinality_test.go.
//
// [HTTPMetrics.Middleware] labels every HTTP metric by method, route
// template, and status class — never by the request's raw URL path. A
// route template ("/widgets/{id}") is supplied by the caller through a
// [RouteTemplateFunc], typically reading it from the router (for example
// chi's RoutePattern) after routing has resolved; this package cannot
// import a router without breaking the framework's zero-dependency target,
// so it never invents one. See http_test.go for the test proving two
// distinct UUID paths that share a route template collapse to one series,
// and cardinality_test.go for the test proving that even a
// [RouteTemplateFunc] that mistakenly echoes the raw path back is still
// bounded by the cap.
//
// # The metrics endpoint is not public by default
//
// [NewHandler] returns a plain [net/http.Handler]. Nothing in this
// framework mounts it on an application's public router; [runtime/lifecycle]
// builds the public HTTP server and never references this package.
// Exposing metrics is the application's explicit choice, made in its own
// router wiring, and it should be made deliberately: bind the handler to a
// separate listener on a private network or localhost, or mount it behind
// the same authorization middleware guarding other operator-only routes.
// See docs/metrics.md for the deployment guidance.
//
// # Database-pool and job metrics are documented seams, not fabrications
//
// M3 (the database pool) and M8 (the job runner) do not exist yet in this
// repository. [PoolStats], [PoolStatsFunc], and [PoolMetrics] define the
// shape a pool implementation reports through; [JobMetrics] and
// [JobOutcome] define the shape a job runner reports through. Neither
// fabricates the system it will eventually measure — pool_test.go and
// job_test.go exercise both seams with a hand-written fake, not a real
// pool or job runner.
//
// # Tracing is a narrow, optional interface
//
// [Tracer] and [Span] name four operations — start a span, end a span,
// record an error, and propagate the span through a [context.Context] — and
// nothing else. This package imports no tracing SDK. [NoopTracer] is the
// default: it and the [Span] it returns do no work and allocate nothing,
// proven by the benchmark in trace_test.go using [testing.AllocsPerRun]. An
// application wires a real tracer (OpenTelemetry or otherwise) by
// implementing [Tracer] itself, outside this package, and passing that
// implementation wherever code in the application accepts a [Tracer] — see
// docs/metrics.md for a worked example.
package observability
