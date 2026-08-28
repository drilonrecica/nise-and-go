package observability

import (
	"fmt"
	"sync"
)

// Registry collects named, labeled metrics independent of how they are
// exposed. Construct one with [NewRegistry], build metrics on it once at
// startup with [Registry.NewCounterVec], [Registry.NewGaugeVec], and
// [Registry.NewHistogramVec], and pass it to [NewHandler] (or your own
// exposition code, via [WriteText]) to render what has been recorded. A
// Registry is safe for concurrent use.
//
// A pull-sampled gauge kind also exists internally (see gaugeFuncVec in
// gauge.go), used by [PoolMetrics]; it is deliberately not exported here —
// see that type's doc comment for why.
type Registry struct {
	mu         sync.Mutex
	names      map[string]struct{}
	counters   []*CounterVec
	gauges     []*GaugeVec
	gaugeFuncs []*gaugeFuncVec
	histograms []*HistogramVec
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{names: make(map[string]struct{})}
}

// reserveName claims name for the Registry, or reports an error if some
// earlier call already claimed it. Metric names share one namespace across
// every Vec kind: a counter and a gauge cannot share a name, matching the
// exposition format's own constraint that one name has exactly one type.
func (r *Registry) reserveName(name string) error {
	// A Registry built as observability.Registry{} — through a composite
	// literal or an uninitialized struct field — has a nil names map, and
	// every constructor below would otherwise fail on it with a bare
	// "assignment to entry in nil map" from inside this package. That is a
	// wiring mistake, not a runtime condition, so it panics at construction
	// naming what to do instead of returning an error the caller would have
	// to distinguish from a genuine duplicate-name error. Lazily allocating
	// the map here would hide the mistake: a Registry nobody constructed is
	// also a Registry nobody passed to NewHandler, so its metrics would be
	// collected and never exposed.
	if r.names == nil {
		panic("runtime/observability: Registry was not constructed; " +
			"build one with observability.NewRegistry() rather than observability.Registry{}")
	}
	if name == "" {
		return fmt.Errorf("observability: metric name must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.names[name]; exists {
		return fmt.Errorf("observability: metric %q already registered", name)
	}
	r.names[name] = struct{}{}
	return nil
}

// VecOpts names and describes a labeled metric.
type VecOpts struct {
	// Name is the metric's exposition name, for example
	// "http_requests_total". It must be unique within the Registry across
	// every metric kind.
	Name string
	// Help is a one-line description written into the exposition output's
	// "# HELP" line.
	Help string
	// Labels names each label, in the order values must later be supplied
	// to WithLabelValues. Label by low-cardinality dimensions only — a
	// route template, an HTTP method, a status class — never a raw path,
	// a user ID, or any other value an unauthenticated caller can vary
	// without bound.
	Labels []string
	// MaxSeries caps the number of distinct label-value combinations
	// tracked individually before further combinations collapse into one
	// shared overflow series. Zero uses DefaultMaxSeries.
	MaxSeries int
}

// NewCounterVec registers a new [CounterVec] on r.
func (r *Registry) NewCounterVec(opts VecOpts) (*CounterVec, error) {
	if err := r.reserveName(opts.Name); err != nil {
		return nil, err
	}
	cv := &CounterVec{
		name:       opts.Name,
		help:       opts.Help,
		labelNames: copyStrings(opts.Labels),
		table: newSeriesTable(opts.MaxSeries, func() *counterValue {
			return &counterValue{}
		}),
	}
	r.mu.Lock()
	r.counters = append(r.counters, cv)
	r.mu.Unlock()
	return cv, nil
}

// NewGaugeVec registers a new [GaugeVec] on r.
func (r *Registry) NewGaugeVec(opts VecOpts) (*GaugeVec, error) {
	if err := r.reserveName(opts.Name); err != nil {
		return nil, err
	}
	gv := &GaugeVec{
		name:       opts.Name,
		help:       opts.Help,
		labelNames: copyStrings(opts.Labels),
		table: newSeriesTable(opts.MaxSeries, func() *gaugeValue {
			return &gaugeValue{}
		}),
	}
	r.mu.Lock()
	r.gauges = append(r.gauges, gv)
	r.mu.Unlock()
	return gv, nil
}

// newGaugeFuncVec registers a new gaugeFuncVec on r. MaxSeries in opts is
// ignored: a gaugeFuncVec's series are registered explicitly through add,
// not created from request-controlled input, so there is nothing for a cap
// to bound — see gaugeFuncVec's doc comment for why this stays
// unexported.
func (r *Registry) newGaugeFuncVec(opts VecOpts) (*gaugeFuncVec, error) {
	if err := r.reserveName(opts.Name); err != nil {
		return nil, err
	}
	gv := &gaugeFuncVec{
		name:       opts.Name,
		help:       opts.Help,
		labelNames: copyStrings(opts.Labels),
		table:      newSeriesTable(0, func() gaugeFuncSeries { return gaugeFuncSeries{} }),
	}
	r.mu.Lock()
	r.gaugeFuncs = append(r.gaugeFuncs, gv)
	r.mu.Unlock()
	return gv, nil
}

// HistogramVecOpts describes a labeled histogram: [VecOpts] plus its
// bucket boundaries.
type HistogramVecOpts struct {
	VecOpts
	// Buckets are the upper bounds of each bucket, in ascending order. An
	// implicit +Inf bucket, matching Prometheus's cumulative "le"
	// semantics, is always included and need not be listed.
	Buckets []float64
}

// NewHistogramVec registers a new [HistogramVec] on r.
func (r *Registry) NewHistogramVec(opts HistogramVecOpts) (*HistogramVec, error) {
	if err := r.reserveName(opts.Name); err != nil {
		return nil, err
	}
	buckets := copyFloats(opts.Buckets)
	hv := &HistogramVec{
		name:       opts.Name,
		help:       opts.Help,
		labelNames: copyStrings(opts.Labels),
		buckets:    buckets,
		table: newSeriesTable(opts.MaxSeries, func() *histogramValue {
			return newHistogramValue(buckets)
		}),
	}
	r.mu.Lock()
	r.histograms = append(r.histograms, hv)
	r.mu.Unlock()
	return hv, nil
}

// copyStrings returns a copy of in, or nil when in is empty.
//
// Every constructor above copies its caller's Labels rather than aliasing
// them. A Registry keeps a Vec for the life of the process and reads
// labelNames on every scrape, so aliasing let a caller who reused or
// mutated its own slice after construction rewrite the label names of a
// live metric — and, because reads happen on the scrape goroutine, do it
// as an unsynchronized write. Constructors take their inputs by value in
// spirit; a slice argument only honors that if it is copied.
func copyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// copyFloats returns a copy of in, or nil when in is empty.
//
// HistogramVec's bucket bounds need this even more than labels do: the one
// slice is shared by every series the Vec creates, exposition reads it to
// render each "le" bound, and histogramValue.Observe reads it on the
// request path with no lock. Aliasing therefore let a caller mutate the
// exported bounds while the recorded counts stayed where they were,
// emitting a non-monotonic — malformed — Prometheus histogram, and did it
// as a data race.
func copyFloats(in []float64) []float64 {
	if len(in) == 0 {
		return nil
	}
	out := make([]float64, len(in))
	copy(out, in)
	return out
}
