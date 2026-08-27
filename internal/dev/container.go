package dev

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Engine identifies which container engine a binary on PATH actually is.
// It is deliberately not inferred from the binary's name: on a great many
// machines /usr/bin/docker is a Podman shim, and code that assumes
// otherwise builds arguments the real engine rejects.
type Engine string

const (
	// EngineDocker is Docker Engine.
	EngineDocker Engine = "docker"
	// EnginePodman is Podman.
	EnginePodman Engine = "podman"
)

// Runtime is a usable container runtime: which engine it is, which binary
// invokes it, and the version string it reported.
type Runtime struct {
	// Engine is the engine the binary actually is.
	Engine Engine
	// Command is the binary name or path to invoke.
	Command string
	// Version is the raw first line of `<command> --version`, for display.
	Version string
}

// String renders the runtime for a startup line, e.g.
// `podman (docker) 5.8.4` when the docker binary is a Podman shim.
func (r Runtime) String() string {
	if r.Command != string(r.Engine) {
		return fmt.Sprintf("%s (via %q) %s", r.Engine, r.Command, r.Version)
	}
	return fmt.Sprintf("%s %s", r.Engine, r.Version)
}

// Exec is the seam over os/exec that everything in this package which
// shells out goes through, so container and process behavior is testable
// without a container runtime or a real child process. SystemExec is the
// production implementation.
type Exec interface {
	// Look resolves name on PATH, returning the same errors exec.LookPath
	// returns.
	Look(name string) (string, error)
	// Output runs name with args and returns its combined stdout. A
	// non-zero exit is an error whose message includes the child's stderr.
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	// Run runs name with args, discarding stdout. A non-zero exit is an
	// error whose message includes the child's stderr.
	Run(ctx context.Context, name string, args ...string) error
}

// SystemExec is the production Exec: it really resolves PATH and really
// runs processes.
type SystemExec struct{}

// Look implements Exec.
func (SystemExec) Look(name string) (string, error) { return exec.LookPath(name) }

// Output implements Exec.
func (SystemExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	// #nosec G204 -- name is always a container-runtime binary this package
	// resolved through exec.LookPath, and args are built by this package's
	// own argv builders from validated project identifiers (see slug) and
	// fixed literals. No element reaches here from unvalidated input.
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return out, annotateExitError(name, args, err)
	}
	return out, nil
}

// Run implements Exec.
func (SystemExec) Run(ctx context.Context, name string, args ...string) error {
	_, err := SystemExec{}.Output(ctx, name, args...)
	return err
}

// annotateExitError folds a child's stderr into the returned error, because
// "exit status 125" on its own tells a developer nothing.
func annotateExitError(name string, args []string, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		stderr := strings.TrimSpace(string(ee.Stderr))
		if stderr != "" {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, stderr)
		}
	}
	return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}

// ErrNoContainerRuntime reports that neither Docker nor Podman is usable.
// The CLI turns it into a precondition error naming --no-db.
var ErrNoContainerRuntime = errors.New("dev: no usable container runtime found")

// runtimeCandidates is the order DetectRuntime probes binaries in. Docker
// is probed first only because it is the more common spelling, not because
// it is assumed to be Docker: whichever binary answers, its engine is read
// from what it reports about itself.
var runtimeCandidates = []string{"docker", "podman"}

// DetectRuntime finds a container runtime and confirms it is actually
// usable, not merely installed.
//
// preferred selects one candidate by name ("docker" or "podman"); "" and
// "auto" probe both. Detection is two steps on purpose:
//
//  1. `<binary> --version` decides which engine it is. A binary named
//     docker that answers "podman version 5.8.4" is Podman, and is treated
//     as Podman everywhere afterwards.
//  2. `<binary> info` decides whether it works. Docker's client is happy to
//     exist with no daemon behind it, and a dev loop that discovers that
//     only when it tries to start PostgreSQL reports the failure three
//     steps away from its cause.
//
// The returned error accumulates what each candidate said, so the message a
// developer reads names the actual obstacle (not installed / daemon not
// running) rather than a bare "not found".
func DetectRuntime(ctx context.Context, ex Exec, preferred string) (Runtime, error) {
	candidates := runtimeCandidates
	switch strings.ToLower(strings.TrimSpace(preferred)) {
	case "", "auto":
	case "docker":
		candidates = []string{"docker"}
	case "podman":
		candidates = []string{"podman"}
	default:
		return Runtime{}, fmt.Errorf("dev: unknown container runtime %q; use docker, podman, or auto", preferred)
	}

	reasons := make([]string, 0, len(candidates))
	for _, name := range candidates {
		if _, err := ex.Look(name); err != nil {
			reasons = append(reasons, name+": not on PATH")
			continue
		}
		out, err := ex.Output(ctx, name, "--version")
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%s: --version failed: %v", name, err))
			continue
		}
		version := firstLine(string(out))
		engine, ok := identifyEngine(version)
		if !ok {
			reasons = append(reasons, fmt.Sprintf("%s: unrecognized engine in %q", name, version))
			continue
		}
		if err := ex.Run(ctx, name, "info"); err != nil {
			reasons = append(reasons, fmt.Sprintf("%s: installed but not usable: %v", name, err))
			continue
		}
		return Runtime{Engine: engine, Command: name, Version: versionNumber(version)}, nil
	}
	return Runtime{}, fmt.Errorf("%w (%s)", ErrNoContainerRuntime, strings.Join(reasons, "; "))
}

// identifyEngine reads the engine out of a `--version` line. Docker prints
// "Docker version 29.6.2, build dfc4efb"; Podman prints "podman version
// 5.8.4". Podman is checked first so that a shim which mentions both names
// is classified as the engine that will actually execute the command.
func identifyEngine(version string) (Engine, bool) {
	lower := strings.ToLower(version)
	switch {
	case strings.Contains(lower, "podman"):
		return EnginePodman, true
	case strings.Contains(lower, "docker"):
		return EngineDocker, true
	default:
		return "", false
	}
}

// versionNumber extracts the bare version from a `--version` line, falling
// back to the whole line when the shape is unfamiliar.
func versionNumber(version string) string {
	fields := strings.Fields(version)
	for i, f := range fields {
		if strings.EqualFold(f, "version") && i+1 < len(fields) {
			return strings.TrimSuffix(fields[i+1], ",")
		}
	}
	return version
}

// firstLine returns s up to the first newline, trimmed.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
