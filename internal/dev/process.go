package dev

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// Spec describes a child process the dev loop supervises.
type Spec struct {
	// Name is the short source label that prefixes this child's output,
	// e.g. "app" or "web".
	Name string
	// Argv is the command and its arguments. Argv[0] is the executable.
	Argv []string
	// Dir is the working directory.
	Dir string
	// Env is the complete environment for the child. A nil Env inherits
	// the parent's, matching os/exec.
	Env []string
	// Stdout and Stderr receive the child's output. A nil writer discards.
	Stdout io.Writer
	Stderr io.Writer
}

// String renders the command for a startup line.
func (s Spec) String() string { return joinArgv(s.Argv) }

// Process is a started child. Every implementation must guarantee that
// Stop leaves nothing running: the dev loop's whole promise is that Ctrl-C
// ends every process it started, and a supervisor that orphans a server
// holding port 8081 makes the next run fail for a reason that has nothing
// to do with what the developer just changed.
type Process interface {
	// Wait blocks until the child exits and returns its exit error, or nil
	// for a clean exit. It is safe to call from one goroutine only.
	Wait() error
	// Stop asks the child's whole process group to terminate, escalating
	// to an unconditional kill if grace elapses first. It returns after
	// the child is gone.
	Stop(grace time.Duration) error
	// Pid is the child's process id, for a log line.
	Pid() int
}

// Runner starts child processes. The dev loop talks only to this
// interface, so the supervisor's restart and debounce logic is testable
// with a fake that starts nothing at all.
type Runner interface {
	Start(spec Spec) (Process, error)
}

// waitDelay bounds how long Wait may block after the child has exited.
//
// Wait does not return when the process dies; it returns when the pipes
// carrying the child's output are closed, and a grandchild inherits the write
// end of those pipes. On Unix that is covered — terminateGroup and killGroup
// signal the whole group, so the grandchildren die with their parent and the
// pipes close. On Windows there is no process group to signal: terminateGroup
// can only end the process it started, so a `go test` that spawned a test
// binary, or a `pnpm` that spawned a runner, leaves a live writer behind and
// Wait blocks for as long as that grandchild would have run anyway.
//
// This is the backstop for that case. When it elapses, os/exec closes the
// pipes itself and Wait returns exec.ErrWaitDelay. Output written by a
// grandchild after this point is lost, which is the right trade: the
// alternative is a Ctrl-C that appears to do nothing.
//
// It never fires on the Unix path, where the pipes are already closed by the
// time Wait looks.
//
// One second, and deliberately shorter than every grace period a caller
// passes to Stop (`nise dev` uses six, `nise test` two). Stop waits for the
// exit to be observed or for its grace to elapse, whichever comes first, so a
// backstop longer than the grace would make Stop take the escalation branch
// on a process that is already dead — a kill aimed at a pid that is only
// still there because Wait had not returned yet.
const waitDelay = time.Second

// OSRunner is the production Runner: it starts real processes, each in its
// own process group.
//
// Its own process group is the point. A Ctrl-C in the terminal delivers
// SIGINT to the foreground process group; putting each child in a group of
// its own means that signal reaches `nise dev` and nobody else, so shutdown
// is something nise performs deliberately and in order rather than a race
// between four processes that all received the same signal at once. It is
// also what makes Stop able to terminate a child's own grandchildren —
// a `pnpm` that forked `vite` — instead of leaving them behind.
type OSRunner struct{}

// Start implements Runner.
func (OSRunner) Start(spec Spec) (Process, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("dev: a process spec needs at least an executable")
	}
	// #nosec G204 -- Argv[0] is either a fixed literal from this package
	// ("go", "pnpm") or a path this package built itself under a temporary
	// directory it created; the arguments are this package's own literals
	// plus validated ports. Nothing here comes from unvalidated input.
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	cmd.WaitDelay = waitDelay
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", spec.Name, err)
	}
	return &osProcess{cmd: cmd, done: make(chan struct{})}, nil
}

// osProcess adapts *exec.Cmd to Process.
type osProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// Wait implements Process.
func (p *osProcess) Wait() error {
	p.err = p.cmd.Wait()
	close(p.done)
	return p.err
}

// Pid implements Process.
func (p *osProcess) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Stop implements Process: SIGTERM to the whole group, then SIGKILL to the
// whole group if grace elapses. It returns once Wait has observed the exit,
// so a caller that returns from Stop knows the port is free.
func (p *osProcess) Stop(grace time.Duration) error {
	if p.cmd.Process == nil {
		return nil
	}
	if err := terminateGroup(p.cmd.Process); err != nil && !isProcessGone(err) {
		return fmt.Errorf("terminating process group %d: %w", p.Pid(), err)
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(grace):
	}
	if err := killGroup(p.cmd.Process); err != nil && !isProcessGone(err) {
		return fmt.Errorf("killing process group %d: %w", p.Pid(), err)
	}
	<-p.done
	return nil
}

// joinArgv renders an argv for display, quoting nothing: every argv this
// package builds is made of its own literals, ports, and paths.
func joinArgv(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
