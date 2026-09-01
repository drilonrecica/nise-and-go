package doctor

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// spawnTimeout is the budget the tests below give a real process to start,
// write a line, and exit. It is deliberately far larger than any of them
// needs, because it is not what they are measuring -- TestExecRunnerTimeout
// owns the timeout behaviour and passes its own short budget.
//
// One second was the original value and it was not enough. On the Windows
// runner `sh` resolves to Git for Windows' sh.exe, whose process startup
// alone can exceed a second, so TestExecRunnerCapturesStderr failed with "sh
// did not respond within 1s" on a runner where stderr capture was working
// fine. A success-path assertion should fail because the behaviour is wrong,
// not because the slowest supported platform is slow.
const spawnTimeout = 30 * time.Second

// requireUnixTool skips the test when name is not on PATH, so this file
// stays safe to run on a machine (or a future CI image) that lacks the
// coreutils this test relies on, rather than failing for an unrelated
// reason.
func requireUnixTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not on PATH: %v", name, err)
	}
}

func TestExecRunnerSuccess(t *testing.T) {
	t.Parallel()
	requireUnixTool(t, "echo")

	r := NewRunner()
	out, err := r.Run(context.Background(), spawnTimeout, "echo", "hello", "doctor")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got, want := strings.TrimSpace(out), "hello doctor"; got != want {
		t.Errorf("Run output = %q, want %q", got, want)
	}
}

// TestExecRunnerCapturesStderr proves Runner.Run's shared stdout/stderr
// buffer (see execRunner.Run's cmd.Stdout = buf; cmd.Stderr = buf) actually
// captures output a tool sends to stderr, not just stdout. This matters in
// production: at least one real tool doctor checks could plausibly print
// its version banner to stderr rather than stdout on some platform, and
// ParseVersion is run against whatever Run returns regardless of which
// stream it came from. Nothing else in this suite pins this behavior, so a
// future refactor that accidentally dropped or discarded cmd.Stderr would
// not be caught by TestExecRunnerSuccess (which only ever writes to
// stdout).
func TestExecRunnerCapturesStderr(t *testing.T) {
	t.Parallel()
	requireUnixTool(t, "sh")

	r := NewRunner()
	out, err := r.Run(context.Background(), spawnTimeout, "sh", "-c", "echo to-stderr 1>&2")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got, want := strings.TrimSpace(out), "to-stderr"; got != want {
		t.Errorf("Run output = %q, want %q (stderr must be captured, not discarded)", got, want)
	}
}

func TestExecRunnerCommandNotFound(t *testing.T) {
	t.Parallel()
	r := NewRunner()
	_, err := r.Run(context.Background(), time.Second, "nise-doctor-definitely-not-a-real-binary")
	if err == nil {
		t.Fatal("Run returned nil error for a nonexistent binary, want an error")
	}
}

// TestExecRunnerTimeout proves a wedged binary cannot hang `nise doctor`
// forever: a real `sleep 5` process, given a much shorter timeout, must be
// killed and Run must return promptly with a timeout error rather than
// blocking for the full 5 seconds.
func TestExecRunnerTimeout(t *testing.T) {
	t.Parallel()
	requireUnixTool(t, "sleep")

	r := NewRunner()
	start := time.Now()
	_, err := r.Run(context.Background(), 100*time.Millisecond, "sleep", "5")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run returned nil error for a command that exceeded its timeout")
	}
	if !strings.Contains(err.Error(), "did not respond within") {
		t.Errorf("Run error = %q, want it to name the timeout", err)
	}
	// Generous upper bound: the process must be killed well before its
	// own 5-second sleep would otherwise return, proving the timeout
	// actually terminated it rather than Run merely returning early while
	// leaving the child running.
	if elapsed > 4*time.Second {
		t.Errorf("Run took %s to return after a 100ms timeout; the child process was not killed promptly", elapsed)
	}
}

// TestExecRunnerBoundsCapturedOutput proves a tool that never stops
// writing cannot make a doctor check hold an unbounded amount of memory:
// `yes` writes "y\n" forever until killed, so combined with a short
// timeout this both exercises the timeout path and proves the captured
// output never exceeds maxCapturedOutput.
func TestExecRunnerBoundsCapturedOutput(t *testing.T) {
	t.Parallel()
	requireUnixTool(t, "yes")

	r := NewRunner()
	out, err := r.Run(context.Background(), 150*time.Millisecond, "yes")
	if err == nil {
		t.Fatal("Run returned nil error for a command killed by timeout")
	}
	if len(out) > maxCapturedOutput {
		t.Errorf("captured output length = %d, want <= %d", len(out), maxCapturedOutput)
	}
	if len(out) == 0 {
		t.Error("captured output is empty; expected at least some of yes's output before it was killed")
	}
}

func TestBoundedBufferDiscardsBeyondLimit(t *testing.T) {
	t.Parallel()
	b := &boundedBuffer{limit: 5}
	n, err := b.Write([]byte("hello world"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != len("hello world") {
		t.Errorf("Write returned n=%d, want %d (short writes must never be reported)", n, len("hello world"))
	}
	if got, want := b.String(), "hello"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	n2, err := b.Write([]byte(" more"))
	if err != nil {
		t.Fatalf("second Write returned error: %v", err)
	}
	if n2 != len(" more") {
		t.Errorf("second Write returned n=%d, want %d", n2, len(" more"))
	}
	if got, want := b.String(), "hello"; got != want {
		t.Errorf("String() after over-limit write = %q, want unchanged %q", got, want)
	}
}
