package observability

import (
	"net/http"
	"time"
)

// defaultHTTPDurationBuckets backs [DefaultHTTPDurationBuckets]. It is
// unexported, and every reader gets a copy, because a package-level
// exported slice is package-level mutable state: assigning one element of
// it would change the bucket bounds of every histogram constructed
// afterwards, process-wide. runtime/doc.go and ADR 0011 both promise that
// nothing in runtime holds global mutable state.
var defaultHTTPDurationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// DefaultHTTPDurationBuckets returns the histogram bucket boundaries, in
// seconds, [NewHTTPMetrics] uses for its request-duration histogram. They
// are the Prometheus client libraries' own conventional default buckets,
// spanning 5ms to 10s.
//
// The returned slice is a fresh copy; mutating it has no effect on the
// package default, matching [logging.DefaultDenyKeys]'s contract.
func DefaultHTTPDurationBuckets() []float64 {
	return copyFloats(defaultHTTPDurationBuckets)
}

// unmatchedRoute labels a request Middleware's [RouteTemplateFunc] could
// not resolve to a template — typically a 404 the router never matched to
// any handler. It is a fixed string, not the request's raw path, so a
// flood of requests to nonexistent routes still produces exactly one
// series.
const unmatchedRoute = "unmatched"

// HTTPMetrics holds the essential HTTP metrics blueprint §15 calls for:
// request count and duration, labeled by method, route template, and
// status class, and in-flight requests, labeled by method. Construct one
// with [NewHTTPMetrics] and wrap your router's top-level handler with
// [HTTPMetrics.Middleware].
//
// The in-flight gauge is labeled by method only, not by route: chi (and
// routers with a similar design) only finishes populating the matched
// route template once the handler chain has run, so a route label is not
// yet known at the point Middleware must increment the gauge, before
// calling the wrapped handler. Method-only in-flight tracking avoids that
// ordering problem entirely rather than working around it with a
// two-phase, request-scoped label update.
type HTTPMetrics struct {
	requests *CounterVec
	duration *HistogramVec
	inFlight *GaugeVec
}

// NewHTTPMetrics registers HTTPMetrics' three metrics on reg:
// "http_requests_total" (counter; labels method, route, status_class),
// "http_request_duration_seconds" (histogram; labels method, route,
// status_class), and "http_requests_in_flight" (gauge; label method).
func NewHTTPMetrics(reg *Registry) (*HTTPMetrics, error) {
	requests, err := reg.NewCounterVec(VecOpts{
		Name:   "http_requests_total",
		Help:   "Total HTTP requests, by method, route template, and status class.",
		Labels: []string{"method", "route", "status_class"},
	})
	if err != nil {
		return nil, err
	}

	duration, err := reg.NewHistogramVec(HistogramVecOpts{
		VecOpts: VecOpts{
			Name:   "http_request_duration_seconds",
			Help:   "HTTP request duration in seconds, by method, route template, and status class.",
			Labels: []string{"method", "route", "status_class"},
		},
		Buckets: defaultHTTPDurationBuckets,
	})
	if err != nil {
		return nil, err
	}

	inFlight, err := reg.NewGaugeVec(VecOpts{
		Name:   "http_requests_in_flight",
		Help:   "HTTP requests currently being served, by method.",
		Labels: []string{"method"},
	})
	if err != nil {
		return nil, err
	}

	return &HTTPMetrics{requests: requests, duration: duration, inFlight: inFlight}, nil
}

// RouteTemplateFunc extracts the low-cardinality route template matched
// for r — for example "/widgets/{id}", read from
// chi.RouteContext(r.Context()).RoutePattern() — after routing has
// resolved. [HTTPMetrics.Middleware] calls it only after the wrapped
// handler has returned, which is when a chi-style router has finished
// populating the route context. Return "" for a request the router never
// matched; Middleware then labels the request [unmatchedRoute] instead of
// the request's raw path.
//
// This package accepts a RouteTemplateFunc, rather than importing a router
// itself, to keep the framework's zero-runtime-dependency target through
// M2: the router an application uses (chi, per the golden profile) is a
// dependency of the generated application, not of runtime/observability.
type RouteTemplateFunc func(*http.Request) string

