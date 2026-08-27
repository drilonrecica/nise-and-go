package observability

import (
	"fmt"
	"testing"
	"time"
)

// fakeJobRunner is a hand-written stand-in for the River-backed job runner
// M8 will eventually build; it exists only to exercise the JobMetrics
// seam, never to fabricate the job runner package itself.
type fakeJobRunner struct {
	metrics *JobMetrics
}

func (r *fakeJobRunner) run(queue, kind string, fail bool) {
	start := time.Now()
	outcome := JobOutcomeSuccess
	if fail {
		outcome = JobOutcomeFailure
	}
	r.metrics.Observe(queue, kind, outcome, time.Since(start))
}

func TestJobMetricsSeamRecordsOutcomes(t *testing.T) {
	reg := NewRegistry()
	jm, err := NewJobMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeJobRunner{metrics: jm}

	runner.run("default", "send_email", false)
	runner.run("default", "send_email", false)
	runner.run("default", "send_email", true)

	success := jm.completed.WithLabelValues("default", "send_email", string(JobOutcomeSuccess))
	if got := success.(*counterValue).value(); got != 2 {
		t.Fatalf("success count = %v, want 2", got)
	}

	failure := jm.completed.WithLabelValues("default", "send_email", string(JobOutcomeFailure))
	if got := failure.(*counterValue).value(); got != 1 {
		t.Fatalf("failure count = %v, want 1", got)
	}

	dur := jm.duration.WithLabelValues("default", "send_email")
	if got := dur.(*histogramValue).count.Load(); got != 3 {
		t.Fatalf("duration observation count = %d, want 3", got)
	}
}

// TestJobMetricsSeamCardinalityCapHolds proves the same safety net applies
// to the job seam as to HTTP metrics: a caller that mislabels "kind" with
// something unbounded (here, a per-job ID rather than a registered job
// type) is still bounded by the underlying CounterVec's cap.
func TestJobMetricsSeamCardinalityCapHolds(t *testing.T) {
	reg := NewRegistry()
	jm, err := NewJobMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeJobRunner{metrics: jm}

	for i := 0; i < 1000; i++ {
		runner.run("default", fmt.Sprintf("job-%d", i), false)
	}

	snap := jm.completed.table.snapshot()
	if len(snap) > DefaultMaxSeries+1 {
		t.Fatalf("series count = %d, want at most %d", len(snap), DefaultMaxSeries+1)
	}
}
