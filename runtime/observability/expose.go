package observability

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// prometheusContentType is the exposition format's registered media type
// (https://prometheus.io/docs/instrumenting/exposition_formats/).
const prometheusContentType = "text/plain; version=0.0.4; charset=utf-8"

// NewHandler returns an [http.Handler] that renders reg in Prometheus text
// exposition format.
//
// This handler is never mounted by default: no other package in this
// module's runtime tree references runtime/observability, so nothing wires
// it onto a generated application's public router automatically. Mounting
// it is the application's explicit choice, and it should be made
// deliberately — bind it to a separate listener on a private network or
// localhost (the usual production shape, so a Prometheus server or
// OpenMetrics-compatible scraper on the same host or private network can
// reach it without ever exposing it publicly), or mount it on the public
// router only behind the same authorization middleware guarding other
// operator-only endpoints. See docs/metrics.md.
func NewHandler(reg *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", prometheusContentType)
		_ = WriteText(w, reg)
	})
}

// WriteText renders every metric recorded on reg to w in Prometheus text
// exposition format, in deterministic (name, then label-value) order.
// [NewHandler] uses it for the HTTP endpoint; call it directly to capture
// metrics output elsewhere, such as a CLI diagnostic command or a test
// assertion.
func WriteText(w io.Writer, reg *Registry) error {
	reg.mu.Lock()
	counters := append([]*CounterVec(nil), reg.counters...)
	gauges := append([]*GaugeVec(nil), reg.gauges...)
	gaugeFuncs := append([]*GaugeFuncVec(nil), reg.gaugeFuncs...)
	histograms := append([]*HistogramVec(nil), reg.histograms...)
	reg.mu.Unlock()

	sortVecsByName(counters, func(c *CounterVec) string { return c.name })
	sortVecsByName(gauges, func(g *GaugeVec) string { return g.name })
	sortVecsByName(gaugeFuncs, func(g *GaugeFuncVec) string { return g.name })
	sortVecsByName(histograms, func(h *HistogramVec) string { return h.name })

	for _, c := range counters {
		if err := writeCounter(w, c); err != nil {
			return err
		}
	}
	for _, g := range gauges {
		if err := writeGauge(w, g); err != nil {
			return err
		}
	}
	for _, g := range gaugeFuncs {
		if err := writeGaugeFunc(w, g); err != nil {
			return err
		}
	}
	for _, h := range histograms {
		if err := writeHistogram(w, h); err != nil {
			return err
		}
	}
	return nil
}

// sortVecsByName sorts vecs in place by the name key returns, so
// exposition output is deterministic regardless of registration order.
func sortVecsByName[T any](vecs []T, key func(T) string) {
	sort.Slice(vecs, func(i, j int) bool { return key(vecs[i]) < key(vecs[j]) })
}

func writeCounter(w io.Writer, c *CounterVec) error {
	if err := writeHeader(w, c.name, c.help, "counter"); err != nil {
		return err
	}
	snap := c.table.snapshot()
	for _, key := range sortedKeys(snap) {
		values := splitLabelValues(key, len(c.labelNames))
		if err := writeSample(w, c.name, c.labelNames, values, snap[key].value()); err != nil {
			return err
		}
	}
	return nil
}

func writeGauge(w io.Writer, g *GaugeVec) error {
	if err := writeHeader(w, g.name, g.help, "gauge"); err != nil {
		return err
	}
	snap := g.table.snapshot()
	for _, key := range sortedKeys(snap) {
		values := splitLabelValues(key, len(g.labelNames))
		if err := writeSample(w, g.name, g.labelNames, values, snap[key].value()); err != nil {
			return err
		}
	}
	return nil
}

func writeGaugeFunc(w io.Writer, g *GaugeFuncVec) error {
	if err := writeHeader(w, g.name, g.help, "gauge"); err != nil {
		return err
	}
	snap := g.table.snapshot()
	for _, key := range sortedKeys(snap) {
		s := snap[key]
		if err := writeSample(w, g.name, g.labelNames, s.values, s.value()); err != nil {
			return err
		}
	}
	return nil
}

func writeHistogram(w io.Writer, h *HistogramVec) error {
	if err := writeHeader(w, h.name, h.help, "histogram"); err != nil {
		return err
	}
	snap := h.table.snapshot()
	bucketLabelNames := append(append([]string{}, h.labelNames...), "le")

	for _, key := range sortedKeys(snap) {
		values := splitLabelValues(key, len(h.labelNames))
		hv := snap[key]

		for i, upper := range hv.buckets {
			// hv.bucketCounts[i] is already the cumulative count of
			// observations <= upper: histogramValue.Observe increments
			// every bucket a value qualifies for, not just the smallest
			// one, so no further summation happens at write time.
			bucketValues := append(append([]string{}, values...), formatFloat(upper))
			if err := writeSample(w, h.name+"_bucket", bucketLabelNames, bucketValues, float64(hv.bucketCounts[i].Load())); err != nil {
				return err
			}
		}
		total := hv.count.Load()
		infValues := append(append([]string{}, values...), "+Inf")
		if err := writeSample(w, h.name+"_bucket", bucketLabelNames, infValues, float64(total)); err != nil {
			return err
		}
		if err := writeSample(w, h.name+"_sum", h.labelNames, values, hv.sum.load()); err != nil {
			return err
		}
		if err := writeSample(w, h.name+"_count", h.labelNames, values, float64(total)); err != nil {
			return err
		}
	}
	return nil
}

func writeHeader(w io.Writer, name, help, typeName string) error {
	_, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, typeName)
	return err
}

func writeSample(w io.Writer, name string, labelNames, labelValues []string, value float64) error {
	var b strings.Builder
	b.WriteString(name)
	if len(labelNames) > 0 {
		b.WriteByte('{')
		for i, ln := range labelNames {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(ln)
			b.WriteString(`="`)
			b.WriteString(escapeLabelValue(labelValues[i]))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(formatFloat(value))
	b.WriteByte('\n')
	_, err := io.WriteString(w, b.String())
	return err
}

// escapeHelp escapes the two characters Prometheus text format requires
// escaped in a HELP line: a backslash and a newline.
func escapeHelp(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// escapeLabelValue escapes the three characters Prometheus text format
// requires escaped inside a quoted label value: a backslash, a double
// quote, and a newline.
func escapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// formatFloat renders v the way Prometheus text format expects: the
// shortest decimal representation that round-trips, or one of the three
// special tokens for a non-finite value.
func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	default:
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
}
