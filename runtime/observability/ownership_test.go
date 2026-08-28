package observability

import (
	"strings"
	"testing"
)

// TestHistogramBucketsAreCopiedFromOpts is the regression test for the
// aliasing defect: HistogramVecOpts.Buckets was stored directly on the
// HistogramVec and shared with every series it created, so a caller that
// kept its own reference could rewrite the exported bucket bounds of a live
// histogram while the recorded counts stayed where they were — a
// non-monotonic, malformed Prometheus histogram, written unsynchronized
// from whatever goroutine the caller happened to be on.
func TestHistogramBucketsAreCopiedFromOpts(t *testing.T) {
	reg := NewRegistry()
	buckets := []float64{1, 2, 3}

	hv, err := reg.NewHistogramVec(HistogramVecOpts{
		VecOpts: VecOpts{Name: "widget_seconds", Help: "h"},
		Buckets: buckets,
	})
	if err != nil {
		t.Fatalf("NewHistogramVec: %v", err)
	}
	hv.WithLabelValues().Observe(1.5)

	before := expositionText(t, reg)

	// The caller mutates the slice it still holds. Nothing exposed may move.
	buckets[0] = 1000
	buckets[1] = 2000
	buckets[2] = 3000

	if after := expositionText(t, reg); after != before {
		t.Errorf("mutating the caller's Buckets slice changed the exposition output.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !strings.Contains(before, `le="1"`) {
		t.Fatalf("expected the original bucket bounds in the exposition output, got:\n%s", before)
	}
}

// TestVecLabelsAreCopiedFromOpts covers the same aliasing defect on
// VecOpts.Labels, for every Vec kind that accepts them. A Registry reads
// labelNames on every scrape for the life of the process.
func TestVecLabelsAreCopiedFromOpts(t *testing.T) {
	tests := []struct {
		name  string
		build func(reg *Registry, labels []string) error
	}{
		{
			name: "counter",
			build: func(reg *Registry, labels []string) error {
				cv, err := reg.NewCounterVec(VecOpts{Name: "m_total", Help: "h", Labels: labels})
				if err == nil {
					cv.WithLabelValues("v").Inc()
				}
				return err
			},
		},
		{
			name: "gauge",
			build: func(reg *Registry, labels []string) error {
				gv, err := reg.NewGaugeVec(VecOpts{Name: "m_total", Help: "h", Labels: labels})
				if err == nil {
					gv.WithLabelValues("v").Set(1)
				}
				return err
			},
		},
		{
			name: "histogram",
			build: func(reg *Registry, labels []string) error {
				hv, err := reg.NewHistogramVec(HistogramVecOpts{
					VecOpts: VecOpts{Name: "m_total", Help: "h", Labels: labels},
					Buckets: []float64{1},
				})
				if err == nil {
					hv.WithLabelValues("v").Observe(0.5)
				}
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			labels := []string{"route"}
			if err := tc.build(reg, labels); err != nil {
				t.Fatalf("construct: %v", err)
			}

			before := expositionText(t, reg)
			labels[0] = "tampered"

			if after := expositionText(t, reg); after != before {
				t.Errorf("mutating the caller's Labels slice changed the exposition output.\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if !strings.Contains(before, "route=") {
				t.Fatalf("expected the original label name in the exposition output, got:\n%s", before)
			}
		})
	}
}

// TestDefaultBucketsReturnCopies asserts the package holds no exported
// mutable state. Both accessors used to be package-level exported slices,
// so assigning one element corrupted the defaults of every histogram
// constructed afterwards, process-wide — the exact thing runtime/doc.go and
// ADR 0011 promise runtime does not do.
func TestDefaultBucketsReturnCopies(t *testing.T) {
	tests := []struct {
		name string
		get  func() []float64
	}{
		{name: "http", get: DefaultHTTPDurationBuckets},
		{name: "job", get: DefaultJobDurationBuckets},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first := tc.get()
			if len(first) == 0 {
				t.Fatal("expected non-empty default buckets")
			}
			original := first[0]

			first[0] = 999

			second := tc.get()
			if second[0] != original {
				t.Errorf("mutating a returned slice changed the package default: got %v, want %v", second[0], original)
			}
			if &first[0] == &second[0] {
				t.Error("two calls returned the same backing array; each must be a fresh copy")
			}
		})
	}
}

// TestZeroRegistryPanicsAtConstruction covers the third of the four
// zero-value defects this fix wave closed. observability.Registry{} used to
// fail with a bare "assignment to entry in nil map" from inside this
// package, at whatever point the application first registered a metric.
func TestZeroRegistryPanicsAtConstruction(t *testing.T) {
	tests := []struct {
		name string
		call func(reg *Registry)
	}{
		{name: "counter", call: func(reg *Registry) { _, _ = reg.NewCounterVec(VecOpts{Name: "m", Help: "h"}) }},
		{name: "gauge", call: func(reg *Registry) { _, _ = reg.NewGaugeVec(VecOpts{Name: "m", Help: "h"}) }},
		{name: "histogram", call: func(reg *Registry) {
			_, _ = reg.NewHistogramVec(HistogramVecOpts{VecOpts: VecOpts{Name: "m", Help: "h"}})
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				p := recover()
				if p == nil {
					t.Fatal("expected a panic from a zero-valued Registry")
				}
				msg, ok := p.(string)
				if !ok {
					t.Fatalf("panic value is %T, want a diagnostic string: %v", p, p)
				}
				for _, want := range []string{"runtime/observability", "NewRegistry"} {
					if !strings.Contains(msg, want) {
						t.Errorf("panic message %q does not mention %q", msg, want)
					}
				}
			}()
			tc.call(&Registry{})
		})
	}
}

// expositionText renders reg through the package's own exposition writer.
func expositionText(t *testing.T, reg *Registry) string {
	t.Helper()
	var b strings.Builder
	if err := WriteText(&b, reg); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	return b.String()
}
