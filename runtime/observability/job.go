package observability

import "time"

// JobOutcome is the closed set of terminal states a background job can end
// in. The background-job runner planned for milestone M8 (River through
// pgx) does not exist in this repository yet;
// JobOutcome and [JobMetrics] are the seam that future runner is expected
// to report through.
type JobOutcome string

const (
	// JobOutcomeSuccess is a job that completed without error.
	JobOutcomeSuccess JobOutcome = "success"
	// JobOutcomeFailure is a job that errored and will be retried.
	JobOutcomeFailure JobOutcome = "failure"
	// JobOutcomeDiscarded is a job that errored and exhausted its
	// retries, or was otherwise permanently abandoned.
	JobOutcomeDiscarded JobOutcome = "discarded"
)

// defaultJobDurationBuckets backs [DefaultJobDurationBuckets]. See
// defaultHTTPDurationBuckets for why it is unexported and copied out.
var defaultJobDurationBuckets = []float64{.1, .5, 1, 5, 15, 30, 60, 120, 300, 600}

// DefaultJobDurationBuckets returns the histogram bucket boundaries, in
// seconds, [NewJobMetrics] uses for its job-duration histogram. Background
// jobs commonly run far longer than an HTTP request, so the top bucket
// reaches minutes rather than [DefaultHTTPDurationBuckets]'s ten seconds.
//
// The returned slice is a fresh copy; mutating it has no effect on the
// package default.
func DefaultJobDurationBuckets() []float64 {
	return copyFloats(defaultJobDurationBuckets)
}

// JobMetrics holds the essential background-job metrics: completed jobs by
// queue, kind, and outcome, and a duration histogram by queue and kind. No
// job runner exists yet in this repository (M8); this package does not
// fabricate one to have something to measure. JobMetrics is the documented
// seam a future job runner reports through — construct it once with
// [NewJobMetrics] and call [JobMetrics.Observe] each time a job finishes.
// job_test.go exercises this seam with a hand-written fake job runner.
type JobMetrics struct {
	completed *CounterVec
	duration  *HistogramVec
}

// NewJobMetrics registers JobMetrics' two metrics on reg:
// "jobs_completed_total" (counter; labels queue, kind, outcome) and
// "job_duration_seconds" (histogram; labels queue, kind).
func NewJobMetrics(reg *Registry) (*JobMetrics, error) {
	completed, err := reg.NewCounterVec(VecOpts{
		Name:   "jobs_completed_total",
		Help:   "Total background jobs completed, by queue, kind, and outcome.",
		Labels: []string{"queue", "kind", "outcome"},
	})
	if err != nil {
		return nil, err
	}

	duration, err := reg.NewHistogramVec(HistogramVecOpts{
		VecOpts: VecOpts{
			Name:   "job_duration_seconds",
			Help:   "Background job execution duration in seconds, by queue and kind.",
			Labels: []string{"queue", "kind"},
		},
		Buckets: defaultJobDurationBuckets,
	})
	if err != nil {
		return nil, err
	}

	return &JobMetrics{completed: completed, duration: duration}, nil
}

// Observe records one job's terminal outcome: its queue and kind, how it
// ended, and how long it ran. Label queue and kind by the job system's own
// low-cardinality identifiers — a queue name, a registered job type — never
// by a per-job ID, argument value, or anything else that varies per
// enqueue: the cardinality cap on the underlying counter and histogram
// bounds the damage if that rule is broken, but Observe cannot enforce
// which identifiers a caller passes.
func (m *JobMetrics) Observe(queue, kind string, outcome JobOutcome, d time.Duration) {
	m.completed.WithLabelValues(queue, kind, string(outcome)).Inc()
	m.duration.WithLabelValues(queue, kind).Observe(d.Seconds())
}
