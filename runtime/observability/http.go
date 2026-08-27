package observability

import (
	"net/http"
	"time"
)

// DefaultHTTPDurationBuckets are the histogram bucket boundaries, in
// seconds, [NewHTTPMetrics] uses for its request-duration histogram. They
// are the Prometheus client libraries' own conventional default buckets,
// spanning 5ms to 10s.
var DefaultHTTPDurationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

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
		Buckets: DefaultHTTPDurationBuckets,
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
// handler returns. routeTemplate supplies the route label; see
// [RouteTemplateFunc]. A nil routeTemplate labels every request
// [unmatchedRoute].
//
// Middleware never labels a metric with the request's raw URL path. The
// cardinality of every series it produces is bounded by the cardinality
// cap on the underlying [CounterVec], [HistogramVec], and [GaugeVec] (see
// [DefaultMaxSeries]) regardless of what routeTemplate returns.
func (m *HTTPMetrics) Middleware(routeTemplate RouteTemplateFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method := r.Method

			inFlight := m.inFlight.WithLabelValues(method)
			inFlight.Inc()
			defer inFlight.Dec()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			elapsed := time.Since(start).Seconds()

			route := unmatchedRoute
			if routeTemplate != nil {
				if t := routeTemplate(r); t != "" {
					route = t
				}
			}
			class := statusClass(rec.status)

			m.requests.WithLabelValues(method, route, class).Inc()
			m.duration.WithLabelValues(method, route, class).Observe(elapsed)
		})
	}
}

// statusClass reduces an HTTP status code to its class ("2xx", "4xx", …),
// which is what request-count and duration metrics are labeled by rather
// than the exact code: a class is a fixed set of six possible values,
// while individual status codes an application might legitimately return
// (404s carrying different Problem Details types, for example) could
// otherwise grow without the same bound.
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
// WriteHeader only; a handler that type-asserts its [http.ResponseWriter]
// to [http.Flusher] or [http.Hijacker] (streaming responses, WebSocket
// upgrades) will not find that optional interface satisfied through this
// wrapper. Excluding those routes from Middleware, or extending this type,
// is the application's call — out of scope for the essential metrics this
// package implements.
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
