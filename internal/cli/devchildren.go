package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/dev"
)

// startDevDatabase brings the PostgreSQL container up and waits for it to
// accept connections. It returns (nil, zero, nil) when --no-db was given.
func startDevDatabase(ctx context.Context, env *Env, f devFlags, project devProject, pr devPrinter) (*dev.Postgres, dev.Runtime, error) {
	if f.noDB {
		return nil, dev.Runtime{}, nil
	}
	rt, err := dev.DetectRuntime(ctx, dev.SystemExec{}, f.runtime)
	if err != nil {
		if errors.Is(err, dev.ErrNoContainerRuntime) {
			return nil, dev.Runtime{}, clierr.Wrap(err, clierr.ExitPrecondition,
				"no usable container runtime was found",
				"Install Docker or Podman and start it, or pass --no-db to run without a database.").
				WithCode("dev.no_container_runtime").WithDocs(devDocs)
		}
		return nil, dev.Runtime{}, clierr.Wrap(err, clierr.ExitUsage, err.Error(),
			`Pass --container-runtime one of "auto", "docker", or "podman".`).WithDocs(devDocs)
	}
	pr.say(sourceDB, "using "+rt.String())

	pg := dev.NewPostgres(project.name, f.dbImage, "127.0.0.1", f.dbPort)
	if err := pg.Up(ctx, dev.SystemExec{}, rt, func(msg string) { pr.say(sourceDB, msg) }); err != nil {
		return nil, rt, clierr.Wrap(err, clierr.ExitPrecondition,
			"could not start the PostgreSQL container",
			fmt.Sprintf("Check that %s can run containers, or pass --no-db.", rt.Command)).
			WithCode("dev.database_start_failed").WithDocs(devDocs)
	}

	// A container left over from an earlier run keeps its original port
	// mapping, so the flag and the reality can disagree. Catch it here
	// rather than printing a connection string that names a port nothing
	// is listening on.
	if err := pg.VerifyPublishedPort(ctx, dev.SystemExec{}, rt); err != nil {
		if errors.Is(err, dev.ErrPortMismatch) {
			return nil, rt, clierr.Wrap(err, clierr.ExitPrecondition, err.Error(),
				fmt.Sprintf("Recreate it with %q (the data volume is kept), or pass --db-port to match.", pg.RemoveCommand(rt))).
				WithCode("dev.database_port_mismatch").WithDocs(devDocs)
		}
		return nil, rt, clierr.Wrap(err, clierr.ExitPrecondition,
			"could not read the database container's published port",
			fmt.Sprintf("Inspect it with: %s inspect %s", rt.Command, pg.ContainerName())).
			WithCode("dev.database_port_unknown").WithDocs(devDocs)
	}

	sp := env.Out.Spinner("waiting for PostgreSQL to accept connections")
	waitErr := pg.WaitReady(ctx, dev.SystemExec{}, rt, databaseReadyTimeout, 250*time.Millisecond)
	if waitErr != nil {
		sp.Stop("PostgreSQL did not become ready")
		return nil, rt, clierr.Wrap(waitErr, clierr.ExitPrecondition,
			"the PostgreSQL container did not become ready",
			fmt.Sprintf("Inspect it with: %s logs %s", rt.Command, pg.ContainerName())).
			WithCode("dev.database_not_ready").WithDocs(devDocs)
	}
	sp.Stop("PostgreSQL is ready")
	return &pg, rt, nil
}

