package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func doGet(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStartupHandler(t *testing.T) {
	gate := NewGate(false)
	h := NewStartupHandler(gate)

	rec := doGet(t, h)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if rec.Body.String() != string(unavailableBody) {
		t.Errorf("body = %q, want %q", rec.Body.String(), unavailableBody)
	}

	gate.Set(true)
	rec = doGet(t, h)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != string(okBody) {
		t.Errorf("body = %q, want %q", rec.Body.String(), okBody)
	}
}

func TestLivenessHandler(t *testing.T) {
	gate := NewGate(true)
	h := NewLivenessHandler(gate)

	rec := doGet(t, h)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	gate.Set(false)
	rec = doGet(t, h)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadinessHandler_NilProber(t *testing.T) {
	gate := NewGate(true)
	h := NewReadinessHandler(gate, nil)
	rec := doGet(t, h)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadinessHandler_GateFalse_ChecksNeverConsulted(t *testing.T) {
	gate := NewGate(false)
	called := false
	prober := NewProber([]Check{{Name: "db", Func: func(context.Context) error {
		called = true
		return nil
	}}})
	h := NewReadinessHandler(gate, prober)

	rec := doGet(t, h)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if called {
		t.Error("check was invoked even though the gate was already not ready")
	}
}

// TestReadinessHandler_LeaksNoDetail is the information-disclosure test: a
// check failing with a message that names internal topology must never
// appear anywhere in the public readiness response.
func TestReadinessHandler_LeaksNoDetail(t *testing.T) {
	sensitive := "postgres: connection refused at 10.0.1.5:5432"
	gate := NewGate(true)
	prober := NewProber([]Check{{Name: "db", Func: func(context.Context) error {
		return errors.New(sensitive)
	}}})
	h := NewReadinessHandler(gate, prober)

	rec := doGet(t, h)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	body := rec.Body.String()
	if body != string(unavailableBody) {
		t.Errorf("body = %q, want exactly %q", body, unavailableBody)
	}
	if strings.Contains(body, "10.0.1.5") || strings.Contains(body, "db") || strings.Contains(body, "postgres") {
		t.Errorf("public readiness response leaked internal detail: %q", body)
	}
}

func TestReadinessReportHandler_IncludesDetail(t *testing.T) {
	sensitive := "postgres: connection refused at 10.0.1.5:5432"
	gate := NewGate(true)
	prober := NewProber([]Check{{Name: "db", Func: func(context.Context) error {
		return errors.New(sensitive)
	}}})
	h := NewReadinessReportHandler(gate, prober)

	rec := doGet(t, h)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var got struct {
		GateReady bool     `json:"gate_ready"`
		Healthy   bool     `json:"healthy"`
		Checks    []Result `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if !got.GateReady {
		t.Error("GateReady = false, want true")
	}
	if got.Healthy {
		t.Error("Healthy = true, want false")
	}
	if len(got.Checks) != 1 || got.Checks[0].Name != "db" || got.Checks[0].Error != sensitive {
		t.Errorf("Checks = %+v, want one db check carrying the detailed error", got.Checks)
	}
}

// TestReadinessHandler_ConcurrentWithGateChanges is the race requirement
// from the brief: the readiness probe must be callable concurrently with
// readiness state (Gate) changes. Run with -race.
func TestReadinessHandler_ConcurrentWithGateChanges(t *testing.T) {
	gate := NewGate(true)
	prober := NewProber([]Check{{Name: "db", Func: alwaysHealthy}})
	h := NewReadinessHandler(gate, prober)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		toggle := true
		for {
			select {
			case <-stop:
				return
			default:
				gate.Set(toggle)
				toggle = !toggle
			}
		}
	}()

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				rec := doGet(t, h)
				if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
					t.Errorf("unexpected status %d", rec.Code)
				}
			}
		}()
	}

	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()
}
