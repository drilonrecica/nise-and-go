package transaction

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeTx struct {
	commits            int
	rollbacks          int
	commitErr          error
	rollbackErr        error
	blockRollback      bool
	rollbackContextErr error
}

func (tx *fakeTx) Commit(context.Context) error {
	tx.commits++
	return tx.commitErr
}

func (tx *fakeTx) Rollback(ctx context.Context) error {
	tx.rollbacks++
	if tx.blockRollback {
		<-ctx.Done()
	}
	tx.rollbackContextErr = ctx.Err()
	if tx.blockRollback {
		return ctx.Err()
	}
	return tx.rollbackErr
}

func TestRunnerCommitsSuccessfulWorkWithOptions(t *testing.T) {
	t.Parallel()
	tx := &fakeTx{}
	wantOptions := Options{Isolation: Serializable, Access: ReadOnly}
	var gotOptions Options
	runner, err := New(func(_ context.Context, options Options) (*fakeTx, error) {
		gotOptions = options
		return tx, nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := runner.Within(context.Background(), wantOptions, func(context.Context, *fakeTx) error { return nil }); err != nil {
		t.Fatalf("Within: %v", err)
	}
	if gotOptions != wantOptions || tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("options=%+v commits=%d rollbacks=%d", gotOptions, tx.commits, tx.rollbacks)
	}
}

func TestRunnerRollsBackAndPreservesWorkAndRollbackErrors(t *testing.T) {
	t.Parallel()
	workErr := errors.New("work failed")
	rollbackErr := errors.New("rollback failed")
	tx := &fakeTx{rollbackErr: rollbackErr}
	runner := mustRunner(t, tx)
	err := runner.Within(context.Background(), Options{}, func(context.Context, *fakeTx) error { return workErr })
	if !errors.Is(err, workErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("Within error = %v, want joined work and rollback errors", err)
	}
	if tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
}

func TestRunnerRollsBackAfterCommitFailure(t *testing.T) {
	t.Parallel()
	commitErr := errors.New("commit failed")
	tx := &fakeTx{commitErr: commitErr}
	runner := mustRunner(t, tx)
	err := runner.Within(context.Background(), Options{}, func(context.Context, *fakeTx) error { return nil })
	if !errors.Is(err, commitErr) {
		t.Fatalf("Within error = %v, want commit failure", err)
	}
	if tx.commits != 1 || tx.rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
}

func TestRunnerRollsBackOnCancellationWithDetachedBoundedContext(t *testing.T) {
	t.Parallel()
	tx := &fakeTx{}
	runner := mustRunner(t, tx)
	ctx, cancel := context.WithCancel(context.Background())
	err := runner.Within(ctx, Options{}, func(context.Context, *fakeTx) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Within error = %v, want context.Canceled", err)
	}
	if tx.rollbacks != 1 || tx.rollbackContextErr != nil {
		t.Fatalf("rollbacks=%d rollback context error=%v", tx.rollbacks, tx.rollbackContextErr)
	}
}

func TestRunnerRollsBackAndRepanicsUnchanged(t *testing.T) {
	t.Parallel()
	tx := &fakeTx{}
	runner := mustRunner(t, tx)
	marker := &struct{ name string }{"panic marker"}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = runner.Within(context.Background(), Options{}, func(context.Context, *fakeTx) error {
			panic(marker)
		})
	}()
	if recovered != marker {
		t.Fatalf("recovered = %#v, want original marker", recovered)
	}
	if tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
}

