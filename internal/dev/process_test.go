//go:build unix

package dev

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestOSRunnerStopsTheWholeProcessGroup is the test behind the promise that
// Ctrl-C never orphans a child. The shell it starts forks a grandchild that
// would outlive a plain Process.Kill on the shell alone — the exact shape of
// `pnpm` forking `vite`, or `go run` forking the built binary.
func TestOSRunnerStopsTheWholeProcessGroup(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no POSIX shell available: %v", err)
	}
	dir := t.TempDir()
	pidFile := dir + "/grandchild.pid"

	var out strings.Builder
	p, err := OSRunner{}.Start(Spec{
		Name: "test",
		Argv: []string{sh, "-c",
			// The grandchild ignores SIGTERM addressed to it alone, so only
			// a group signal followed by the kill escalation ends it.
			`(trap "" TERM; sleep 30) & echo $! > ` + pidFile + `; wait`},
		Stdout: &out,
		Stderr: &out,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	go func() { _ = p.Wait() }()

	var grandchild int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile) // #nosec G304 -- a path this test built under t.TempDir().
		if readErr == nil {
			if n, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && n > 0 {
				grandchild = n
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if grandchild == 0 {
		t.Fatalf("the grandchild never recorded its pid; output was %q", out.String())
	}
	if err := syscall.Kill(grandchild, 0); err != nil {
		t.Fatalf("the grandchild %d is not running before the stop: %v", grandchild, err)
	}

	// A short grace so the escalation to SIGKILL is actually exercised.
	if err := p.Stop(200 * time.Millisecond); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Stop returns only once the child is gone; the grandchild follows it
	// because the signal went to the whole group.
	gone := false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(grandchild, 0); err != nil {
			gone = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !gone {
		_ = syscall.Kill(grandchild, syscall.SIGKILL)
		t.Fatalf("the grandchild %d survived Stop; the process group was orphaned", grandchild)
	}
}

func TestOSRunnerRunsInItsOwnProcessGroup(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no POSIX shell available: %v", err)
	}
	var out strings.Builder
	p, err := OSRunner{}.Start(Spec{Name: "test", Argv: []string{sh, "-c", "sleep 5"}, Stdout: &out})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	go func() { _ = p.Wait() }()
	defer func() { _ = p.Stop(time.Second) }()

	pgid, err := syscall.Getpgid(p.Pid())
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	// A child in its own group is what keeps a terminal's Ctrl-C from
	// reaching it directly, so nise can shut everything down in order.
	if pgid != p.Pid() {
		t.Fatalf("child pgid = %d, want it to equal its own pid %d", pgid, p.Pid())
	}
	if pgid == syscall.Getpid() {
		t.Fatal("the child shares nise's process group; a terminal Ctrl-C would reach it directly")
	}
}

func TestOSRunnerStopOnAnAlreadyExitedChildIsNotAnError(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no POSIX shell available: %v", err)
	}
	p, err := OSRunner{}.Start(Spec{Name: "test", Argv: []string{sh, "-c", "exit 0"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = p.Wait() }()
	<-done
	if err := p.Stop(time.Second); err != nil {
		t.Fatalf("Stop on an exited child = %v, want nil", err)
	}
}

func TestOSRunnerRejectsAnEmptyArgv(t *testing.T) {
	t.Parallel()
	if _, err := (OSRunner{}).Start(Spec{Name: "test"}); err == nil {
		t.Fatal("Start accepted a spec with no executable")
	}
}

func TestSpecStringRendersTheCommand(t *testing.T) {
	t.Parallel()
	s := Spec{Argv: []string{"go", "build", "-o", "/tmp/x", "./cmd/demoapp"}}
	if got, want := s.String(), "go build -o /tmp/x ./cmd/demoapp"; got != want {
		t.Fatalf("Spec.String() = %q, want %q", got, want)
	}
}
