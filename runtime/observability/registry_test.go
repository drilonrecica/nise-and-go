package observability

import (
	"strings"
	"testing"
)

func TestRegistryDuplicateNameRejected(t *testing.T) {
	reg := NewRegistry()

	if _, err := reg.NewCounterVec(VecOpts{Name: "widgets_total", Help: "h"}); err != nil {
		t.Fatalf("first registration: unexpected error: %v", err)
	}

	tests := []struct {
		name string
		fn   func() error
	}{
		{
			name: "counter vs counter",
			fn: func() error {
				_, err := reg.NewCounterVec(VecOpts{Name: "widgets_total", Help: "h"})
				return err
			},
		},
		{
			name: "counter vs gauge",
			fn: func() error {
				_, err := reg.NewGaugeVec(VecOpts{Name: "widgets_total", Help: "h"})
				return err
			},
		},
		{
			name: "counter vs histogram",
			fn: func() error {
				_, err := reg.NewHistogramVec(HistogramVecOpts{VecOpts: VecOpts{Name: "widgets_total", Help: "h"}})
				return err
			},
		},
		{
			name: "counter vs gauge func",
			fn: func() error {
				_, err := reg.NewGaugeFuncVec(VecOpts{Name: "widgets_total", Help: "h"})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected an error for a duplicate metric name, got nil")
			}
			if !strings.Contains(err.Error(), "widgets_total") {
				t.Errorf("error %q does not name the conflicting metric", err.Error())
			}
		})
	}
}

func TestRegistryEmptyNameRejected(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.NewCounterVec(VecOpts{Name: "", Help: "h"}); err == nil {
		t.Fatal("expected an error for an empty metric name, got nil")
	}
}

func TestCounterVecIncAndAdd(t *testing.T) {
	reg := NewRegistry()
	cv, err := reg.NewCounterVec(VecOpts{Name: "c", Help: "h", Labels: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}

	c := cv.WithLabelValues("x")
	c.Inc()
	c.Add(2.5)
	c.Add(-100) // negative delta must be ignored, not subtracted

	got := cv.table.snapshot()["x"].value()
	if got != 3.5 {
		t.Fatalf("counter value = %v, want 3.5", got)
	}
}

func TestCounterVecSameLabelsShareOneSeries(t *testing.T) {
	reg := NewRegistry()
	cv, err := reg.NewCounterVec(VecOpts{Name: "c", Help: "h", Labels: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}

	cv.WithLabelValues("x").Inc()
	cv.WithLabelValues("x").Inc()

	snap := cv.table.snapshot()
	if len(snap) != 1 {
		t.Fatalf("series count = %d, want 1", len(snap))
	}
	if got := snap["x"].value(); got != 2 {
		t.Fatalf("counter value = %v, want 2", got)
	}
}

func TestGaugeVecSetIncDecAdd(t *testing.T) {
	reg := NewRegistry()
	gv, err := reg.NewGaugeVec(VecOpts{Name: "g", Help: "h", Labels: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}

	g := gv.WithLabelValues("x")
	g.Set(10)
	g.Inc()
	g.Dec()
	g.Dec()
	g.Add(0.5)

	got := gv.table.snapshot()["x"].value()
	if got != 9.5 {
		t.Fatalf("gauge value = %v, want 9.5", got)
	}
}

func TestGaugeFuncVecSamplesOnRead(t *testing.T) {
	reg := NewRegistry()
	gv, err := reg.NewGaugeFuncVec(VecOpts{Name: "g", Help: "h", Labels: []string{"pool"}})
	if err != nil {
		t.Fatal(err)
	}

	n := 3
	if err := gv.Add(func() float64 { return float64(n) }, "primary"); err != nil {
		t.Fatal(err)
	}

	first := gv.table.snapshot()["primary"].value()
	n = 7
	second := gv.table.snapshot()["primary"].value()

	if first != 3 || second != 7 {
		t.Fatalf("gauge func values = (%v, %v), want (3, 7): value must be sampled live, not cached", first, second)
	}
}

func TestGaugeFuncVecDuplicateLabelValuesRejected(t *testing.T) {
	reg := NewRegistry()
	gv, err := reg.NewGaugeFuncVec(VecOpts{Name: "g", Help: "h", Labels: []string{"pool"}})
	if err != nil {
		t.Fatal(err)
	}

	if err := gv.Add(func() float64 { return 1 }, "primary"); err != nil {
		t.Fatal(err)
	}
	if err := gv.Add(func() float64 { return 2 }, "primary"); err == nil {
		t.Fatal("expected an error registering the same label values twice, got nil")
	}
}

func TestHistogramVecObserve(t *testing.T) {
	reg := NewRegistry()
	hv, err := reg.NewHistogramVec(HistogramVecOpts{
		VecOpts: VecOpts{Name: "h", Help: "h", Labels: []string{"a"}},
		Buckets: []float64{1, 5, 10},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := hv.WithLabelValues("x")
	h.Observe(0.5)
	h.Observe(3)
	h.Observe(20)

	v := hv.table.snapshot()["x"]
	wantBuckets := []uint64{1, 2, 2} // <=1: 1 obs; <=5: 2 obs; <=10: 2 obs
	for i, want := range wantBuckets {
		if got := v.bucketCounts[i].Load(); got != want {
			t.Errorf("bucket[%d] = %d, want %d", i, got, want)
		}
	}
	if got := v.count.Load(); got != 3 {
		t.Errorf("count = %d, want 3", got)
	}
	if got := v.sum.load(); got != 23.5 {
		t.Errorf("sum = %v, want 23.5", got)
	}
}

func TestWithLabelValuesCountMismatchDoesNotPanic(t *testing.T) {
	reg := NewRegistry()
	cv, err := reg.NewCounterVec(VecOpts{Name: "c", Help: "h", Labels: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("WithLabelValues panicked on a label-count mismatch: %v", r)
		}
	}()

	cv.WithLabelValues("only-one").Inc()
	cv.WithLabelValues().Inc()
	cv.WithLabelValues("a", "b", "c", "extra").Inc()
}
