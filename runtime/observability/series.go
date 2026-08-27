package observability

import (
	"sort"
	"strings"
	"sync"
)

// labelSeparator joins label values into one map key. It is a control
// character (Unicode information separator one) that cannot appear in a
// label value supplied through the ordinary Go string APIs this package
// exposes, so it cannot be forged by a caller to collide two distinct
// label-value tuples into the same series key.
const labelSeparator = "\x1f"

// overflowLabelValue replaces every label value once a metric's distinct
// label-value combinations reach its cap. A caller that mislabels a metric
// with something unbounded — a route-template callback that falls back to
// the raw request path, a job "kind" sourced from user input — collapses
// into this one shared catch-all series instead of growing the metric's
// cardinality without limit.
const overflowLabelValue = "overflow"

// DefaultMaxSeries is the cardinality cap [Registry.NewCounterVec],
// [Registry.NewGaugeVec], and [Registry.NewHistogramVec] apply when
// [VecOpts.MaxSeries] is zero. It is deliberately small: the label
// dimensions this package documents — HTTP method, route template, status
// class, job queue and kind — number in the dozens for a real application,
// not hundreds.
const DefaultMaxSeries = 200

// seriesTable maps a joined label-value key to one metric value of type T,
// enforcing maxSeries. A Vec (see counter.go, gauge.go, histogram.go) holds
// one seriesTable per metric name. The zero value is not usable; construct
// one with newSeriesTable.
//
// seriesTable never grows past maxSeries+1 entries: maxSeries individually
// tracked combinations, plus exactly one additional overflow series shared
// by every combination past the cap. That fixed +1, not a hard maxSeries
// ceiling, is documented deliberately — see get.
type seriesTable[T any] struct {
	mu        sync.RWMutex
	maxSeries int
	series    map[string]T
	newValue  func() T
}

func newSeriesTable[T any](maxSeries int, newValue func() T) *seriesTable[T] {
	if maxSeries <= 0 {
		maxSeries = DefaultMaxSeries
	}
	return &seriesTable[T]{
		maxSeries: maxSeries,
		series:    make(map[string]T),
		newValue:  newValue,
	}
}

// joinLabelValues builds a seriesTable key from an ordered list of label
// values. An empty label list joins to the empty string, the key for a
// metric with no labels.
func joinLabelValues(values []string) string {
	return strings.Join(values, labelSeparator)
}

// splitLabelValues is the inverse of joinLabelValues, used only by
// exposition (expose.go) to recover the label values a key was built from.
// n is the number of labels the owning Vec declared; a zero-label metric's
// only key is "", which must split to zero values, not the one-element
// slice strings.Split would otherwise return.
func splitLabelValues(key string, n int) []string {
	if n == 0 {
		return nil
	}
	return strings.Split(key, labelSeparator)
}

// get returns the metric value for values, creating it if this is a new
// combination the cap has not yet been reached for, or returning the
// shared overflow value once it has. get never returns an error and never
// panics: a metrics call sits on every request's hot path, and a labeling
// bug must never fail the request it is trying to measure.
func (t *seriesTable[T]) get(values []string) T {
	key := joinLabelValues(values)

	t.mu.RLock()
	v, ok := t.series[key]
	t.mu.RUnlock()
	if ok {
		return v
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if v, ok := t.series[key]; ok {
		return v
	}

	if len(t.series) >= t.maxSeries {
		return t.overflowLocked(len(values))
	}

	v = t.newValue()
	t.series[key] = v
	return v
}

// overflowLocked returns the shared overflow series for an n-label Vec,
// creating it on first overflow. Callers must hold t.mu for writing.
func (t *seriesTable[T]) overflowLocked(n int) T {
	overflowValues := make([]string, n)
	for i := range overflowValues {
		overflowValues[i] = overflowLabelValue
	}
	key := joinLabelValues(overflowValues)
	if v, ok := t.series[key]; ok {
		return v
	}
	v := t.newValue()
	t.series[key] = v
	return v
}

// snapshot returns a copy of every (joined label key, value) pair recorded
// so far, for exposition. Exposition sorts the keys itself for
// deterministic output; snapshot makes no ordering promise.
func (t *seriesTable[T]) snapshot() map[string]T {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]T, len(t.series))
	for k, v := range t.series {
		out[k] = v
	}
	return out
}

// sortedKeys returns the keys of m in ascending order, for deterministic
// exposition output.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
