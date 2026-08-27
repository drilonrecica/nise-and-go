package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds how long any single doctor check waits for an
// external tool to print its version and exit. A tool that has not
// answered by then is treated as a failed check, not an indefinitely
// blocked `nise doctor` — see Runner.Run and TestExecRunnerTimeout.
const DefaultTimeout = 10 * time.Second

// maxCapturedOutput bounds how much of a tool's stdout/stderr Runner.Run
// keeps. Any real version banner is a handful of lines; this exists so a
// tool that is confused about what flag it was given (and dumps a large
// help page, or worse, echoes something unbounded) cannot make a doctor
// check hold an unbounded amount of memory.
const maxCapturedOutput = 16 * 1024

// Runner executes a local command and returns its combined output. It is
// an interface, rather than a bare function, so a test can substitute a
// fake that returns canned tool output without needing the real binary
// installed — every check in this package (tools.go, doctor.go) takes a
// Runner rather than calling exec.Command directly.
type Runner interface {
	// Run executes name with args, waits at most timeout for it to
	// finish, and returns its combined stdout+stderr, bounded to
	// maxCapturedOutput. It never starts a shell and never interpolates
	// name or args into a shell command line — name and args are passed
	// directly to the OS as an argv vector, exactly as
	// os/exec.CommandContext defines it, so no argument value (from
	// whatever source) can be interpreted as shell syntax.
	Run(ctx context.Context, timeout time.Duration, name string, args ...string) (output string, err error)
}

// execRunner is the real Runner, backed by os/exec.
type execRunner struct{}

// NewRunner returns the production Runner: real processes, via
// os/exec.CommandContext.
func NewRunner() Runner {
	return execRunner{}
}

// Run implements Runner. name and args always come from this package's own
// check definitions (tools.go) — never from a command-line flag, a file, or
// any other value an invoking user or attacker could shape — so there is
// no shell-injection surface here despite the dynamic name/args: gosec's
// G204 ("subprocess launched with variable") exists to catch exactly the
// case this is not, a path built from untrusted input reaching
// exec.Command. See docs/cli-output.md and constraints.md §4: doctor is
// documented to run only fixed local executables, never anything shaped by
// user input.
func (execRunner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- name/args are fixed literals from this package's own
	// check definitions (see tools.go), never derived from CLI flags,
	// files, or any other externally influenced value.
	cmd := exec.CommandContext(runCtx, name, args...)
	buf := &boundedBuffer{limit: maxCapturedOutput}
	cmd.Stdout = buf
	cmd.Stderr = buf

	runErr := cmd.Run()
	out := buf.String()

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("%s did not respond within %s", name, timeout)
	}
	if runErr != nil {
		return out, fmt.Errorf("run %q: %w", strings.TrimSpace(name+" "+strings.Join(args, " ")), runErr)
	}
	return out, nil
}

// boundedBuffer is an io.Writer that keeps at most limit bytes of
// everything written to it, silently discarding the rest. Write always
// reports success for the discarded portion too (never returning an
// error), because a command whose Stdout/Stderr write fails is treated by
// os/exec as the command itself having failed — which would turn "the
// tool printed more than we wanted to keep" into a false failure report
// for what might otherwise be a perfectly healthy tool.
type boundedBuffer struct {
	limit int
	buf   bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return b.buf.String()
}
