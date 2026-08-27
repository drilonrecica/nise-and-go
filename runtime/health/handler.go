package health

import (
	"encoding/json"
	"net/http"
)

const contentTypeJSON = "application/json; charset=utf-8"

// okBody and unavailableBody are the only two response bodies an
// unauthenticated caller of a handler in this package ever receives. They
// are fixed: no dependency name, error string, timing, or any other
// internal detail is ever interpolated into them.
var (
	okBody          = []byte(`{"status":"ok"}`)
	unavailableBody = []byte(`{"status":"unavailable"}`)
)

func writeProbeResponse(w http.ResponseWriter, healthy bool) {
	w.Header().Set("Content-Type", contentTypeJSON)
	if healthy {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(okBody)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(unavailableBody)
}

// NewStartupHandler returns the startup probe: it reports whether gate is
// ready and nothing else. Wire it to your orchestrator's startup probe so
// the liveness and readiness probes are not consulted until initialization
// — migrations checked, pools built — has finished.
func NewStartupHandler(gate *Gate) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeProbeResponse(w, gate.Ready())
	})
}

// NewLivenessHandler returns the liveness probe: it reports whether gate is
// ready and nothing else. Wire it to your orchestrator's liveness probe,
// whose failure restarts the process — so gate should only ever be set
// false for a condition a restart can actually fix, such as a detected
// deadlock, never merely because a downstream dependency is unavailable
// (that is what readiness is for).
func NewLivenessHandler(gate *Gate) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeProbeResponse(w, gate.Ready())
	})
}

// NewReadinessHandler returns the readiness probe: it reports ready only
// when gate is ready and, if prober is non-nil, every check prober
// aggregates currently passes within its configured timeouts. Wire it to
// your load balancer's health check, whose failure stops new traffic from
// being routed here without restarting anything.
//
// The response is a status code and one of two fixed bodies — never a
// dependency name, an error string, or a timing. Use
// [NewReadinessReportHandler], mounted behind your own authorization, for a
// detailed view.
func NewReadinessHandler(gate *Gate, prober *Prober) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthy := gate.Ready()
		if healthy && prober != nil {
			healthy = prober.Probe(r.Context()).Healthy
		}
		writeProbeResponse(w, healthy)
	})
}

// readinessReport is the JSON shape NewReadinessReportHandler emits.
type readinessReport struct {
	GateReady bool     `json:"gate_ready"`
	Healthy   bool     `json:"healthy"`
	Checks    []Result `json:"checks"`
}

// NewReadinessReportHandler returns the detailed readiness view: the full
// per-check [Report], as JSON, naming every registered check, whether it
// passed, its error text, and how long it took.
//
// This is the seam for an authorized operator or internal caller to see the
// dependency topology and failure detail the public probes deliberately
// withhold. This package implements no authorization of its own — the
// caller must mount this handler only behind middleware that authenticates
// the caller first. Never expose it directly to an unauthenticated client.
func NewReadinessReportHandler(gate *Gate, prober *Prober) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		report := Report{Healthy: true}
		if prober != nil {
			report = prober.Probe(r.Context())
		}
		gateReady := gate.Ready()
		healthy := gateReady && report.Healthy

		status := http.StatusOK
		if !healthy {
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", contentTypeJSON)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(readinessReport{
			GateReady: gateReady,
			Healthy:   healthy,
			Checks:    report.Results,
		})
	})
}
