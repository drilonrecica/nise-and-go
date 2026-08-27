package dev

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// EventKind labels what a Supervisor just did, so a caller can render it
// however its output mode requires without the supervisor knowing anything
// about terminals.
type EventKind string

const (
	// EventBuilding is emitted before the build step runs.
	EventBuilding EventKind = "building"
	// EventBuildFailed is emitted when the build step fails. The previous
	// child, if any, keeps running.
	EventBuildFailed EventKind = "build_failed"
	// EventStarted is emitted after the child starts.
	EventStarted EventKind = "started"
	// EventRestarting is emitted when a change has settled and the child
	// is about to be replaced.
	EventRestarting EventKind = "restarting"
	// EventExited is emitted when the child exits on its own.
	EventExited EventKind = "exited"
	// EventStopped is emitted after the supervisor stops the child on the
	// way out.
	EventStopped EventKind = "stopped"
)

// Event is one thing a Supervisor did.
type Event struct {
	// Kind is what happened.
	Kind EventKind
	// Name is the supervised child's name.
	Name string
	// Pid is the child's process id, where one exists.
	Pid int
	// Err is the failure, for EventBuildFailed and a non-clean EventExited.
	Err error
}

// Supervisor keeps one child process running and replaces it when a change
// arrives, after the change has stopped arriving.
//
// The debounce is the whole point of the design. An editor writing a Go
// file produces several filesystem events in a few milliseconds, a `git
// checkout` produces hundreds, and `go build` on a mid-sized tree takes
// long enough that starting one per event would leave the loop permanently
// behind the developer. So a change starts a timer, every further change
// resets it, and the rebuild happens once the tree has been quiet for
// Debounce.
type Supervisor struct {
	// Name is the child's short label, used in every Event.
	Name string
	// Runner starts the child. Required.
	Runner Runner
	// Spec builds the child's process spec. It is a function rather than a
	// value so a restart can pick up anything that changed between runs
	// (a rebuilt binary path, say). Required.
	Spec func() Spec
	// Build, when non-nil, runs before every start. A failing build does
	// not stop the currently running child: leaving the last good binary
	// serving is what lets a developer keep clicking around the
	// application while they read the compiler error.
	Build func(ctx context.Context) error
	// Debounce is how long the tree must be quiet before a restart. Zero
	// selects DefaultDebounce.
	Debounce time.Duration
	// StopGrace is how long a child gets to exit after SIGTERM before it
	// is killed. Zero selects DefaultStopGrace.
	StopGrace time.Duration
	// OnEvent, when non-nil, receives every Event, in order, from Run's
	// own goroutine.
	OnEvent func(Event)
}

// Debounce and stop-grace defaults. See docs/commands/dev.md for how the
// debounce interacts with the watcher's poll interval.
const (
	// DefaultDebounce is how long the tree must be quiet before a rebuild.
	DefaultDebounce = 250 * time.Millisecond
	// DefaultStopGrace is how long a child has to exit on SIGTERM.
	DefaultStopGrace = 5 * time.Second
)

// Run starts the child and supervises it until ctx is canceled, restarting
// it whenever changes delivers a value that settles.
//
// It always leaves no child behind: the deferred stop runs on every exit
// path, including a build failure at startup and a canceled context.
func (s *Supervisor) Run(ctx context.Context, changes <-chan struct{}) error {
	if s.Runner == nil || s.Spec == nil {
		return errors.New("dev: a supervisor needs a Runner and a Spec")
	}
	debounce := s.Debounce
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	grace := s.StopGrace
	if grace <= 0 {
		grace = DefaultStopGrace
	}

	var (
		current Process
		exited  chan error
	)
	stop := func() {
		if current == nil {
			return
		}
		if err := current.Stop(grace); err != nil {
			s.emit(Event{Kind: EventStopped, Name: s.Name, Pid: current.Pid(), Err: err})
		} else {
			s.emit(Event{Kind: EventStopped, Name: s.Name, Pid: current.Pid()})
		}
		current, exited = nil, nil
	}
	defer stop()

	// build reports whether the (optional) build step succeeded. A failure
	// is not fatal to the loop: the developer is going to fix it, and the
	// supervisor's job is to be there when they do.
	build := func() bool {
		if s.Build == nil {
			return true
		}
		s.emit(Event{Kind: EventBuilding, Name: s.Name})
		if err := s.Build(ctx); err != nil {
			s.emit(Event{Kind: EventBuildFailed, Name: s.Name, Err: err})
			return false
		}
		return true
	}

	spawn := func() error {
		p, err := s.Runner.Start(s.Spec())
		if err != nil {
			return fmt.Errorf("starting %s: %w", s.Name, err)
		}
		current = p
		done := make(chan error, 1)
		exited = done
		go func() { done <- p.Wait() }()
		s.emit(Event{Kind: EventStarted, Name: s.Name, Pid: p.Pid()})
		return nil
	}

	if build() {
		if err := spawn(); err != nil {
			return err
		}
	}

	// settle is nil whenever no change is pending. A non-nil settle is an
	// armed timer; every further change resets it.
	var settle *time.Timer
	var settleC <-chan time.Time
	disarm := func() {
		if settle != nil {
			settle.Stop()
			settle, settleC = nil, nil
		}
	}
	defer disarm()

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-changes:
			if settle == nil {
				settle = time.NewTimer(debounce)
				settleC = settle.C
				continue
			}
			// Reset only after a successful Stop; a timer that already
			// fired has drained into settleC and will be handled below.
			if settle.Stop() {
				settle.Reset(debounce)
			}

		case <-settleC:
			disarm()
			s.emit(Event{Kind: EventRestarting, Name: s.Name})
			// Build before stopping. A compiler error must leave the last
			// good binary serving: the developer is usually still clicking
			// around the application while they read the error, and taking
			// it down would make every failed build cost a page reload as
			// well as a fix.
			if !build() {
				continue
			}
			stop()
			if err := spawn(); err != nil {
				return err
			}

		case err := <-exited:
			// The child ended on its own: a panic, a configuration error,
			// a port already taken. Report it and keep supervising, so the
			// next source change restarts it. Restarting immediately would
			// spin on a permanent failure.
			pid := 0
			if current != nil {
				pid = current.Pid()
			}
			s.emit(Event{Kind: EventExited, Name: s.Name, Pid: pid, Err: err})
			current, exited = nil, nil
		}
	}
}

// emit delivers an event, if anybody is listening.
func (s *Supervisor) emit(e Event) {
	if s.OnEvent != nil {
		s.OnEvent(e)
	}
}
