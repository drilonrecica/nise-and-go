package lifecycle

import (
	"context"
	"errors"
	"time"
)

// DefaultShutdownTimeout is the bounded wait for in-flight work to finish
// during a graceful shutdown, applied by [HTTPServer] and available for a
// caller of [Worker.Shutdown] to reuse. Past this deadline, [HTTPServer]
// forces a close. 30 seconds matches the common default grace period
// orchestrators (for example Kubernetes) give a process between asking it to
// stop and killing it outright, so a shorter runtime-enforced bound would
// only make this package the first thing to give up.
const DefaultShutdownTimeout = 30 * time.Second

// ErrShutdownTimeout is returned by [HTTPServer.Shutdown] and [Worker.Shutdown]
// — wrapped with [errors.Is]-compatible context — when the bounded shutdown
// deadline elapsed before in-flight work finished, and a forced close or
// cancellation was required. Its absence from a returned error means the
// shutdown was clean: every in-flight request or loop iteration finished on
// its own before the deadline.
var ErrShutdownTimeout = errors.New("lifecycle: shutdown timed out before in-flight work finished; forced")

// Runnable is one independently startable and stoppable component of the
// application process — the HTTP server or the background worker loop. It
// deliberately mirrors [net/http.Server]'s own ListenAndServe/Shutdown
// shape.
type Runnable interface {
	// Run starts the component and blocks until it stops, either because
	// Shutdown was called and completed, or because it failed on its own.
	// Run returns nil only for a clean stop; any non-nil error, including
	// one wrapping [ErrShutdownTimeout], means [NewGroup]'s caller should
	// treat the process as needing to exit.
	Run(ctx context.Context) error

	// Shutdown begins a graceful stop: the component accepts no new work,
	// and Shutdown returns once existing work has finished or ctx's
	// deadline arrives, whichever comes first. Shutdown is idempotent and
	// safe to call before Run.
	Shutdown(ctx context.Context) error
}
