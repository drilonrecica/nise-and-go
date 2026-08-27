package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeRunnable is a Runnable test double that records whether Run and
// Shutdown were invoked, and blocks in Run until either Shutdown is called
// or runErr fires.
type fakeRunnable struct {
	mu       sync.Mutex
	runCalls int
	shutdown bool
	stopped  chan struct{}

	runErr      error         // if set, Run returns this immediately instead of blocking.
	shutdownErr error         // returned by Shutdown.
	stop        chan struct{} // closed by Shutdown to unblock a blocking Run.
}

func newFakeRunnable() *fakeRunnable {
	return &fakeRunnable{stopped: make(chan struct{}), stop: make(chan struct{})}
}

func (f *fakeRunnable) Run(ctx context.Context) error {
	f.mu.Lock()
	f.runCalls++
	f.mu.Unlock()
	defer close(f.stopped)

	if f.runErr != nil {
		return f.runErr
	}
	select {
	case <-f.stop:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeRunnable) Shutdown(ctx context.Context) error {
	f.mu.Lock()
	f.shutdown = true
	f.mu.Unlock()
	select {
	case <-f.stop:
	default:
		close(f.stop)
	}
	return f.shutdownErr
}

func (f *fakeRunnable) wasRun() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runCalls > 0
}

func (f *fakeRunnable) wasShutdown() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shutdown
}

func TestNewGroup_ModeWeb_OnlyIncludesWeb(t *testing.T) {
	web := newFakeRunnable()
	worker := newFakeRunnable()

	g, err := NewGroup(ModeWeb, web, worker)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background()) }()
	time.Sleep(20 * time.Millisecond)

	if !web.wasRun() {
		t.Error("web component was not started for ModeWeb")
	}
	if worker.wasRun() {
		t.Error("worker component was started for ModeWeb; it must not be")
	}

	_ = web.Shutdown(context.Background())
	<-done
}

func TestNewGroup_ModeWorker_OnlyIncludesWorker(t *testing.T) {
	web := newFakeRunnable()
	worker := newFakeRunnable()

	g, err := NewGroup(ModeWorker, web, worker)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background()) }()
	time.Sleep(20 * time.Millisecond)

	if web.wasRun() {
		t.Error("web component was started for ModeWorker; the HTTP port must not be bound")
	}
	if !worker.wasRun() {
		t.Error("worker component was not started for ModeWorker")
	}

	_ = worker.Shutdown(context.Background())
	<-done
}

func TestNewGroup_ModeAll_StartsBoth(t *testing.T) {
	web := newFakeRunnable()
	worker := newFakeRunnable()

	g, err := NewGroup(ModeAll, web, worker)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background()) }()
	time.Sleep(20 * time.Millisecond)

	if !web.wasRun() || !worker.wasRun() {
		t.Errorf("ModeAll must start both: web=%v worker=%v", web.wasRun(), worker.wasRun())
	}

	if err := g.Shutdown(context.Background()); err != nil {
		t.Errorf("Group.Shutdown() = %v, want nil", err)
	}
	<-done

	if !web.wasShutdown() || !worker.wasShutdown() {
		t.Errorf("Group.Shutdown must stop both: web=%v worker=%v", web.wasShutdown(), worker.wasShutdown())
	}
}

func TestNewGroup_UnknownMode(t *testing.T) {
	if _, err := NewGroup(Mode("bogus"), newFakeRunnable(), newFakeRunnable()); err == nil {
		t.Error("NewGroup with an unknown mode = nil error, want an error")
	}
}

func TestNewGroup_RequiresComponentsForMode(t *testing.T) {
	tests := []struct {
		name        string
		mode        Mode
		web, worker bool // whether to supply a non-nil component
	}{
		{"web mode without web", ModeWeb, false, true},
		{"worker mode without worker", ModeWorker, true, false},
		{"all mode without web", ModeAll, false, true},
		{"all mode without worker", ModeAll, true, false},
		{"all mode without either", ModeAll, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var web, worker Runnable
			if tt.web {
				web = newFakeRunnable()
			}
			if tt.worker {
				worker = newFakeRunnable()
			}
			if _, err := NewGroup(tt.mode, web, worker); err == nil {
				t.Error("NewGroup() = nil error, want an error for a missing required component")
			}
		})
	}
}

// TestGroup_FailureTearsDownOthers is the core M2-008 safety requirement: a
// startup failure in one component must shut down the other rather than
// leaving a half-running process.
func TestGroup_FailureTearsDownOthers(t *testing.T) {
	web := newFakeRunnable()
	web.runErr = errors.New("bind: address already in use")

	worker := newFakeRunnable() // blocks in Run until Shutdown is called.

	g, err := NewGroup(ModeAll, web, worker)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background()) }()

	select {
	case runErr := <-done:
		if runErr == nil {
			t.Fatal("Group.Run() = nil, want the web component's error")
		}
		if !errors.Is(runErr, web.runErr) {
			t.Errorf("Group.Run() = %v, want it to wrap %v", runErr, web.runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Group.Run did not return after one component failed")
	}

	if !worker.wasShutdown() {
		t.Error("worker was not shut down after the web component failed; process would be left half-running")
	}
}
