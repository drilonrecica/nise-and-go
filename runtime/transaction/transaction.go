// Package transaction runs one explicit unit of work inside a caller-supplied
// transaction implementation. It is database-driver independent: generated
// application code adapts pgx and passes the resulting transaction directly to
// sqlc-generated query constructors.
package transaction

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const defaultRollbackTimeout = 5 * time.Second

// Isolation names the transaction isolation levels that generated adapters
// must support. The empty value uses the database's configured default.
type Isolation string

const (
	// DefaultIsolation uses the database's configured isolation level.
	DefaultIsolation Isolation = ""
	// ReadCommitted permits each statement to observe rows committed before it began.
	ReadCommitted Isolation = "read_committed"
	// RepeatableRead gives the transaction one stable snapshot.
	RepeatableRead Isolation = "repeatable_read"
	// Serializable requires PostgreSQL to reject non-serializable executions.
	Serializable Isolation = "serializable"
)

// AccessMode controls whether a transaction may write. The empty value uses
// the database's configured default.
type AccessMode string

const (
	// DefaultAccess uses the database's configured transaction access mode.
	DefaultAccess AccessMode = ""
	// ReadWrite permits reads and writes.
	ReadWrite AccessMode = "read_write"
	// ReadOnly rejects writes at the database transaction boundary.
	ReadOnly AccessMode = "read_only"
)

// Options are the database-independent transaction properties selected by a
// use case. Generated driver adapters translate them without changing their
// meaning.
type Options struct {
	Isolation Isolation
	Access    AccessMode
}

func (o Options) validate() error {
	switch o.Isolation {
	case DefaultIsolation, ReadCommitted, RepeatableRead, Serializable:
	default:
		return fmt.Errorf("transaction isolation %q is not supported", o.Isolation)
	}
	switch o.Access {
	case DefaultAccess, ReadWrite, ReadOnly:
	default:
		return fmt.Errorf("transaction access mode %q is not supported", o.Access)
	}
	return nil
}

// Transaction is the lifecycle surface Runner needs. A pgx.Tx satisfies this
// interface and retains its query methods when used as Runner's type argument.
type Transaction interface {
	Commit(context.Context) error
	Rollback(context.Context) error
}

// BeginFunc starts one driver transaction with the requested options.
type BeginFunc[T Transaction] func(context.Context, Options) (T, error)

type settings struct {
	rollbackTimeout time.Duration
}

// Option configures a Runner. Applications normally use the defaults; options
// exist for bounded test and deployment-specific cleanup behavior.
type Option func(*settings) error

// WithRollbackTimeout bounds rollback cleanup after work fails, the context is
// canceled, or the callback panics. Cleanup uses a context detached from the
// canceled business context but retaining its values.
func WithRollbackTimeout(timeout time.Duration) Option {
	return func(cfg *settings) error {
		if timeout <= 0 {
			return errors.New("transaction rollback timeout must be greater than zero")
		}
		cfg.rollbackTimeout = timeout
		return nil
	}
}

// Runner owns begin, commit, and rollback around one use-case callback. It has
// no global registry and is safe for concurrent use when its BeginFunc is.
type Runner[T Transaction] struct {
	begin           BeginFunc[T]
	rollbackTimeout time.Duration
}

// New constructs a Runner around an explicit driver adapter.
func New[T Transaction](begin BeginFunc[T], options ...Option) (*Runner[T], error) {
	if begin == nil {
		return nil, errors.New("transaction begin function must not be nil")
	}
	cfg := settings{rollbackTimeout: defaultRollbackTimeout}
	for index, option := range options {
		if option == nil {
			return nil, fmt.Errorf("transaction option %d must not be nil", index)
		}
		if err := option(&cfg); err != nil {
			return nil, fmt.Errorf("transaction option %d: %w", index, err)
		}
	}
	return &Runner[T]{begin: begin, rollbackTimeout: cfg.rollbackTimeout}, nil
}

// NestedError reports an attempted Runner.Within call from an active
// transaction callback. Runner never invents savepoints or commits an inner
// unit independently; compose work inside the outer callback instead.
type NestedError struct{}

// Error implements error.
func (*NestedError) Error() string {
	return "nested transaction is not allowed; compose work inside the existing transaction callback"
}

type activeContextKey struct{}

// Within runs work in one transaction. Success commits. Work failure or
// cancellation rolls back and preserves both the primary and rollback errors.
// A panic triggers bounded best-effort rollback and is re-panicked unchanged.
// The derived callback context must be propagated so nested use can be rejected.
func (r *Runner[T]) Within(ctx context.Context, options Options, work func(context.Context, T) error) error {
	if r == nil || r.begin == nil {
		return errors.New("transaction runner must not be nil")
	}
	if ctx == nil {
		return errors.New("transaction context must not be nil")
	}
	if work == nil {
		return errors.New("transaction work function must not be nil")
	}
	if ctx.Value(activeContextKey{}) != nil {
		return &NestedError{}
	}
	if err := options.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	tx, err := r.begin(ctx, options)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = r.rollback(ctx, tx)
			panic(recovered)
		}
	}()

	workContext := context.WithValue(ctx, activeContextKey{}, struct{}{})
	if err := work(workContext, tx); err != nil {
		return errors.Join(err, r.rollback(ctx, tx))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, r.rollback(ctx, tx))
	}
	if err := tx.Commit(ctx); err != nil {
		_ = r.rollback(ctx, tx)
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (r *Runner[T]) rollback(ctx context.Context, tx T) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.rollbackTimeout)
	defer cancel()
	if err := tx.Rollback(cleanupContext); err != nil {
		return fmt.Errorf("rollback transaction: %w", err)
	}
	return nil
}
