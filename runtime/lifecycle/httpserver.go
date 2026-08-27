package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/health"
)

// DefaultDrainPeriod is how long [HTTPServer.Shutdown] waits, after marking
// its [health.Gate] not ready but before it stops accepting new
// connections, when no [WithDrainPeriod] option overrides it.
//
// The reasoning: a load balancer or ingress does not learn that an instance
// has failed readiness the instant it fails — it learns on its own polling
// interval, commonly every few seconds. Five seconds comfortably covers at
// least one such poll for the great majority of default load balancer and
// ingress configurations, so by the time this instance actually stops
// accepting connections, traffic has almost always already stopped being
// routed here. A deployment whose health-check interval is longer than this
// should override it with [WithDrainPeriod] to match.
const DefaultDrainPeriod = 5 * time.Second

// DefaultReadHeaderTimeout bounds how long HTTPServer waits to read a
// request's headers once a connection is accepted, when no
// [WithReadHeaderTimeout] option overrides it. Without a bound here, a slow
// or malicious client that trickles headers one byte at a time can hold a
// connection, and the goroutine serving it, open indefinitely.
const DefaultReadHeaderTimeout = 10 * time.Second

// HTTPServer wraps [net/http.Server], adding the drain sequence that makes a
// zero-downtime deploy possible: see the package doc comment for the full
// ordering and its rationale.
type HTTPServer struct {
	addr   string
	inner  *http.Server
	gate   *health.Gate
	logger *slog.Logger

	drainPeriod     time.Duration
	shutdownTimeout time.Duration

	shutdownOnce sync.Once

	// onListen, when set, is called synchronously from Run right after the
	// listener is bound and before Serve blocks. It exists only so this
	// package's own tests can learn an ephemeral ":0" port; it is
	// deliberately unexported and not part of the public API.
	onListen func(net.Addr)
}

// HTTPServerOption configures an [HTTPServer] built by [NewHTTPServer].
type HTTPServerOption func(*HTTPServer)

// WithDrainPeriod overrides [DefaultDrainPeriod].
func WithDrainPeriod(d time.Duration) HTTPServerOption {
	return func(s *HTTPServer) { s.drainPeriod = d }
}

// WithShutdownTimeout overrides [DefaultShutdownTimeout] for this server's
// wait on in-flight requests before it forces a close.
func WithShutdownTimeout(d time.Duration) HTTPServerOption {
	return func(s *HTTPServer) { s.shutdownTimeout = d }
}

// WithReadHeaderTimeout overrides [DefaultReadHeaderTimeout].
func WithReadHeaderTimeout(d time.Duration) HTTPServerOption {
	return func(s *HTTPServer) { s.inner.ReadHeaderTimeout = d }
}

// WithLogger attaches logger for HTTPServer to report its own shutdown
// lifecycle to: the start of the drain period and a forced close after the
// shutdown timeout elapsed. A nil logger (the default) means HTTPServer logs
// nothing; it never constructs one of its own.
func WithLogger(logger *slog.Logger) HTTPServerOption {
	return func(s *HTTPServer) { s.logger = logger }
}

// NewHTTPServer builds an HTTPServer that will listen on addr and serve
// handler once [HTTPServer.Run] is called. gate is flipped not-ready at the
// start of [HTTPServer.Shutdown]; pass the same [health.Gate] used to build
// your readiness probe with [health.NewReadinessHandler].
func NewHTTPServer(addr string, handler http.Handler, gate *health.Gate, opts ...HTTPServerOption) *HTTPServer {
	s := &HTTPServer{
		addr: addr,
		inner: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: DefaultReadHeaderTimeout,
		},
		gate:            gate,
		drainPeriod:     DefaultDrainPeriod,
		shutdownTimeout: DefaultShutdownTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Run listens on addr and serves until [HTTPServer.Shutdown] is called and
// completes, or the listener fails. It returns nil for a clean shutdown. If
// ctx is already done, Run returns its error without binding a listener at
// all.
func (s *HTTPServer) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("lifecycle: listen on %s: %w", s.addr, err)
	}
	if s.onListen != nil {
		s.onListen(ln.Addr())
	}

	err = s.inner.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown runs the drain sequence and then stops the server: it marks gate
// not ready, waits the configured drain period, then stops accepting new
// connections and waits — bounded by the configured shutdown timeout,
// further bounded by ctx's own deadline if it is earlier — for in-flight
// requests to finish. If that bound elapses first, Shutdown forces every
// remaining connection closed and returns an error wrapping
// [ErrShutdownTimeout].
//
// Shutdown is idempotent: the gate flip and drain wait happen at most once,
// and calling Shutdown before Run, or multiple times concurrently, is safe.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		if s.gate != nil {
			s.gate.Set(false)
		}
		if s.logger != nil {
			s.logger.Info("http server draining", "drain_period", s.drainPeriod)
		}
		time.Sleep(s.drainPeriod)
	})

	shutdownCtx, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()

	if err := s.inner.Shutdown(shutdownCtx); err != nil {
		_ = s.inner.Close()
		if s.logger != nil {
			s.logger.Warn("http server shutdown timed out; forced close", "error", err)
		}
		return fmt.Errorf("%w: %w", ErrShutdownTimeout, err)
	}
	return nil
}
