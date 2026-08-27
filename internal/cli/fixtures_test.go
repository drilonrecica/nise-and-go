package cli

import (
	"context"
	"flag"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
)

// This file's fixtures exist to test dispatch (nesting, flags, help, error
// rendering) without touching the real registry in registry.go — see
// resolve.go's and dispatch.go's doc comments for why Commands() stays the
// single, minimal, production source of truth. M10 needs a nested command
// (nise db migrate); this fixture proves the nesting works before any real
// nested command exists.

func emptyGetenv(string) string { return "" }

// fixtureTree returns a small command tree: a "db" group with two nested
// subcommands (mirroring the planned "nise db migrate"/"nise db backup"
// shape) and a top-level leaf with its own flag.
func fixtureTree() []*Command {
	return []*Command{
		{
			Name:  "db",
			Short: "database commands",
			Subcommands: []*Command{
				dbMigrateCommand(),
				dbBackupCommand(),
			},
		},
		pingCommand(),
		failCommand(),
		echoArgsCommand(),
		greetCommand(),
	}
}

// lineResult is a minimal output.Result fixture. It marshals as a JSON
// object (not a bare string) because that's the shape a real command's
// Result should take — an object leaves room to add fields later without
// changing the JSON envelope's outer shape.
type lineResult struct {
	Message string `json:"message"`
}

func line(s string) lineResult { return lineResult{Message: s} }

func (l lineResult) Human() string { return l.Message }

func dbMigrateCommand() *Command {
	var dryRun bool
	return &Command{
		Name:  "migrate",
		Short: "run pending migrations",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
			fs.BoolVar(&dryRun, "dry-run", false, "report what would run without applying it")
			return fs
		},
		Run: func(_ context.Context, env *Env) error {
			if dryRun {
				env.Out.Result(line("dry-run: migrated"))
				return nil
			}
			env.Out.Result(line("migrated"))
			return nil
		},
	}
}

func dbBackupCommand() *Command {
	return &Command{
		Name:  "backup",
		Short: "back up the database",
		Run: func(_ context.Context, env *Env) error {
			env.Out.Result(line("backed up"))
			return nil
		},
	}
}

func pingCommand() *Command {
	return &Command{
		Name:  "ping",
		Short: "trivial leaf command",
		Run: func(_ context.Context, env *Env) error {
			env.Out.Result(line("pong"))
			return nil
		},
	}
}

// failCommand always returns a *clierr.Error, for exercising error
// rendering and exit codes end to end through Execute. Its own flag
// selects which exit-code class to return.
func failCommand() *Command {
	var kind string
	return &Command{
		Name:  "fail",
		Short: "always fails, for testing error rendering",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("fail", flag.ContinueOnError)
			fs.StringVar(&kind, "kind", "generic", "generic|precondition|plain")
			return fs
		},
		Run: func(_ context.Context, _ *Env) error {
			switch kind {
			case "precondition":
				return clierr.Precondition("missing tool", "install it")
			case "plain":
				return errPlainFixture
			default:
				return clierr.New(clierr.ExitError, "it failed", "try again")
			}
		},
	}
}

var errPlainFixture = plainFixtureError("boom")

type plainFixtureError string

func (e plainFixtureError) Error() string { return string(e) }

// echoArgsCommand echoes its own positional arguments (env.Args, i.e.
// whatever survived global-flag extraction and its own — empty — flag
// set) back as its Result, joined by "|". It exists to prove what a
// command actually receives after a "--" token: see
// TestExecuteDoubleDashStopsGlobalFlagExtraction.
func echoArgsCommand() *Command {
	return &Command{
		Name:  "echoargs",
		Short: "echo positional arguments, for testing -- passthrough",
		Run: func(_ context.Context, env *Env) error {
			env.Out.Result(line(strings.Join(env.Args, "|")))
			return nil
		},
	}
}

// greetCommand defines one value-consuming flag, --name, with no default
// collision with any reserved global flag name. It exists to prove that a
// value happening to look like a reserved spelling (e.g. "--json") is
// preserved as --name's literal value rather than stolen as a global flag
// — see TestExecuteValueFlagArgumentIsNotStolenByGlobalExtraction.
func greetCommand() *Command {
	var name string
	return &Command{
		Name:  "greet",
		Short: "print a greeting, for testing value-flag argument stealing",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("greet", flag.ContinueOnError)
			fs.StringVar(&name, "name", "", "who to greet")
			return fs
		},
		Run: func(_ context.Context, env *Env) error {
			env.Out.Result(line("hello " + name))
			return nil
		},
	}
}

// shadowCommand defines its own "--quiet" flag, deliberately colliding
// with the reserved global flag of the same name. It exists to prove
// dispatch refuses to run a command shaped like this, rather than
// silently letting the global flag win — see
// TestExecuteReservedFlagCollisionIsALoudFailure. It is NOT part of
// fixtureTree()'s default set (a broken command has no business among the
// "everything works" fixtures); tests that need it build a small
// one-command tree directly.
func shadowCommand() *Command {
	var quiet bool
	return &Command{
		Name:  "shadow",
		Short: "defines a colliding --quiet flag, for testing collision detection",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("shadow", flag.ContinueOnError)
			fs.BoolVar(&quiet, "quiet", false, "this collides with the global --quiet on purpose")
			return fs
		},
		Run: func(_ context.Context, env *Env) error {
			env.Out.Result(line("quiet=" + boolString(quiet)))
			return nil
		},
	}
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
