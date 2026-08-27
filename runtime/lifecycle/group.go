package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type namedRunnable struct {
	name string
	r    Runnable
}

// Group runs the fixed set of [Runnable] components a [Mode] selects,
// together, and enforces the composition rule mode selection exists for: if
// any one component's Run returns before the others do, every other
// component in the Group is shut down before [Group.Run] returns, so a
// failure in one component never leaves the process half-running.
type Group struct {
	items           []namedRunnable
	shutdownTimeout time.Duration
}

// GroupOption configures a [Group] built by [NewGroup].
type GroupOption func(*groupConfig)

type groupConfig struct {
	shutdownTimeout time.Duration
}

// WithGroupShutdownTimeout overrides [DefaultShutdownTimeout] for the bound
// Group.Run applies when tearing down surviving components after one fails.
func WithGroupShutdownTimeout(d time.Duration) GroupOption {
	return func(c *groupConfig) { c.shutdownTimeout = d }
}

// NewGroup builds the Group that mode selects: [ModeWeb] runs only web,
// [ModeWorker] runs only worker, and [ModeAll] runs both. It returns an
// error if mode is not one of those three values, or if the component(s)
// mode requires are nil — for example, [ModeAll] with a nil worker.
func NewGroup(mode Mode, web, worker Runnable, opts ...GroupOption) (*Group, error) {
	cfg := groupConfig{shutdownTimeout: DefaultShutdownTimeout}
	for _, opt := range opts {
		opt(&cfg)
	}

	var items []namedRunnable
	switch mode {
	case ModeWeb:
		if web == nil {
			return nil, fmt.Errorf("lifecycle: mode %q requires a non-nil web component", mode)
		}
		items = []namedRunnable{{"web", web}}
	case ModeWorker:
		if worker == nil {
			return nil, fmt.Errorf("lifecycle: mode %q requires a non-nil worker component", mode)
		}
		items = []namedRunnable{{"worker", worker}}
	case ModeAll:
		if web == nil || worker == nil {
			return nil, fmt.Errorf("lifecycle: mode %q requires non-nil web and worker components", mode)
		}
		items = []namedRunnable{{"web", web}, {"worker", worker}}
	default:
		return nil, fmt.Errorf("lifecycle: unknown mode %q", mode)
	}

	return &Group{items: items, shutdownTimeout: cfg.shutdownTimeout}, nil
}

type groupOutcome struct {
	name string
	err  error
}

// Run starts every component this Group selected, each in its own
// goroutine, and blocks until all of them have stopped. As soon as any one
// component's Run call returns, Run shuts down every other still-running
// component, bounded by the Group's shutdown timeout, before returning. The
// returned error is the first non-nil error observed, prefixed with the name
// of the component that produced it; it is nil only if every component
// stopped cleanly.
func (g *Group) Run(ctx context.Context) error {
	results := make(chan groupOutcome, len(g.items))
	for _, it := range g.items {
		go func(it namedRunnable) {
			results <- groupOutcome{name: it.name, err: it.r.Run(ctx)}
		}(it)
	}

	var firstErr error
	stoppedOthers := false
	remaining := len(g.items)

	for remaining > 0 {
		o := <-results
		remaining--
		if o.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", o.name, o.err)
		}
		if !stoppedOthers {
			stoppedOthers = true
			g.shutdownExcept(o.name)
		}
	}

	return firstErr
}

// shutdownExcept calls Shutdown on every component other than skip,
// concurrently, each bounded by the Group's own shutdown timeout so one slow
// or hung component being torn down cannot delay another.
func (g *Group) shutdownExcept(skip string) {
	var wg sync.WaitGroup
	for _, it := range g.items {
		if it.name == skip {
			continue
		}
		wg.Add(1)
		go func(it namedRunnable) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), g.shutdownTimeout)
			defer cancel()
			_ = it.r.Shutdown(ctx) // best-effort teardown; Run's own return value is the error of record.
		}(it)
	}
	wg.Wait()
}

// Shutdown gracefully stops every component this Group selected,
// concurrently, and returns their combined errors joined together, or nil if
// every component shut down cleanly.
func (g *Group) Shutdown(ctx context.Context) error {
	errs := make([]error, len(g.items))
	var wg sync.WaitGroup
	for i, it := range g.items {
		wg.Add(1)
		go func(i int, it namedRunnable) {
			defer wg.Done()
			if err := it.r.Shutdown(ctx); err != nil {
				errs[i] = fmt.Errorf("%s: %w", it.name, err)
			}
		}(i, it)
	}
	wg.Wait()
	return errors.Join(errs...)
}
