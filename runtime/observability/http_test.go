package observability

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestStatusClass(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{100, "1xx"},
		{200, "2xx"},
		{201, "2xx"},
		{301, "3xx"},
		{404, "4xx"},
		{500, "5xx"},
		{599, "5xx"},
		{600, "unknown"},
		{0, "unknown"},
	}
	for _, tt := range tests {
		if got := statusClass(tt.code); got != tt.want {
			t.Errorf("statusClass(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestHTTPMetricsMiddlewareRecordsCountAndDuration(t *testing.T) {
	reg := NewRegistry()
	m, err := NewHTTPMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}

	template := func(r *http.Request) string { return "/widgets/{id}" }
	handler := m.Middleware(template)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/widgets/1", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	c := m.requests.WithLabelValues(http.MethodPost, "/widgets/{id}", "2xx")
	if got := c.(*counterValue).value(); got != 1 {
		t.Fatalf("request count = %v, want 1", got)
	}

	h := m.duration.WithLabelValues(http.MethodPost, "/widgets/{id}", "2xx")
	hv := h.(*histogramValue)
	if hv.count.Load() != 1 {
		t.Fatalf("duration observation count = %d, want 1", hv.count.Load())
	}
}

func TestHTTPMetricsMiddlewareUnmatchedRoute(t *testing.T) {
	reg := NewRegistry()
	m, err := NewHTTPMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}

	// A nil RouteTemplateFunc, and one that always reports no match, must
	// both label the request "unmatched" rather than the raw path.
	for _, tmpl := range []RouteTemplateFunc{nil, func(*http.Request) string { return "" }} {
		handler := m.Middleware(tmpl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		req := httptest.NewRequest(http.MethodGet, "/does/not/exist", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}

	c := m.requests.WithLabelValues(http.MethodGet, unmatchedRoute, "4xx")
	if got := c.(*counterValue).value(); got != 2 {
		t.Fatalf("unmatched-route request count = %v, want 2", got)
	}
}

func TestHTTPMetricsMiddlewareInFlightTracksConcurrency(t *testing.T) {
	reg := NewRegistry()
	m, err := NewHTTPMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	started := make(chan struct{})
	handler := m.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/slow", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}()

	<-started
	inFlight := m.inFlight.WithLabelValues(http.MethodGet)
	if got := inFlight.(*gaugeValue).value(); got != 1 {
		t.Fatalf("in-flight gauge during request = %v, want 1", got)
	}

	close(release)
	wg.Wait()

	if got := inFlight.(*gaugeValue).value(); got != 0 {
		t.Fatalf("in-flight gauge after request = %v, want 0", got)
	}
}

func TestHTTPMetricsMiddlewareInFlightDecrementsOnPanic(t *testing.T) {
	reg := NewRegistry()
	m, err := NewHTTPMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}

	handler := m.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panics", nil)
	rr := httptest.NewRecorder()

	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(rr, req)
	}()

	inFlight := m.inFlight.WithLabelValues(http.MethodGet)
	if got := inFlight.(*gaugeValue).value(); got != 0 {
		t.Fatalf("in-flight gauge after a panicking handler = %v, want 0 (deferred Dec must still run)", got)
	}
}

// TestHTTPMetricsMiddlewarePanicIsCountedAndRePanics is the fix-round-1
// test for finding 3 (task-9-report.md): a panicking handler must still be
// counted, labeled with the fixed panicStatusClass, and the panic must
// continue to propagate unchanged rather than being swallowed by
// Middleware.
func TestHTTPMetricsMiddlewarePanicIsCountedAndRePanics(t *testing.T) {
	reg := NewRegistry()
	m, err := NewHTTPMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}

	template := func(r *http.Request) string { return "/panics" }
	handler := m.Middleware(template)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panics", nil)
	rr := httptest.NewRecorder()

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		handler.ServeHTTP(rr, req)
	}()

	if recovered != "boom" {
		t.Fatalf("recovered panic value = %v, want %q: Middleware must re-panic with the original value, not swallow or replace it", recovered, "boom")
	}

	c := m.requests.WithLabelValues(http.MethodGet, "/panics", panicStatusClass)
	if got := c.(*counterValue).value(); got != 1 {
		t.Fatalf("request count for a panicking handler = %v, want 1: a panic must still be counted", got)
	}

	h := m.duration.WithLabelValues(http.MethodGet, "/panics", panicStatusClass)
	if got := h.(*histogramValue).count.Load(); got != 1 {
		t.Fatalf("duration observation count for a panicking handler = %d, want 1", got)
	}

	// The panic must never surface as a normal statusClass value (the
	// handler wrote nothing, so rec.status would default to 200/"2xx"
	// absent the fix).
	normal := m.requests.WithLabelValues(http.MethodGet, "/panics", "2xx")
	if got := normal.(*counterValue).value(); got != 0 {
		t.Fatalf("2xx request count after a panic = %v, want 0: a panic must not be mislabeled as a normal response", got)
	}
}

// TestHTTPMetricsMiddlewareFlushesThroughResponseController is the
// fix-round-1 test for finding 2 (task-9-report.md): a handler wrapped by
// Middleware must still be able to reach the underlying ResponseWriter's
// http.Flusher through net/http.ResponseController, via statusRecorder's
// Unwrap method.
func TestHTTPMetricsMiddlewareFlushesThroughResponseController(t *testing.T) {
	reg := NewRegistry()
	m, err := NewHTTPMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}

	var flushErr error
	handler := m.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("chunk"))
		flushErr = http.NewResponseController(w).Flush()
	}))

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	rr := httptest.NewRecorder() // httptest.ResponseRecorder implements http.Flusher.
	handler.ServeHTTP(rr, req)

	if flushErr != nil {
		t.Fatalf("ResponseController.Flush() through statusRecorder = %v, want nil: Unwrap must expose the underlying http.Flusher", flushErr)
	}
	if !rr.Flushed {
		t.Fatal("underlying ResponseRecorder was never flushed: statusRecorder.Unwrap did not reach it")
	}
}