// stopDevDatabase either stops the container or says plainly that it is
// still running and how to stop it.
//
// Leaving it running is the default because a development database
// accumulates state a developer paid for — seed data, a half-finished
// migration, a reproduction of a bug — and stopping it on every Ctrl-C
// makes the next start slower for no benefit. What makes that defensible
// rather than untidy is that the container is named on the way out, along
// with the exact command that stops it.
func stopDevDatabase(ctx context.Context, env *Env, f devFlags, pg *dev.Postgres, rt dev.Runtime, pr devPrinter) {
	if pg == nil {
		return
	}
	if !f.stopDB {
		pr.say(sourceDB, "container "+pg.ContainerName()+" is still running — stop it with: "+pg.StopCommand(rt))
		return
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := pg.Stop(stopCtx, dev.SystemExec{}, rt); err != nil {
		// A database that would not stop is worth saying out loud, but it
		// is not a reason to fail a shutdown that otherwise succeeded.
		pr.say(sourceDB, "could not stop the container: "+err.Error())
		return
	}
	pr.say(sourceDB, "stopped container "+pg.ContainerName())
}

// appSupervisor builds the supervisor for the Go child: `go build` into a
// scratch directory outside the project, then run the built binary.
//
// Building to a temporary directory rather than into the project's own
// bin/ keeps `nise dev` from leaving an artifact behind in a tree the
// developer is about to commit, and running the built binary rather than
// `go run` is what makes the child killable: `go run` starts the real
// program as a grandchild, and stopping the wrapper leaves the server
// holding the port.
func appSupervisor(root, scratch string, project devProject, f devFlags, pg *dev.Postgres, color bool, pr devPrinter) *dev.Supervisor {
	binary := filepath.Join(scratch, project.name+binarySuffix())
	stdout := dev.NewPrefixWriter(sourceApp, !color, pr.raw)

	return &dev.Supervisor{
		Name:      sourceApp,
		Runner:    dev.OSRunner{},
		Debounce:  dev.DefaultDebounce,
		StopGrace: time.Duration(f.graceMs) * time.Millisecond,
		Build: func(ctx context.Context) error {
			return runBuild(ctx, root, childEnv(f, pg, color), color, pr,
				[]string{"go", "build", "-o", binary, "./cmd/" + project.name})
		},
		Spec: func() dev.Spec {
			return dev.Spec{
				Name:   sourceApp,
				Argv:   []string{binary},
				Dir:    root,
				Env:    childEnv(f, pg, color),
				Stdout: stdout,
				Stderr: stdout,
			}
		},
		OnEvent: func(e dev.Event) { reportSupervisorEvent(pr, stdout, e) },
	}
}

// viteSupervisor builds the supervisor for the Vite child. It runs the
// project's own `dev` script rather than a command nise invented, and
// appends the address flags so nothing in frontend/package.json has to
// change for the proxy to know where Vite is.
//
// Both flags are load-bearing, and both were added after watching them
// fail:
//
//   - --host 127.0.0.1. Vite's default host is "localhost", which on a
//     dual-stack machine binds [::1] only. The proxy dials 127.0.0.1, so
//     without this every document request 502s while Vite's own log
//     cheerfully reports "ready".
//   - --strictPort. Without it Vite silently moves to the next free port
//     when the chosen one is taken, and the proxy then forwards every
//     document request into a connection refused with no explanation.
//
// The flags are appended without a preceding "--". pnpm forwards a literal
// "--" through to the script, and Vite's argument parser puts everything
// after it into a passthrough bucket it never reads — so the separator
// that looks like good hygiene is exactly what silently discards both
// flags above.
func viteSupervisor(root string, f devFlags, color bool, pr devPrinter) *dev.Supervisor {
	stdout := dev.NewPrefixWriter(sourceWeb, !color, pr.raw)
	return &dev.Supervisor{
		Name:      sourceWeb,
		Runner:    dev.OSRunner{},
		StopGrace: time.Duration(f.graceMs) * time.Millisecond,
		Spec: func() dev.Spec {
			return dev.Spec{
				Name: sourceWeb,
				Argv: []string{"pnpm", "--dir", "frontend", "run", "dev",
					"--host", "127.0.0.1", "--port", f.vitePort, "--strictPort"},
				Dir:    root,
				Env:    childEnv(f, nil, color),
				Stdout: stdout,
				Stderr: stdout,
			}
		},
		OnEvent: func(e dev.Event) { reportSupervisorEvent(pr, stdout, e) },
	}
}

// buildFrontendOnce runs the project's own frontend build, for --embedded
// mode. It is a one-shot: --embedded exists to exercise the production
// topology and the production Content Security Policy, and a frontend
// change in that mode means re-running nise dev, which the docs say.
func buildFrontendOnce(ctx context.Context, root string, color bool, pr devPrinter) error {
	pr.say(sourceWeb, "building the frontend into the Go embed (--embedded)")
	if err := runBuild(ctx, root, nil, color, pr,
		[]string{"pnpm", "--dir", "frontend", "run", "build"}); err != nil {
		return clierr.Wrap(err, clierr.ExitPrecondition,
			"the frontend build failed",
			`Run "pnpm --dir frontend install" and try again, or drop --embedded to use Vite.`).
			WithCode("dev.frontend_build_failed").WithDocs(devDocs)
	}
	return nil
}

// runBuild runs one build command to completion, streaming its output
// through the [dev] prefix so a compiler error is attributed to the step
// that produced it. A non-zero exit is returned as an error carrying the
// child's own message.
func runBuild(ctx context.Context, root string, environ []string, color bool, pr devPrinter, argv []string) error {
	out := dev.NewPrefixWriter(sourceDev, !color, pr.raw)
	defer out.Flush()

	// #nosec G204 -- argv[0] is one of this file's own fixed literals ("go"
	// or "pnpm"); the remaining elements are fixed literals plus a binary
	// path this command created under its own scratch directory and a
	// command name read from the project's own cmd/ directory listing.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = environ
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return nil
}

// childEnv is the environment a supervised child runs with.
//
// It is the parent's environment with a small, explicit overlay, and every
// entry in that overlay is listed in docs/commands/dev.md. Two things are
// deliberately *not* in it:
//
//   - TRUST_PROXY_HEADERS stays at its production default of false. The
//     development proxy does not set X-Request-Id or X-Correlation-Id, so
//     turning it on would only teach the application to believe a browser
//     that sent them — a development-only weakening of a production
//     default, which is exactly what this command is forbidden to do.
//   - DEBUG stays wherever the developer left it. `nise dev` chooses
//     nothing about the application's own behavior beyond where it listens
//     and what database it talks to.
func childEnv(f devFlags, pg *dev.Postgres, color bool) []string {
	overlay := map[string]string{
		"PORT":      appListenPort(f),
		"BIND_ADDR": "127.0.0.1",
	}
	if _, set := os.LookupEnv("APP_ENV"); !set {
		overlay["APP_ENV"] = "development"
	}
	if pg != nil {
		// The only place the real connection string exists. It is never
		// printed: the topology block shows Postgres.RedactedDSN instead.
		overlay["DATABASE_URL"] = pg.DSN()
	}
	if !color {
		// Ask the children not to color, rather than only stripping what
		// they produce. Vite colors whenever it believes it is talking to
		// a terminal, and it is talking to a pipe held by nise.
		overlay["NO_COLOR"] = "1"
		overlay["FORCE_COLOR"] = "0"
	}

	return overlayEnvironment(os.Environ(), overlay)
}

// overlayEnvironment returns base with each named value replaced exactly once.
// Sorting the overlay also makes command construction deterministic in tests.
func overlayEnvironment(base []string, overlay map[string]string) []string {
	out := make([]string, 0, len(base)+len(overlay))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if _, replaced := overlay[name]; !replaced {
			out = append(out, kv)
		}
	}
	for _, name := range sortedKeys(overlay) {
		out = append(out, name+"="+overlay[name])
	}
	return out
}

