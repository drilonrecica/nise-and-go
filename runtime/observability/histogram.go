package observability

import "sync/atomic"

// Histogram accumulates observations of a numeric value — typically a
// duration in seconds — into fixed buckets, plus a running sum and count.
// Obtain one from a [HistogramVec] with [HistogramVec.WithLabelValues].
type Histogram interface {
	// Observe records one value.
	Observe(v float64)
}

// histogramValue is the concrete, concurrency-safe [Histogram] every
// [HistogramVec] series is backed by. buckets holds the upper bound of
// each bucket in ascending order; bucketCounts[i] is the number of
// observations less than or equal to buckets[i] (Prometheus's cumulative
// "le" semantics). The implicit +Inf bucket is count, not a slot in
// bucketCounts.
type histogramValue struct {
	buckets      []float64
	bucketCounts []atomic.Uint64
	sum          atomicFloat64
	count        atomic.Uint64
}

func newHistogramValue(buckets []float64) *histogramValue {
	return &histogramValue{
		buckets:      buckets,
		bucketCounts: make([]atomic.Uint64, len(buckets)),
	}
}

// Observe records v: every bucket whose upper bound is at least v is
// incremented (cumulative semantics), and the running sum and count are
// updated.
func (h *histogramValue) Observe(v float64) {
	for i, upper := range h.buckets {
		if v <= upper {
			h.bucketCounts[i].Add(1)
		}
	}
	h.count.Add(1)
	h.sum.add(v)
}

// HistogramVec is a [Histogram] labeled by one or more label values, such
// as HTTP method, route template, and status class. Construct one with
// [Registry.NewHistogramVec].
type HistogramVec struct {
	name       string
	help       string
	labelNames []string
	buckets    []float64
	table      *seriesTable[*histogramValue]
}

// WithLabelValues returns the [Histogram] for this exact combination of
// label values, given in the same order as the labels declared in
// [VecOpts.Labels], creating that series if this is the first time these
// values have been seen. See [CounterVec.WithLabelValues] for the
// label-count mismatch and cardinality-cap behavior, which is identical
// here.
func (v *HistogramVec) WithLabelValues(values ...string) Histogram {
	return v.table.get(normalizeLabelValues(v.labelNames, values))
}
