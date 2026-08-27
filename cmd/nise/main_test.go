package main

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/version"
)

// runCLI runs run with captured output and returns exit code, stdout, stderr.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	want := version.Get().String() + "\n"

	for _, spelling := range []string{"version", "--version"} {
		t.Run(spelling, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runCLI(t, spelling)
			if code != 0 {
				t.Errorf("run(%q) exit code = %d, want 0", spelling, code)
			}
			if stdout != want {
				t.Errorf("run(%q) stdout = %q, want %q", spelling, stdout, want)
			}
			if stderr != "" {
				t.Errorf("run(%q) stderr = %q, want empty", spelling, stderr)
			}
		})
	}
}

func TestRunVersionSpellingsAgree(t *testing.T) {
	t.Parallel()

	_, subcommand, _ := runCLI(t, "version")
	_, flag, _ := runCLI(t, "--version")
	if subcommand != flag {
		t.Errorf("output differs: version = %q, --version = %q", subcommand, flag)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCLI(t, "frobnicate")
	if code == 0 {
		t.Error("run(unknown command) exit code = 0, want non-zero")
	}
	if stdout != "" {
		t.Errorf("run(unknown command) stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "frobnicate") {
		t.Errorf("run(unknown command) stderr = %q, want it to name the command", stderr)
	}
}

func TestRunNoArguments(t *testing.T) {
	t.Parallel()

	// A bare "nise" is treated as a help request, not a usage error: it
	// prints the same root help as "nise help" and exits 0. See
	// internal/cli's dispatch tests for the full help-rendering contract;
	// this test only pins main's wiring of that behavior.
	code, stdout, stderr := runCLI(t)
	if code != 0 {
		t.Errorf("run(no args) exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("run(no args) stdout = %q, want it to contain usage help", stdout)
	}
	if stderr != "" {
		t.Errorf("run(no args) stderr = %q, want empty", stderr)
	}
}