// Middleware returns net/http middleware that records m's three metrics
// for every request it wraps: the in-flight gauge for the duration of the
// call, then the request counter and duration histogram once the wrapped
// handler returns — or panics; see below. routeTemplate supplies the route
// label; see [RouteTemplateFunc]. A nil routeTemplate labels every request
// [unmatchedRoute].
//
// Middleware never labels a metric with the request's raw URL path. The
// cardinality of every series it produces is bounded by the cardinality
// cap on the underlying [CounterVec], [HistogramVec], and [GaugeVec] (see
// [DefaultMaxSeries]) regardless of what routeTemplate returns.
//
// # A panicking handler is still counted
//
// The count and duration observation are made from a deferred function, so
// a handler that panics is recorded exactly like one that returns
// normally, labeled with the fixed status class [panicStatusClass]
// ("panic") regardless of anything the handler wrote to the response
// before panicking. Middleware does not recover the panic itself — a crash
// is exactly the request class an operator most wants counted, so
// recording it and then letting the panic continue to propagate (to
// whatever recovery middleware, if any, sits further out in the chain) is
// the right default; silently dropping the observation would make the
// worst-case request the one category never visible in the metrics that
// exist to surface it.
func (m *HTTPMetrics) Middleware(routeTemplate RouteTemplateFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method := r.Method

			inFlight := m.inFlight.WithLabelValues(method)
			inFlight.Inc()
			defer inFlight.Dec()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()

			defer func() {
				elapsed := time.Since(start).Seconds()

				route := unmatchedRoute
				if routeTemplate != nil {
					if t := routeTemplate(r); t != "" {
						route = t
					}
				}

				class := statusClass(rec.status)
				p := recover()
				if p != nil {
					class = panicStatusClass
				}

				m.requests.WithLabelValues(method, route, class).Inc()
				m.duration.WithLabelValues(method, route, class).Observe(elapsed)

				if p != nil {
					panic(p) // re-panic: this middleware observes, it does not recover.
				}
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

// panicStatusClass is the status_class label [HTTPMetrics.Middleware]
// records for a handler that panicked, in place of whatever
// [statusRecorder.status] happened to hold at the moment of the panic
// (which may still be the zero-value default if the handler panicked
// before writing anything). It is a fixed string, never derived from the
// panic value itself, so it adds exactly one bounded label value alongside
// the six [statusClass] can already produce — never anything
// request-controlled.
const panicStatusClass = "panic"

// statusClass reduces an HTTP status code to its class ("2xx", "4xx", …),
// which is what request-count and duration metrics are labeled by rather
// than the exact code: a class is a fixed set of six possible values
// ([panicStatusClass] is a distinct seventh, applied only when the handler
// panics — see [HTTPMetrics.Middleware]), while individual status codes an
// application might legitimately return (404s carrying different Problem
// Details types, for example) could otherwise grow without the same bound.
func statusClass(code int) string {
	switch {
	case code >= 100 && code < 200:
		return "1xx"
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return "unknown"
	}
}

// statusRecorder wraps an [http.ResponseWriter] to capture the status code
// a handler wrote, which the standard library does not otherwise expose to
// middleware wrapping that handler. It forwards Header, Write, and
// WriteHeader directly, and exposes every other optional interface the
// wrapped [http.ResponseWriter] implements — [http.Flusher],
// [http.Hijacker], and anything else [net/http.ResponseController] knows
// how to reach — through Unwrap, rather than by hand-rolling a forwarding
// method for each one. A handler wrapped by [HTTPMetrics.Middleware] that
// wants to flush a streamed response (SSE) or hijack the connection
// (WebSocket) must go through [net/http.NewResponseController] rather than
// type-asserting w directly to [http.Flusher] or [http.Hijacker]: a type
// assertion on the wrapper itself would fail, exactly the silent
// regression [net/http.ResponseController]'s Unwrap-chain walk exists to
// avoid. See docs/metrics.md.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap returns the wrapped [http.ResponseWriter], which is what lets
// [net/http.ResponseController] reach Flush, Hijack, and the other
// optional response-writer interfaces on the writer statusRecorder wraps,
// even though statusRecorder itself only implements Header, Write, and
// WriteHeader.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
