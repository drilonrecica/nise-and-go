package dev

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeRunner starts nothing. It records the specs it was asked to start and
// hands back processes a test can end on demand, which is what makes the
// supervisor's restart and debounce logic testable with no real child, no
// port, and no build.
type fakeRunner struct {
	mu        sync.Mutex
	started   []Spec
	processes []*fakeProcess
	startErr  error
}

func (r *fakeRunner) Start(spec Spec) (Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return nil, r.startErr
	}
	p := &fakeProcess{done: make(chan struct{}), pid: 1000 + len(r.processes)}
	r.started = append(r.started, spec)
	r.processes = append(r.processes, p)
	return p, nil
}

func (r *fakeRunner) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.started)
}

func (r *fakeRunner) lastSpec() Spec {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.started) == 0 {
		return Spec{}
	}
	return r.started[len(r.started)-1]
}

func (r *fakeRunner) process(i int) *fakeProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.processes) {
		return nil
	}
	return r.processes[i]
}

type fakeProcess struct {
	pid      int
	done     chan struct{}
	mu       sync.Mutex
	stopped  bool
	exitErr  error
	closeOne sync.Once
}

func (p *fakeProcess) Wait() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

func (p *fakeProcess) Stop(time.Duration) error {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	p.closeOne.Do(func() { close(p.done) })
	return nil
}

func (p *fakeProcess) Pid() int { return p.pid }

// exit makes the process end on its own, as a crash would.
func (p *fakeProcess) exit(err error) {
	p.mu.Lock()
	p.exitErr = err
	p.mu.Unlock()
	p.closeOne.Do(func() { close(p.done) })
}

func (p *fakeProcess) wasStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

// eventLog collects a supervisor's events for assertions.
type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (l *eventLog) record(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) kinds() []EventKind {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]EventKind, len(l.events))
	for i, e := range l.events {
		out[i] = e.Kind
	}
	return out
}

func (l *eventLog) count(k EventKind) int {
	n := 0
	for _, got := range l.kinds() {
		if got == k {
			n++
		}
	}
	return n
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// runSupervisor starts s in a goroutine and returns a cancel func and a
// memoized wait: calling wait more than once (a test, then the cleanup)
// returns the same result rather than blocking on a drained channel.
func runSupervisor(t *testing.T, s *Supervisor, changes <-chan struct{}) (cancel context.CancelFunc, wait func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, changes) }()

	var once sync.Once
	var result error
	var timedOut bool
	wait = func() error {
		once.Do(func() {
			select {
			case result = <-done:
			case <-time.After(3 * time.Second):
				timedOut = true
			}
		})
		if timedOut {
			t.Fatal("Supervisor.Run did not return after its context was canceled")
		}
		return result
	}
	t.Cleanup(func() {
		cancel()
		_ = wait()
	})
	return cancel, wait
}

func TestSupervisorStartsAndStopsCleanly(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	log := &eventLog{}
	s := &Supervisor{
		Name:    "app",
		Runner:  runner,
		Spec:    func() Spec { return Spec{Name: "app", Argv: []string{"bin"}} },
		OnEvent: log.record,
	}
	cancel, wait := runSupervisor(t, s, make(chan struct{}))

	waitFor(t, "the child to start", func() bool { return runner.startCount() == 1 })
	cancel()
	if err := wait(); err != nil {
		t.Fatalf("Run returned %v, want nil on cancellation", err)
	}
	if !runner.process(0).wasStopped() {
		t.Fatal("the child was orphaned: Run returned without stopping it")
	}
	if log.count(EventStopped) != 1 {
		t.Fatalf("events = %v, want exactly one stopped event", log.kinds())
	}
}

// TestSupervisorDebouncesABurstIntoOneRestart is the property the whole
// design exists for: a `git checkout` or a formatter-on-save produces a
// burst of changes, and it must produce exactly one rebuild.
func TestSupervisorDebouncesABurstIntoOneRestart(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	log := &eventLog{}
	changes := make(chan struct{}, 1)
	s := &Supervisor{
		Name:     "app",
		Runner:   runner,
		Spec:     func() Spec { return Spec{Name: "app", Argv: []string{"bin"}} },
		Debounce: 60 * time.Millisecond,
		OnEvent:  log.record,
	}
	runSupervisor(t, s, changes)
	waitFor(t, "the first start", func() bool { return runner.startCount() == 1 })

	// Twenty changes inside one debounce window.
	for i := 0; i < 20; i++ {
		changes <- struct{}{}
		time.Sleep(2 * time.Millisecond)
	}
	waitFor(t, "the debounced restart", func() bool { return runner.startCount() == 2 })

	// Nothing further may happen once the burst has settled.
	time.Sleep(200 * time.Millisecond)
	if got := runner.startCount(); got != 2 {
		t.Fatalf("start count = %d, want 2: the burst was not debounced into one restart", got)
	}
	if got := log.count(EventRestarting); got != 1 {
		t.Fatalf("restarting events = %d, want 1", got)
	}
	if !runner.process(0).wasStopped() {
		t.Fatal("the replaced child was not stopped")
	}
}

