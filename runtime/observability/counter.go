package observability

// Counter is a value that only ever increases, such as a request count or
// a completed-job count. Obtain one from a [CounterVec] with
// [CounterVec.WithLabelValues].
type Counter interface {
	// Inc increments the counter by 1.
	Inc()
	// Add increments the counter by delta. A negative delta is a caller
	// bug — a counter never decreases — and is silently ignored rather
	// than panicking on what is usually a request's hot path.
	Add(delta float64)
}

// counterValue is the concrete, concurrency-safe [Counter] every
// [CounterVec] series is backed by.
type counterValue struct {
	v atomicFloat64
}

func (c *counterValue) Inc() { c.v.add(1) }

func (c *counterValue) Add(delta float64) {
	if delta < 0 {
		return
	}
	c.v.add(delta)
}

func (c *counterValue) value() float64 { return c.v.load() }

// CounterVec is a [Counter] labeled by one or more label values, such as
// HTTP method and route template. Construct one with
// [Registry.NewCounterVec].
type CounterVec struct {
	name       string
	help       string
	labelNames []string
	table      *seriesTable[*counterValue]
}

// WithLabelValues returns the [Counter] for this exact combination of
// label values, given in the same order as the labels declared in
// [VecOpts.Labels], creating that series if this is the first time these
// values have been seen. If values has a different length than the
// declared labels, WithLabelValues treats every missing value as the empty
// string and ignores extras, rather than panicking — a label-count
// mismatch is a caller bug that a metrics call must not turn into a
// request failure.
//
// The number of distinct combinations WithLabelValues has been called with
// is capped at [VecOpts.MaxSeries]; see the package documentation's
// cardinality section.
func (v *CounterVec) WithLabelValues(values ...string) Counter {
	return v.table.get(normalizeLabelValues(v.labelNames, values))
}

// normalizeLabelValues makes values exactly len(labelNames) long by
// padding with empty strings or truncating, so a caller's label-count
// mistake resolves to one consistent series instead of panicking.
func normalizeLabelValues(labelNames, values []string) []string {
	if len(values) == len(labelNames) {
		return values
	}
	out := make([]string, len(labelNames))
	copy(out, values)
	return out
}