// sortedKeys returns m's keys in sorted order, so a child's environment is
// assembled deterministically.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// reportSupervisorEvent renders one supervisor event as a [dev] line.
func reportSupervisorEvent(pr devPrinter, out *dev.PrefixWriter, e dev.Event) {
	switch e.Kind {
	case dev.EventBuilding:
		pr.say(sourceDev, "building "+e.Name)
	case dev.EventBuildFailed:
		out.Flush()
		pr.say(sourceDev, "build failed — keeping the running "+e.Name+"; fix the error above and save again")
	case dev.EventStarted:
		pr.say(sourceDev, fmt.Sprintf("started %s (pid %d)", e.Name, e.Pid))
	case dev.EventRestarting:
		pr.say(sourceDev, "change detected — restarting "+e.Name)
	case dev.EventExited:
		out.Flush()
		if e.Err != nil {
			pr.say(sourceDev, fmt.Sprintf("%s exited: %v — waiting for the next change", e.Name, e.Err))
			return
		}
		pr.say(sourceDev, e.Name+" exited — waiting for the next change")
	case dev.EventStopped:
		out.Flush()
		if e.Err != nil {
			pr.say(sourceDev, fmt.Sprintf("stopping %s: %v", e.Name, e.Err))
			return
		}
		pr.say(sourceDev, fmt.Sprintf("stopped %s (pid %d)", e.Name, e.Pid))
	}
}

// binarySuffix is ".exe" on Windows and empty elsewhere.
func binarySuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