func TestSupervisorRestartsOncePerSettledChange(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	changes := make(chan struct{}, 1)
	s := &Supervisor{
		Name:     "app",
		Runner:   runner,
		Spec:     func() Spec { return Spec{Name: "app", Argv: []string{"bin"}} },
		Debounce: 20 * time.Millisecond,
	}
	runSupervisor(t, s, changes)
	waitFor(t, "the first start", func() bool { return runner.startCount() == 1 })

	for want := 2; want <= 4; want++ {
		changes <- struct{}{}
		waitFor(t, "a restart", func() bool { return runner.startCount() == want })
	}
}

// TestSupervisorKeepsTheLastGoodBinaryRunningWhenTheBuildFails is the
// behavior that makes the loop usable: a compiler error must not take the
// application down, because the developer is usually still clicking around
// it while they read the error.
func TestSupervisorKeepsTheLastGoodBinaryRunningWhenTheBuildFails(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	log := &eventLog{}
	changes := make(chan struct{}, 1)

	var mu sync.Mutex
	buildFails := false
	s := &Supervisor{
		Name:   "app",
		Runner: runner,
		Spec:   func() Spec { return Spec{Name: "app", Argv: []string{"bin"}} },
		Build: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			if buildFails {
				return errors.New("./internal/app/app.go:12:2: undefined: Foo")
			}
			return nil
		},
		Debounce: 20 * time.Millisecond,
		OnEvent:  log.record,
	}
	runSupervisor(t, s, changes)
	waitFor(t, "the first start", func() bool { return runner.startCount() == 1 })

	mu.Lock()
	buildFails = true
	mu.Unlock()
	changes <- struct{}{}
	waitFor(t, "the failed build to be reported", func() bool { return log.count(EventBuildFailed) == 1 })

	if got := runner.startCount(); got != 1 {
		t.Fatalf("start count = %d, want 1: a failing build must not start a new child", got)
	}
	if runner.process(0).wasStopped() {
		t.Fatal("a failing build stopped the last good binary; the application should stay up while the developer reads the error")
	}

	// Fixing the code brings it back.
	mu.Lock()
	buildFails = false
	mu.Unlock()
	changes <- struct{}{}
	waitFor(t, "the recovery restart", func() bool { return runner.startCount() == 2 })
}

// TestSupervisorReportsAChildThatCrashesAndDoesNotSpin covers a child that
// exits on its own: report it, then wait for a change rather than
// restarting into the same permanent failure at full speed.
func TestSupervisorReportsAChildThatCrashesAndDoesNotSpin(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	log := &eventLog{}
	changes := make(chan struct{}, 1)
	s := &Supervisor{
		Name:     "app",
		Runner:   runner,
		Spec:     func() Spec { return Spec{Name: "app", Argv: []string{"bin"}} },
		Debounce: 20 * time.Millisecond,
		OnEvent:  log.record,
	}
	runSupervisor(t, s, changes)
	waitFor(t, "the first start", func() bool { return runner.startCount() == 1 })

	runner.process(0).exit(errors.New("listen tcp 127.0.0.1:8081: address already in use"))
	waitFor(t, "the exit to be reported", func() bool { return log.count(EventExited) == 1 })

	time.Sleep(150 * time.Millisecond)
	if got := runner.startCount(); got != 1 {
		t.Fatalf("start count = %d, want 1: the supervisor restarted a crashed child in a loop", got)
	}

	// A source change is what brings it back.
	changes <- struct{}{}
	waitFor(t, "the restart after a change", func() bool { return runner.startCount() == 2 })
}

func TestSupervisorRebuildsTheSpecOnEveryStart(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	changes := make(chan struct{}, 1)
	generation := 0
	s := &Supervisor{
		Name:   "app",
		Runner: runner,
		Spec: func() Spec {
			generation++
			return Spec{Name: "app", Argv: []string{"bin", "--generation", strconv.Itoa(generation)}}
		},
		Debounce: 20 * time.Millisecond,
	}
	runSupervisor(t, s, changes)
	waitFor(t, "the first start", func() bool { return runner.startCount() == 1 })
	changes <- struct{}{}
	waitFor(t, "the restart", func() bool { return runner.startCount() == 2 })

	if got := runner.lastSpec().String(); got != "bin --generation 2" {
		t.Fatalf("restarted with %q; Spec was not re-evaluated", got)
	}
}

func TestSupervisorRefusesToRunWithoutItsDependencies(t *testing.T) {
	t.Parallel()
	for _, s := range []*Supervisor{
		{Name: "app", Spec: func() Spec { return Spec{} }},
		{Name: "app", Runner: &fakeRunner{}},
	} {
		if err := s.Run(context.Background(), nil); err == nil {
			t.Fatal("Run accepted a supervisor with a missing dependency")
		}
	}
}

func TestSupervisorReturnsAStartFailure(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{startErr: errors.New("exec: \"bin\": executable file not found in $PATH")}
	s := &Supervisor{
		Name:   "app",
		Runner: runner,
		Spec:   func() Spec { return Spec{Name: "app", Argv: []string{"bin"}} },
	}
	err := s.Run(context.Background(), make(chan struct{}))
	if err == nil {
		t.Fatal("Run returned nil when the child could not be started at all")
	}
	if !errors.Is(err, runner.startErr) {
		t.Fatalf("error %v does not wrap the underlying start failure", err)
	}
}
