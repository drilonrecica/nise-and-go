package observability

import "fmt"

// Gauge is a value that can move up or down, such as the number of
// requests currently in flight. Obtain one from a [GaugeVec] with
// [GaugeVec.WithLabelValues].
type Gauge interface {
	// Set replaces the current value with v.
	Set(v float64)
	// Inc increments the value by 1.
	Inc()
	// Dec decrements the value by 1.
	Dec()
	// Add increments the value by delta, which may be negative.
	Add(delta float64)
}

// gaugeValue is the concrete, concurrency-safe [Gauge] every [GaugeVec]
// series is backed by.
type gaugeValue struct {
	v atomicFloat64
}

func (g *gaugeValue) Set(v float64)     { g.v.store(v) }
func (g *gaugeValue) Inc()              { g.v.add(1) }
func (g *gaugeValue) Dec()              { g.v.add(-1) }
func (g *gaugeValue) Add(delta float64) { g.v.add(delta) }
func (g *gaugeValue) value() float64    { return g.v.load() }

// GaugeVec is a [Gauge] labeled by one or more label values. Construct one
// with [Registry.NewGaugeVec].
type GaugeVec struct {
	name       string
	help       string
	labelNames []string
	table      *seriesTable[*gaugeValue]
}

// WithLabelValues returns the [Gauge] for this exact combination of label
// values, given in the same order as the labels declared in
// [VecOpts.Labels], creating that series if this is the first time these
// values have been seen. See [CounterVec.WithLabelValues] for the
// label-count mismatch and cardinality-cap behavior, which is identical
// here.
func (v *GaugeVec) WithLabelValues(values ...string) Gauge {
	return v.table.get(normalizeLabelValues(v.labelNames, values))
}

// gaugeFuncSeries is one registered (label values, callback) pair inside a
// [GaugeFuncVec].
type gaugeFuncSeries struct {
	values []string
	fn     func() float64
}

func (s gaugeFuncSeries) value() float64 { return s.fn() }

// GaugeFuncVec is a gauge whose series values are computed on demand, at
// exposition time, by a caller-supplied function rather than pushed
// incrementally by application code. It exists for values a dependency
// already tracks internally — a connection pool's open-connection count is
// the motivating case (see [PoolMetrics]) — where re-deriving the value as
// a pushed [Gauge] would just be a second, potentially stale copy of a
// number the dependency can report accurately on demand. Construct one
// with [Registry.NewGaugeFuncVec].
//
// Unlike [GaugeVec], a GaugeFuncVec's series are fixed at registration
// time through [GaugeFuncVec.Add], not created on the fly per request:
// there is no cardinality cap here because nothing request-controlled ever
// reaches it — see Add.
type GaugeFuncVec struct {
	name       string
	help       string
	labelNames []string
	table      *seriesTable[gaugeFuncSeries]
}

// Add registers fn under this exact combination of label values, given in
// the same order as the labels declared in [VecOpts.Labels]. fn is called
// on every scrape and must be safe for concurrent use; it should return
// quickly, since it runs synchronously while the exposition handler is
// being written.
//
// Add is a startup-time call, not a per-request one: an application
// registers one series per pool, or per whatever fixed set of dependencies
// it constructs, not one per request. Calling Add again with the same
// label values is an error — a GaugeFuncVec series is meant to be
// registered exactly once.
func (v *GaugeFuncVec) Add(fn func() float64, values ...string) error {
	values = normalizeLabelValues(v.labelNames, values)
	key := joinLabelValues(values)

	v.table.mu.Lock()
	defer v.table.mu.Unlock()
	if _, exists := v.table.series[key]; exists {
		return fmt.Errorf("observability: %s already registered for label values %v", v.name, values)
	}
	v.table.series[key] = gaugeFuncSeries{values: values, fn: fn}
	return nil
}
