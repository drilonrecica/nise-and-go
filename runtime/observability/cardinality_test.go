package observability

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// randomUUID returns a random RFC 4122 v4 UUID string built from the
// standard library alone: runtime/ targets zero runtime dependencies, so
// this test does not import a UUID package to manufacture the
// unbounded-cardinality input its name describes.
func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// TestCounterVecCardinalityCapHolds is the cardinality-cap test the task
// brief calls for: a Vec created with a small MaxSeries never grows past
// MaxSeries individually tracked series plus the one shared overflow
// series, no matter how many distinct label-value combinations a caller
// throws at it.
func TestCounterVecCardinalityCapHolds(t *testing.T) {
	reg := NewRegistry()
	const maxSeries = 5
	cv, err := reg.NewCounterVec(VecOpts{
		Name:      "attempts_total",
		Help:      "h",
		Labels:    []string{"id"},
		MaxSeries: maxSeries,
	})
	if err != nil {
		t.Fatal(err)
	}

	const distinctValues = 1000
	for i := 0; i < distinctValues; i++ {
		cv.WithLabelValues(fmt.Sprintf("id-%d", i)).Inc()
	}

	snap := cv.table.snapshot()
	if len(snap) > maxSeries+1 {
		t.Fatalf("series count = %d, want at most %d (cap %d + overflow)", len(snap), maxSeries+1, maxSeries)
	}

	overflow, ok := snap[overflowLabelValue]
	if !ok {
		t.Fatal("expected an overflow series once the cap was exceeded, found none")
	}
	// distinctValues - maxSeries requests landed in the overflow bucket.
	if got, want := overflow.value(), float64(distinctValues-maxSeries); got != want {
		t.Errorf("overflow series count = %v, want %v", got, want)
	}
}

// TestHTTPMetricsRawUUIDPathDoesNotCreateNewSeries is the raw-path test
// the task brief calls for. It exercises the failure mode the emphasis
// section describes directly: an application's RouteTemplateFunc that
// (correctly, and separately, incorrectly) hands back the request's raw
// path — which, for a resource collection, differs on every request
// because it embeds a UUID.
//
// The "correct" half of this test shows the intended design working: a
// RouteTemplateFunc that resolves the UUID path to a fixed template
// produces exactly one series for any number of distinct IDs. The
// "incorrect" half shows the safety net behind it: even a
// RouteTemplateFunc bugged into echoing the raw path back is still bounded
// by the underlying CounterVec's cardinality cap, so a single crafted
// client cannot exhaust memory by generating fresh UUIDs.
func TestHTTPMetricsRawUUIDPathDoesNotCreateNewSeries(t *testing.T) {
	t.Run("route template collapses distinct UUID paths to one series", func(t *testing.T) {
		reg := NewRegistry()
		m, err := NewHTTPMetrics(reg)
		if err != nil {
			t.Fatal(err)
		}

		template := func(r *http.Request) string { return "/widgets/{id}" }
		handler := m.Middleware(template)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for i := 0; i < 5; i++ {
			id := randomUUID()
			req := httptest.NewRequest(http.MethodGet, "/widgets/"+id, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
		}

		snap := m.requests.table.snapshot()
		if len(snap) != 1 {
			t.Fatalf("series count = %d, want 1: distinct UUID paths must not create distinct series when labeled by route template", len(snap))
		}
		for _, v := range snap {
			if got := v.value(); got != 5 {
				t.Fatalf("request count = %v, want 5", got)
			}
		}
	})

	t.Run("even a RouteTemplateFunc that echoes the raw path stays bounded", func(t *testing.T) {
		reg := NewRegistry()
		m, err := NewHTTPMetrics(reg)
		if err != nil {
			t.Fatal(err)
		}

		// Deliberately wrong: returns the raw, attacker-influenced path
		// instead of a route template.
		rawPath := func(r *http.Request) string { return r.URL.Path }
		handler := m.Middleware(rawPath)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		const requests = 2000
		for i := 0; i < requests; i++ {
			id := randomUUID()
			req := httptest.NewRequest(http.MethodGet, "/widgets/"+id, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
		}

		snap := m.requests.table.snapshot()
		if len(snap) > DefaultMaxSeries+1 {
			t.Fatalf("series count = %d, want at most %d: a raw path with a UUID must not create unbounded series", len(snap), DefaultMaxSeries+1)
		}
	})
}
