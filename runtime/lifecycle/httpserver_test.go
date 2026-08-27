package lifecycle

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/health"
)

// startServer runs s.Run in a goroutine on ":0" style addresses, waits for
// the listener to bind (via the unexported onListen test hook), and returns
// its dial-able address plus the channel Run's eventual result arrives on.
func startServer(t *testing.T, s *HTTPServer) (addr string, runDone <-chan error) {
	t.Helper()
	addrCh := make(chan string, 1)
	s.onListen = func(a net.Addr) { addrCh <- a.String() }

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()

	select {
	case a := <-addrCh:
		return a, done
	case err := <-done:
		t.Fatalf("Run() returned before binding a listener: %v", err)
		return "", nil
	case <-time.After(2 * time.Second):
		t.Fatal("server never bound a listener")
		return "", nil
	}
}

func TestHTTPServer_ListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	s := NewHTTPServer(ln.Addr().String(), http.NewServeMux(), health.NewGate(true))
	if err := s.Run(context.Background()); err == nil {
		t.Error("Run() on an already-bound address = nil, want an error")
	}
}

func TestHTTPServer_RunReturnsCtxErrIfAlreadyDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := NewHTTPServer("127.0.0.1:0", http.NewServeMux(), health.NewGate(true))
	if err := s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run(canceled ctx) = %v, want context.Canceled", err)
	}
}

func TestHTTPServer_GracefulShutdown_InFlightRequestCompletes(t *testing.T) {
	release := make(chan struct{})
	reached := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		<-release
		w.WriteHeader(http.StatusOK)
	})

	gate := health.NewGate(true)
	s := NewHTTPServer("127.0.0.1:0", mux, gate, WithDrainPeriod(10*time.Millisecond))
	addr, runDone := startServer(t, s)

	reqDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + addr)
		if err == nil {
			_ = resp.Body.Close()
		}
		reqDone <- err
	}()
	<-reached

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- s.Shutdown(ctx)
	}()

	time.Sleep(30 * time.Millisecond) // let Shutdown pass the drain period and begin draining.
	close(release)                    // let the in-flight handler finish.

	if err := <-reqDone; err != nil {
		t.Errorf("in-flight request failed: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Errorf("Shutdown() = %v, want nil (clean shutdown)", err)
	}
	if err := <-runDone; err != nil {
		t.Errorf("Run() = %v, want nil", err)
	}
}

func TestHTTPServer_ShutdownTimeout_CutsOffSlowRequest(t *testing.T) {
	block := make(chan struct{}) // never closed.
	reached := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		<-block
	})

	gate := health.NewGate(true)
	s := NewHTTPServer("127.0.0.1:0", mux, gate,
		WithDrainPeriod(1*time.Millisecond),
		WithShutdownTimeout(30*time.Millisecond),
	)
	addr, _ := startServer(t, s)

	go func() {
		resp, err := http.Get("http://" + addr)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-reached

	start := time.Now()
	err := s.Shutdown(context.Background())
	elapsed := time.Since(start)

	if !errors.Is(err, ErrShutdownTimeout) {
		t.Errorf("Shutdown() = %v, want an error wrapping ErrShutdownTimeout", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("Shutdown took %s, want close to the configured 30ms timeout", elapsed)
	}
}

// TestHTTPServer_ReadinessFailsBeforeListenerStopsAccepting is the ordering
// test the brief calls out as the requirement most likely to be implemented
// backwards: readiness must fail, and stay failed for the drain period,
// before the listener actually stops accepting new connections.
func TestHTTPServer_ReadinessFailsBeforeListenerStopsAccepting(t *testing.T) {
	const drainPeriod = 150 * time.Millisecond

	gate := health.NewGate(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s := NewHTTPServer("127.0.0.1:0", mux, gate, WithDrainPeriod(drainPeriod))
	addr, _ := startServer(t, s)

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownDone <- s.Shutdown(ctx)
	}()

	// Give Shutdown a moment to begin, but stay well inside the drain window.
	time.Sleep(20 * time.Millisecond)

	if gate.Ready() {
		t.Fatal("gate is still ready shortly after Shutdown began; readiness must fail immediately")
	}

	// The listener must still be accepting new connections during the drain
	// window: a new request sent now must still succeed.
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr)
	if err != nil {
		t.Fatalf("request during the drain window failed, want it to still be accepted: %v", err)
	}
	_ = resp.Body.Close()

	if err := <-shutdownDone; err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}

	// After Shutdown has completed, the listener must be closed: a new
	// connection attempt must now fail.
	if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		t.Error("dial succeeded after Shutdown completed; the listener should have stopped accepting")
	}
}

func TestHTTPServer_ShutdownIdempotent(t *testing.T) {
	gate := health.NewGate(true)
	s := NewHTTPServer("127.0.0.1:0", http.NewServeMux(), gate, WithDrainPeriod(time.Millisecond))
	startServer(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown() = %v, want nil", err)
	}
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("second Shutdown() = %v, want nil", err)
	}
}

func TestHTTPServer_ShutdownBeforeStart(t *testing.T) {
	gate := health.NewGate(true)
	s := NewHTTPServer("127.0.0.1:0", http.NewServeMux(), gate, WithDrainPeriod(time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown() before Run = %v, want nil", err)
	}
	if gate.Ready() {
		t.Error("gate is still ready after Shutdown before Run")
	}
}
