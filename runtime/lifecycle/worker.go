package lifecycle

import (
	"context"
	"fmt"
	"sync"
)

// WorkerFunc is a background-processing loop: it runs until ctx is done and
// then returns promptly. A WorkerFunc that ignores ctx and keeps running
// past it will cause [Worker.Shutdown] to time out and force the loop's
// context toward cancellation, but the goroutine running it leaks until the
// loop eventually notices — write loops that select on ctx.Done() in their
// main iteration.
type WorkerFunc func(ctx context.Context) error

type workerState int

const (
	workerIdle workerState = iota
	workerRunning
	workerStopped
)

// Worker adapts a [WorkerFunc] into a [Runnable], giving a background job
// loop the same Run/Shutdown shape as [HTTPServer]. A Worker is safe for
// concurrent use of Run and Shutdown from different goroutines, though Run
// should only ever be called once per Worker.
type Worker struct {
	loop WorkerFunc

	mu     sync.Mutex
	state  workerState
	cancel context.CancelFunc
	done   chan struct{}
}

// NewWorker builds a Worker around loop.
func NewWorker(loop WorkerFunc) *Worker {
	return &Worker{loop: loop, done: make(chan struct{})}
}

// Run starts the worker loop and blocks until it returns, either on its own
// or because [Worker.Shutdown] canceled its context. If Shutdown was already
// called before Run started, Run returns immediately without ever invoking
// the loop.
func (w *Worker) Run(ctx context.Context) error {
	w.mu.Lock()
	if w.state == workerStopped {
		w.mu.Unlock()
		return nil
	}
	w.state = workerRunning
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.mu.Unlock()

	defer cancel()
	err := w.loop(runCtx)

	w.mu.Lock()
	w.state = workerStopped
	w.mu.Unlock()
	close(w.done)

	return err
}

// Shutdown cancels the context the running loop was given and waits for it
// to return, bounded by ctx's own deadline. Calling Shutdown before Run has
// started is safe: it marks the Worker stopped so a later Run call is a
// no-op, and returns immediately. Shutdown is idempotent.
func (w *Worker) Shutdown(ctx context.Context) error {
	w.mu.Lock()
	switch w.state {
	case workerIdle:
		w.state = workerStopped
		w.mu.Unlock()
		return nil
	case workerStopped:
		w.mu.Unlock()
		return nil
	}
	cancel := w.cancel
	w.mu.Unlock()

	cancel()

	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrShutdownTimeout, ctx.Err())
	}
}