func TestRunnerRejectsNestedUseWithoutOpeningAnotherTransaction(t *testing.T) {
	t.Parallel()
	beginCalls := 0
	runner, err := New(func(context.Context, Options) (*fakeTx, error) {
		beginCalls++
		return &fakeTx{}, nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = runner.Within(context.Background(), Options{}, func(ctx context.Context, _ *fakeTx) error {
		nestedErr := runner.Within(ctx, Options{}, func(context.Context, *fakeTx) error { return nil })
		var target *NestedError
		if !errors.As(nestedErr, &target) {
			t.Fatalf("nested error = %v, want *NestedError", nestedErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("outer Within: %v", err)
	}
	if beginCalls != 1 {
		t.Fatalf("begin calls = %d, want 1", beginCalls)
	}
}

func TestRunnerRejectsCanceledContextAndInvalidConstructionBeforeBegin(t *testing.T) {
	t.Parallel()
	beginCalls := 0
	begin := func(context.Context, Options) (*fakeTx, error) {
		beginCalls++
		return &fakeTx{}, nil
	}
	runner, err := New(begin)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Within(ctx, Options{}, func(context.Context, *fakeTx) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Within error = %v", err)
	}
	if err := runner.Within(context.Background(), Options{Isolation: Isolation("snapshot")}, func(context.Context, *fakeTx) error { return nil }); err == nil {
		t.Fatal("Within accepted an unknown isolation level")
	}
	if err := runner.Within(context.Background(), Options{Access: AccessMode("execute")}, func(context.Context, *fakeTx) error { return nil }); err == nil {
		t.Fatal("Within accepted an unknown access mode")
	}
	var nilContext context.Context
	if err := runner.Within(nilContext, Options{}, func(context.Context, *fakeTx) error { return nil }); err == nil {
		t.Fatal("Within accepted a nil context")
	}
	if err := runner.Within(context.Background(), Options{}, nil); err == nil {
		t.Fatal("Within accepted a nil work function")
	}
	if beginCalls != 0 {
		t.Fatalf("begin calls = %d, want 0", beginCalls)
	}
	if _, err := New[*fakeTx](nil); err == nil {
		t.Fatal("New accepted a nil begin function")
	}
	if _, err := New(begin, WithRollbackTimeout(0)); err == nil {
		t.Fatal("New accepted a zero rollback timeout")
	}
	if _, err := New(begin, nil); err == nil {
		t.Fatal("New accepted a nil option")
	}
}

func TestRunnerPreservesBeginError(t *testing.T) {
	t.Parallel()
	beginErr := errors.New("begin failed")
	runner, err := New(func(context.Context, Options) (*fakeTx, error) { return nil, beginErr })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = runner.Within(context.Background(), Options{}, func(context.Context, *fakeTx) error { return nil })
	if !errors.Is(err, beginErr) {
		t.Fatalf("Within error = %v, want begin error", err)
	}
}

func TestRunnerBoundsRollbackAfterFailure(t *testing.T) {
	t.Parallel()
	workErr := errors.New("work failed")
	tx := &fakeTx{blockRollback: true}
	runner, err := New(
		func(context.Context, Options) (*fakeTx, error) { return tx, nil },
		WithRollbackTimeout(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = runner.Within(context.Background(), Options{}, func(context.Context, *fakeTx) error { return workErr })
	if !errors.Is(err, workErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Within error = %v, want work and rollback deadline errors", err)
	}
	if !errors.Is(tx.rollbackContextErr, context.DeadlineExceeded) {
		t.Fatalf("rollback context error = %v, want deadline exceeded", tx.rollbackContextErr)
	}
}

func mustRunner(t *testing.T, tx *fakeTx) *Runner[*fakeTx] {
	t.Helper()
	runner, err := New(func(context.Context, Options) (*fakeTx, error) { return tx, nil })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner
}

// TestIsActive covers the predicate a package outside this one uses to refuse
// an operation that would escape the caller's transaction.
func TestIsActive(t *testing.T) {
	t.Parallel()

	if IsActive(context.Background()) {
		t.Error("IsActive(Background) = true, want false")
	}
	//nolint:staticcheck // SA1012: a nil context is exactly what this checks.
	if IsActive(nil) {
		t.Error("IsActive(nil) = true, want false")
	}

	runner, err := New(func(context.Context, Options) (*fakeTx, error) {
		return &fakeTx{}, nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var insideCallback bool
	if err := runner.Within(context.Background(), Options{}, func(ctx context.Context, _ *fakeTx) error {
		insideCallback = IsActive(ctx)
		// The parent context is not inside the transaction; only the one
		// the callback was handed is. A caller that propagated the wrong
		// one would defeat every check built on this.
		if IsActive(context.Background()) {
			t.Error("IsActive reports true for a context the callback did not derive from")
		}
		return nil
	}); err != nil {
		t.Fatalf("Within: %v", err)
	}
	if !insideCallback {
		t.Error("IsActive = false inside a Within callback")
	}
}
