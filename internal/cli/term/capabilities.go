package term

import "os"

// FileStat is the subset of *os.File that Detect needs to decide whether a
// stream is an interactive terminal. os.Stdout and os.Stderr both satisfy it
// directly; tests satisfy it with a fake so detection never needs a real
// TTY.
type FileStat interface {
	Stat() (os.FileInfo, error)
}

// Capabilities is the immutable result of detecting the current process's
// terminal environment. It is computed once, in Detect, and then passed
// explicitly to whatever needs it (never stored in a package-level
// variable), so that command dispatch and the output layer are both
// testable by construction rather than by monkey-patching the environment.
type Capabilities struct {
	// StdoutIsTerminal reports whether stdout is an interactive terminal
	// (a character device), as opposed to a file, pipe, or redirect.
	StdoutIsTerminal bool
	// StderrIsTerminal reports the same for stderr.
	StderrIsTerminal bool
	// NoColor reports whether the NO_COLOR environment variable is set to
	// any non-empty value. Per the informal standard
	// (https://no-color.org/), presence — not content — disables color.
	NoColor bool
	// TermDumb reports whether TERM is exactly "dumb", the traditional
	// signal for a terminal with no editing or color capability.
	TermDumb bool
	// CLIColorEnabled reports whether CLICOLOR is set to a value other
	// than "0" (and non-empty). See CLIColorDisabled for the other half
	// of this tri-state: CLICOLOR being unset and CLICOLOR being set to
	// exactly "0" both leave CLIColorEnabled false, so a single bool
	// cannot tell them apart — that ambiguity is exactly what let
	// CLICOLOR=0 silently fail to disable color before this field
	// existed (ColorEnabled's "otherwise → true" default fired for both
	// cases identically).
	CLIColorEnabled bool
	// CLIColorDisabled reports whether CLICOLOR is set to exactly "0",
	// the other half of the CLICOLOR tri-state (unset / "0" / other).
	CLIColorDisabled bool
	// CLIColorForce reports whether CLICOLOR_FORCE is set to a non-empty
	// value other than "0". Per the informal convention, this forces color
	// on even when output is not a terminal.
	CLIColorForce bool
	// CI reports whether the CI environment variable is set to any
	// non-empty value, the de facto convention CI providers use to signal
	// a non-interactive environment. It disables animation.
	CI bool
}

// ColorEnabled reports the default color decision from detected
// capabilities alone, before any explicit --no-color flag is applied by the
// caller. Precedence, highest first:
//
//  1. CLICOLOR_FORCE set (non-empty, not "0") → true, even off a terminal.
//  2. NO_COLOR set (any non-empty value) → false.
//  3. TERM=dumb → false.
//  4. stdout is not a terminal → false.
//  5. CLICOLOR set to "0" → false.
//  6. CLICOLOR set to any other non-empty value → true.
//  7. otherwise (CLICOLOR unset) → true.
//
// Callers that also support a --no-color flag or a --json mode must apply
// those on top of this result themselves; ColorEnabled only reports what the
// environment says.
func (c Capabilities) ColorEnabled() bool {
	switch {
	case c.CLIColorForce:
		return true
	case c.NoColor:
		return false
	case c.TermDumb:
		return false
	case !c.StdoutIsTerminal:
		return false
	case c.CLIColorDisabled:
		return false
	case c.CLIColorEnabled:
		return true
	default:
		return true
	}
}

// AnimationEnabled reports whether spinners and banners are appropriate:
// stdout is an interactive terminal, the environment is not a known CI
// runner, and the terminal is not TERM=dumb. Callers must additionally
// suppress animation for --quiet and --json modes; AnimationEnabled reports
// only the environment's part of that decision.
func (c Capabilities) AnimationEnabled() bool {
	return c.StdoutIsTerminal && !c.CI && !c.TermDumb
}

// isSet reports whether an environment variable lookup returned a
// non-empty value, matching the NO_COLOR and CI informal-standard rule that
// presence (any value, including "false") is what matters, not content.
func isSet(v string) bool {
	return v != ""
}

// isTerminal reports whether f is a character device, the classic
// dependency-free way to distinguish an interactive terminal from a file,
// pipe, or redirect. A nil f (no stream available) is never a terminal.
//
// Verified: Linux and macOS, where a pty is reliably reported as a
// character device by Stat().Mode(). Not verified: native Windows consoles
// (cmd.exe, legacy conhost). Go's os.ModeCharDevice bit is set correctly by
// the Windows runtime for a real console handle in modern Go toolchains, but
// this package has not been exercised against one; ANSI processing on
// legacy Windows consoles (as opposed to Windows Terminal / ConPTY, which
// support ANSI natively) is a separate, unverified concern the output layer
// does not attempt to detect or enable.
func isTerminal(f FileStat) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Detect computes Capabilities from the given stdout/stderr streams and an
// environment lookup function. Both parameters are injected rather than
// read from os.Stdout/os.Stderr/os.Getenv directly, so every combination of
// terminal and environment state is reachable from a table-driven test
// without a real TTY or mutating the process environment.
//
// getenv is called with exactly the variable names this package cares
// about (NO_COLOR, TERM, CLICOLOR, CLICOLOR_FORCE, CI); passing os.Getenv
// reproduces normal process behavior.
func Detect(stdout, stderr FileStat, getenv func(string) string) Capabilities {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return Capabilities{
		StdoutIsTerminal: isTerminal(stdout),
		StderrIsTerminal: isTerminal(stderr),
		NoColor:          isSet(getenv("NO_COLOR")),
		TermDumb:         getenv("TERM") == "dumb",
		CLIColorEnabled:  isSet(getenv("CLICOLOR")) && getenv("CLICOLOR") != "0",
		CLIColorDisabled: getenv("CLICOLOR") == "0",
		CLIColorForce:    isSet(getenv("CLICOLOR_FORCE")) && getenv("CLICOLOR_FORCE") != "0",
		CI:               isSet(getenv("CI")),
	}
}
