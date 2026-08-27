package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

func alwaysHealthy(context.Context) error { return nil }

func TestProber_NoChecks(t *testing.T) {
	p := NewProber(nil)
	report := p.Probe(context.Background())
	if !report.Healthy {
		t.Error("Report.Healthy = false with no registered checks, want true")
	}
	if len(report.Results) != 0 {
		t.Errorf("Report.Results = %v, want empty", report.Results)
	}
}

func TestProber_AllHealthy(t *testing.T) {
	p := NewProber([]Check{
		{Name: "db", Func: alwaysHealthy},
		{Name: "cache", Func: alwaysHealthy},
	})
	report := p.Probe(context.Background())
	if !report.Healthy {
		t.Fatalf("Report.Healthy = false, want true: %+v", report)
	}
	if len(report.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(report.Results))
	}
	for _, r := range report.Results {
		if !r.Healthy || r.Error != "" {
			t.Errorf("Result %+v, want Healthy=true and empty Error", r)
		}
	}
}

func TestProber_OneUnhealthy(t *testing.T) {
	wantErr := errors.New("connection refused at 10.0.1.5:5432")
	p := NewProber([]Check{
		{Name: "db", Func: func(context.Context) error { return wantErr }},
		{Name: "cache", Func: alwaysHealthy},
	})
	report := p.Probe(context.Background())
	if report.Healthy {
		t.Fatal("Report.Healthy = true, want false")
	}

	var got Result
	for _, r := range report.Results {
		if r.Name == "db" {
			got = r
		}
	}
	if got.Healthy {
		t.Error("db Result.Healthy = true, want false")
	}
	if got.Error != wantErr.Error() {
		t.Errorf("db Result.Error = %q, want %q", got.Error, wantErr.Error())
	}
}

// TestProber_HungCheckRespectsPerCheckTimeout proves that a single check
// which ignores ctx and blocks forever is bounded by the per-check timeout,
// not by the (much larger) overall timeout.
func TestProber_HungCheckRespectsPerCheckTimeout(t *testing.T) {
	hang := func(context.Context) error {
		select {} // never returns; ignores ctx entirely.
	}
	p := NewProber(
		[]Check{{Name: "stuck", Func: hang}},
		WithCheckTimeout(20*time.Millisecond),
		WithOverallTimeout(2*time.Second),
	)

	start := time.Now()
	report := p.Probe(context.Background())
	elapsed := time.Since(start)

	if report.Healthy {
		t.Fatal("Report.Healthy = true for a hung check, want false")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Probe took %s, want close to the 20ms per-check timeout, well under the 2s overall timeout", elapsed)
	}
}

// TestProber_OverallDeadlineBound proves the overall deadline holds even
// when every registered check's own per-check timeout, on its own, would
// exceed it — the requirement most likely to be implemented backwards.
func TestProber_OverallDeadlineBound(t *testing.T) {
	hang := func(context.Context) error {
		select {}
	}
	p := NewProber(
		[]Check{
			{Name: "stuck-1", Func: hang},
			{Name: "stuck-2", Func: hang},
			{Name: "stuck-3", Func: hang},
		},
		WithCheckTimeout(10*time.Second),
		WithOverallTimeout(50*time.Millisecond),
	)

	start := time.Now()
	report := p.Probe(context.Background())
	elapsed := time.Since(start)

	if report.Healthy {
		t.Fatal("Report.Healthy = true, want false")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Probe took %s, want close to the 50ms overall timeout despite a 10s per-check timeout", elapsed)
	}
	for _, r := range report.Results {
		if r.Healthy {
			t.Errorf("Result %+v reported healthy despite the overall deadline elapsing first", r)
		}
	}
}

// TestProber_ConcurrentProbes proves Probe itself is safe to call
// concurrently from many goroutines (in addition to the handler-level
// concurrency test in handler_test.go, which also exercises Gate state
// changes concurrently with probing).
func TestProber_ConcurrentProbes(t *testing.T) {
	p := NewProber([]Check{{Name: "db", Func: alwaysHealthy}})
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			p.Probe(context.Background())
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}
