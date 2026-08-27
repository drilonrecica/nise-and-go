package health

import (
	"context"
	"time"
)

// DefaultCheckTimeout is the per-check timeout [NewProber] applies when no
// [WithCheckTimeout] option is given. It bounds a single dependency check —
// for example one round trip to a database or cache — generously enough for
// a healthy dependency under load, but short enough that one slow dependency
// cannot consume the whole overall deadline by itself.
const DefaultCheckTimeout = 2 * time.Second

// DefaultOverallTimeout is the deadline across every registered check
// together that [NewProber] applies when no [WithOverallTimeout] option is
// given. It is the hard upper bound on how long a caller of [Prober.Probe]
// can be made to wait, regardless of how many checks are registered or
// whether any of them ignores its context and never returns.
const DefaultOverallTimeout = 5 * time.Second

// CheckFunc probes a single dependency's health. It should return promptly
// once ctx is done; a CheckFunc that ignores ctx and blocks forever is still
// bounded by [Prober]'s per-check and overall timeouts, but leaks the
// goroutine running it for the life of the process — see the package doc
// comment.
type CheckFunc func(ctx context.Context) error

// Check names a single registered dependency check. Name identifies the
// check in a [Result]; it is never returned by an unauthenticated probe
// handler, only by [NewReadinessReportHandler]'s detailed view.
type Check struct {
	Name string
	Func CheckFunc
}

// Result is one check's outcome within a [Report]. It carries Error's exact
// text and is therefore only ever exposed through [NewReadinessReportHandler],
// never through an unauthenticated probe response.
type Result struct {
	// Name is the Check's name.
	Name string

	// Healthy is true if the check succeeded within its timeout.
	Healthy bool

	// Error is the check's error text, or "check timed out" if it did not
	// return within its timeout. It is empty when Healthy is true.
	Error string

	// Duration is how long the check took to return, or the check timeout
	// if it did not return in time.
	Duration time.Duration
}

// Report aggregates every registered [Check]'s [Result] from one call to
// [Prober.Probe].
type Report struct {
	// Healthy is true only if every check reported Healthy.
	Healthy bool

	// Results holds one Result per registered Check, in registration order.
	Results []Result
}

// Option configures a [Prober] built by [NewProber].
type Option func(*proberConfig)

type proberConfig struct {
	checkTimeout   time.Duration
	overallTimeout time.Duration
}

// WithCheckTimeout overrides [DefaultCheckTimeout].
func WithCheckTimeout(d time.Duration) Option {
	return func(c *proberConfig) { c.checkTimeout = d }
}

// WithOverallTimeout overrides [DefaultOverallTimeout].
func WithOverallTimeout(d time.Duration) Option {
	return func(c *proberConfig) { c.overallTimeout = d }
}

// Prober aggregates a fixed, explicitly registered set of [Check] values
// into a single readiness [Report], bounding the whole aggregation by an
// overall deadline and each individual check by its own timeout. A Prober is
// safe for concurrent use: [Prober.Probe] may be called concurrently from
// any number of goroutines.
type Prober struct {
	checks         []Check
	checkTimeout   time.Duration
	overallTimeout time.Duration
}

// NewProber builds a Prober from checks. checks is copied; registering a
// check happens only here, at construction, never afterward.
func NewProber(checks []Check, opts ...Option) *Prober {
	cfg := proberConfig{
		checkTimeout:   DefaultCheckTimeout,
		overallTimeout: DefaultOverallTimeout,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	owned := make([]Check, len(checks))
	copy(owned, checks)

	return &Prober{
		checks:         owned,
		checkTimeout:   cfg.checkTimeout,
		overallTimeout: cfg.overallTimeout,
	}
}

type checkOutcome struct {
	index  int
	result Result
}

// Probe runs every registered check concurrently and returns their combined
// Report. It always returns within the Prober's overall timeout: a check
// that has not reported by then is recorded as unhealthy with a "check timed
// out" error, even if the check itself never returns at all.
func (p *Prober) Probe(ctx context.Context) Report {
	if len(p.checks) == 0 {
		return Report{Healthy: true}
	}

	outcomes := make(chan checkOutcome, len(p.checks))
	for i, c := range p.checks {
		go func(i int, c Check) {
			outcomes <- checkOutcome{index: i, result: p.runCheck(ctx, c)}
		}(i, c)
	}

	results := make([]Result, len(p.checks))
	filled := make([]bool, len(p.checks))

	timer := time.NewTimer(p.overallTimeout)
	defer timer.Stop()

	remaining := len(p.checks)
	for remaining > 0 {
		select {
		case o := <-outcomes:
			results[o.index] = o.result
			filled[o.index] = true
			remaining--
		case <-timer.C:
			remaining = 0
		}
	}

	healthy := true
	for i, c := range p.checks {
		if !filled[i] {
			results[i] = Result{Name: c.Name, Healthy: false, Error: "check timed out", Duration: p.overallTimeout}
		}
		if !results[i].Healthy {
			healthy = false
		}
	}

	return Report{Healthy: healthy, Results: results}
}

// runCheck runs one Check bounded by the Prober's per-check timeout. It
// always returns within that timeout, even if c.Func ignores its context and
// never returns; in that case the goroutine running c.Func leaks for the
// life of the process, which is why a CheckFunc should always respect ctx.
func (p *Prober) runCheck(ctx context.Context, c Check) Result {
	start := time.Now()
	checkCtx, cancel := context.WithTimeout(ctx, p.checkTimeout)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Func(checkCtx)
	}()

	select {
	case err := <-errCh:
		d := time.Since(start)
		if err != nil {
			return Result{Name: c.Name, Healthy: false, Error: err.Error(), Duration: d}
		}
		return Result{Name: c.Name, Healthy: true, Duration: d}
	case <-checkCtx.Done():
		return Result{Name: c.Name, Healthy: false, Error: "check timed out", Duration: p.checkTimeout}
	}
}
