package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteTextCounter(t *testing.T) {
	reg := NewRegistry()
	cv, err := reg.NewCounterVec(VecOpts{Name: "widgets_total", Help: "Widgets created.", Labels: []string{"kind"}})
	if err != nil {
		t.Fatal(err)
	}
	cv.WithLabelValues("blue").Add(3)

	var b strings.Builder
	if err := WriteText(&b, reg); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	want := []string{
		"# HELP widgets_total Widgets created.",
		"# TYPE widgets_total counter",
		`widgets_total{kind="blue"} 3`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q:\n%s", w, out)
		}
	}
}

func TestWriteTextGaugeNoLabels(t *testing.T) {
	reg := NewRegistry()
	gv, err := reg.NewGaugeVec(VecOpts{Name: "queue_depth", Help: "Current queue depth."})
	if err != nil {
		t.Fatal(err)
	}
	gv.WithLabelValues().Set(42)

	var b strings.Builder
	if err := WriteText(&b, reg); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, "queue_depth 42") {
		t.Errorf("output missing unlabeled gauge sample:\n%s", out)
	}
	if strings.Contains(out, "queue_depth{") {
		t.Errorf("unlabeled metric must not render an empty {} label set:\n%s", out)
	}
}

func TestWriteTextHistogramBucketsCumulativeAndSorted(t *testing.T) {
	reg := NewRegistry()
	hv, err := reg.NewHistogramVec(HistogramVecOpts{
		VecOpts: VecOpts{Name: "req_seconds", Help: "h", Labels: []string{"route"}},
		Buckets: []float64{0.1, 1, 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := hv.WithLabelValues("/x")
	h.Observe(0.05)
	h.Observe(5)

	var b strings.Builder
	if err := WriteText(&b, reg); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	// 0.05 qualifies for every bucket (le=0.1, le=1, le=10); 5 qualifies
	// only for le=10. Bucket counts are cumulative: le="1" counts every
	// observation <= 1, which is just the 0.05 one.
	for _, want := range []string{
		`req_seconds_bucket{route="/x",le="0.1"} 1`,
		`req_seconds_bucket{route="/x",le="1"} 1`,
		`req_seconds_bucket{route="/x",le="10"} 2`,
		`req_seconds_bucket{route="/x",le="+Inf"} 2`,
		`req_seconds_sum{route="/x"} 5.05`,
		`req_seconds_count{route="/x"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteTextEscapesLabelValues(t *testing.T) {
	reg := NewRegistry()
	cv, err := reg.NewCounterVec(VecOpts{Name: "c", Help: `has "quotes" and \backslash`, Labels: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	cv.WithLabelValues(`va"lue\with\backslash`).Inc()

	var b strings.Builder
	if err := WriteText(&b, reg); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	// HELP lines are plain text, not a quoted string, so a literal double
	// quote needs no escaping there — only a backslash and a newline do.
	if !strings.Contains(out, `# HELP c has "quotes" and \\backslash`) {
		t.Errorf("HELP line not escaped correctly:\n%s", out)
	}
	if !strings.Contains(out, `a="va\"lue\\with\\backslash"`) {
		t.Errorf("label value not escaped correctly:\n%s", out)
	}
}

func TestWriteTextDeterministicOrder(t *testing.T) {
	reg := NewRegistry()
	cv, err := reg.NewCounterVec(VecOpts{Name: "c", Help: "h", Labels: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	cv.WithLabelValues("zebra").Inc()
	cv.WithLabelValues("apple").Inc()
	cv.WithLabelValues("mango").Inc()

	var first, second strings.Builder
	if err := WriteText(&first, reg); err != nil {
		t.Fatal(err)
	}
	if err := WriteText(&second, reg); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("WriteText output is not deterministic across calls:\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}

	out := first.String()
	iApple := strings.Index(out, `a="apple"`)
	iMango := strings.Index(out, `a="mango"`)
	iZebra := strings.Index(out, `a="zebra"`)
	if iApple >= iMango || iMango >= iZebra {
		t.Fatalf("label values not emitted in sorted order:\n%s", out)
	}
}

func TestNewHandlerServesPrometheusContentType(t *testing.T) {
	reg := NewRegistry()
	cv, err := reg.NewCounterVec(VecOpts{Name: "c", Help: "h"})
	if err != nil {
		t.Fatal(err)
	}
	cv.WithLabelValues().Inc()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	NewHandler(reg).ServeHTTP(rr, req)

	if ct := rr.Header().Get("Content-Type"); ct != prometheusContentType {
		t.Errorf("Content-Type = %q, want %q", ct, prometheusContentType)
	}
	if !strings.Contains(rr.Body.String(), "c 1") {
		t.Errorf("body missing expected sample:\n%s", rr.Body.String())
	}
}
