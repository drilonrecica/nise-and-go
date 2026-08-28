package health_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/health"
)

// TestHandlerConstructorsRejectNilGate covers a zero-value defect this fix
// wave closed. A nil *Gate used to be accepted silently and then
// dereferenced inside the handler, so the first symptom of a wiring mistake
// was a panic served on the liveness probe rather than a failure in the
// process's own startup path. runtime/secure.Middleware already failed this
// way; these three did not.
func TestHandlerConstructorsRejectNilGate(t *testing.T) {
	tests := []struct {
		name string
		call func()
	}{
		{name: "NewStartupHandler", call: func() { health.NewStartupHandler(nil) }},
		{name: "NewLivenessHandler", call: func() { health.NewLivenessHandler(nil) }},
		{name: "NewReadinessHandler", call: func() { health.NewReadinessHandler(nil, nil) }},
		{name: "NewReadinessReportHandler", call: func() { health.NewReadinessReportHandler(nil, nil) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				p := recover()
				if p == nil {
					t.Fatal("expected a panic for a nil *Gate")
				}
				msg, ok := p.(string)
				if !ok {
					t.Fatalf("panic value is %T, want a diagnostic string: %v", p, p)
				}
				for _, want := range []string{"runtime/health", tc.name, "NewGate"} {
					if !strings.Contains(msg, want) {
						t.Errorf("panic message %q does not mention %q", msg, want)
					}
				}
			}()
			tc.call()
		})
	}
}

// TestZeroGateIsValidAndFailsClosed pins the other half of the rule: only
// nil is rejected. A composite-literal Gate is a legitimate way to build one
// and starts not ready, which is the fail-closed answer.
func TestZeroGateIsValidAndFailsClosed(t *testing.T) {
	var g health.Gate
	if g.Ready() {
		t.Error("a zero-valued Gate reports ready; it must start not ready")
	}
	if health.NewLivenessHandler(&g) == nil {
		t.Error("NewLivenessHandler(&health.Gate{}) returned nil")
	}
}
