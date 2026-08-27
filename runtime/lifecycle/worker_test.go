package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWorker_RunReturnsWhenLoopReturns(t *testing.T) {
	loopErr := errors.New("boom")
	w := NewWorker(func(context.Context) error { return loopErr })

	err := w.Run(context.Background())
	if !errors.Is(err, loopErr) {
		t.Errorf("Run() = %v, want %v", err, loopErr)
	}
}

func TestWorker_ShutdownCancelsLoopAndWaits(t *testing.T) {
	started := make(chan struct{})
	w := NewWorker(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})

	runErr := make(chan error, 1)
	go func() { runErr <- w.Run(context.Background()) }()
	<-started

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() = %v, want nil (loop respects ctx cancellation)", err)
	}

	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run() returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after Shutdown completed")
	}
}

func TestWorker_ShutdownTimeout_LoopIgnoresCancellation(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{}) // never closed: loop ignores ctx entirely.
	w := NewWorker(func(ctx context.Context) error {
		close(started)
		<-block
		return nil
	})

	go func() { _ = w.Run(context.Background()) }()
	<-started

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := w.Shutdown(shutdownCtx)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrShutdownTimeout) {
		t.Errorf("Shutdown() = %v, want an error wrapping ErrShutdownTimeout", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Shutdown took %s, want close to the 20ms deadline", elapsed)
	}
}

func TestWorker_ShutdownBeforeStart(t *testing.T) {
	w := NewWorker(func(context.Context) error {
		t.Fatal("loop must never run: Shutdown was called before Run")
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := w.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown() before Run = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Errorf("Shutdown before Run took %s, want an immediate return", elapsed)
	}

	// A subsequent Run call must be a safe no-op, never invoking the loop.
	if err := w.Run(context.Background()); err != nil {
		t.Errorf("Run() after Shutdown-before-start = %v, want nil", err)
	}
}

func TestWorker_ShutdownIdempotent(t *testing.T) {
	w := NewWorker(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	go func() { _ = w.Run(context.Background()) }()
	time.Sleep(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := w.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown() = %v, want nil", err)
	}
	if err := w.Shutdown(ctx); err != nil {
		t.Errorf("second Shutdown() = %v, want nil", err)
	}
}
