package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/cli/output"
	"github.com/drilonrecica/nise-and-go/internal/cli/term"
)

// Reserved global flag spellings. Every command's own flags must avoid
// these names — global flags are stripped from argv wherever they appear,
// before any command sees its arguments, so a command that tried to define
// e.g. its own "--json" would never see it typed.
const (
	flagJSON        = "--json"
	flagQuiet       = "--quiet"
	flagVerbose     = "--verbose"
	flagNoColor     = "--no-color"
	flagHelpLong    = "--help"
	flagHelpShort   = "-h"
	flagVersionLong = "--version"
)

// IO bundles everything Execute needs from the process environment,
// injected explicitly so the whole dispatcher is testable without a real
// terminal, a real environment, or a subprocess.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// StdoutStat and StderrStat back terminal-capability detection (see
	// internal/cli/term). os.Stdout and os.Stderr both satisfy
	// term.FileStat directly. Separate from Stdout/Stderr above because a
	// test commonly wants to capture output into a *bytes.Buffer (which
	// has no Stat method) while still controlling what capability
	// detection sees.
	StdoutStat term.FileStat
	StderrStat term.FileStat
	// Getenv looks up an environment variable; nil behaves as if every
	// variable were unset. Pass os.Getenv for normal process behavior.
	Getenv func(string) string
}

// Execute runs the nise CLI against args (os.Args[1:]) and returns the
// process exit code. It is the package's only entry point; cmd/nise/main.go
// does nothing but call this and os.Exit the result.
func Execute(args []string, ioc IO) int {
	return execute(args, ioc, Commands())
}

// execute is Execute's implementation, taking the command tree as a
// parameter so tests can dispatch against a small fixture tree (including
// a nested one) without touching the real registry.
func execute(args []string, ioc IO, root []*Command) int {
	getenv := ioc.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	caps := term.Detect(ioc.StdoutStat, ioc.StderrStat, getenv)

	rest, g := extractGlobalFlags(args)

	mode := output.ModeHuman
	if g.json {
		mode = output.ModeJSON
	}
	opts := output.Options{
		Quiet:   g.quiet,
		Verbose: g.verbose,
		Color:   mode == output.ModeHuman && resolveColor(caps, g.noColor),
		Animate: mode == output.ModeHuman && !g.quiet && caps.AnimationEnabled(),
	}
	out := output.New(mode, opts, ioc.Stdout, ioc.Stderr)

	if g.quiet && g.verbose {
		ce := clierr.Usage(
			`--quiet and --verbose cannot be used together`,
			`Choose one: drop "--quiet" or drop "--verbose".`,
		).WithDocs("docs/cli-output.md#output-modes")
		out.Error(ce)
		return int(ce.ExitCode())
	}

	// --version is a legacy alias for the "version" command, preserved
	// from Task 10's original entry point (its report explicitly calls
	// this the one behavior worth keeping byte-for-byte). It takes
	// precedence over an empty command line but not over --help, so
	// "nise --version --help" still shows help rather than the version.
	if g.version && !g.help {
		if vc := findByName(root, "version"); vc != nil && vc.Run != nil {
			return runLeaf(vc, nil, nil, out, caps, ioc.Stdin)
		}
	}

	if len(rest) == 0 {
		printHelp(out, rootHelp(root))
		return int(clierr.ExitOK)
	}

	if rest[0] == "help" {
		return runHelp(out, root, rest[1:])
	}

	res := resolveCommand(root, rest)
	if !res.ok {
		ce := unknownCommandError(res.parentPath, res.attempted, names(res.searchedIn))
		out.Error(ce)
		return int(ce.ExitCode())
	}

	if g.help {
		printHelp(out, commandHelp(res.path, res.leaf))
		return int(clierr.ExitOK)
	}

	if res.leaf.Run == nil {
		// A group command with no direct action (e.g. a future bare
		// "nise db") is shown as help rather than treated as an error.
		printHelp(out, commandHelp(res.path, res.leaf))
		return int(clierr.ExitOK)
	}

	return runLeaf(res.leaf, res.path, res.remaining, out, caps, ioc.Stdin)
}

// runLeaf parses argv against leaf's own flag set and calls its Run,
// rendering any error the same way every other error is rendered. path is
// leaf's full command path (nil for the --version alias, which has no
// path of its own to report).
func runLeaf(leaf *Command, path, argv []string, out output.Writer, caps term.Capabilities, stdin io.Reader) int {
	fs := newCommandFlagSet(leaf)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(out, commandHelp(path, leaf))
			return int(clierr.ExitOK)
		}
		full := "nise " + strings.Join(path, " ")
		ce := clierr.Usage(
			fmt.Sprintf("invalid flags for %q: %s", full, err),
			fmt.Sprintf("Run %q for usage.", strings.TrimSpace(full)+" --help"),
		).WithDocs("docs/cli-output.md#usage-errors")
		out.Error(ce)
		return int(ce.ExitCode())
	}

	env := &Env{Out: out, Caps: caps, Args: fs.Args(), Stdin: stdin}
	if err := leaf.Run(context.Background(), env); err != nil {
		ce := clierr.From(err)
		out.Error(ce)
		return int(ce.ExitCode())
	}
	return int(clierr.ExitOK)
}

func newCommandFlagSet(cmd *Command) *flag.FlagSet {
	if cmd.NewFlagSet != nil {
		return cmd.NewFlagSet()
	}
	return flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
}

// globalFlags holds the resolved state of every reserved global flag.
type globalFlags struct {
	json, quiet, verbose, noColor, help, version bool
}

// extractGlobalFlags removes every reserved global flag token from args,
// wherever it appears, and reports which were present. Global flags are
// simple booleans with no "=value" form, so a plain token scan is
// sufficient and unambiguous; this is also why command-specific flags must
// not reuse these names (documented on the flag constants above).
func extractGlobalFlags(args []string) ([]string, globalFlags) {
	var g globalFlags
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case flagJSON:
			g.json = true
		case flagQuiet:
			g.quiet = true
		case flagVerbose:
			g.verbose = true
		case flagNoColor:
			g.noColor = true
		case flagHelpLong, flagHelpShort:
			g.help = true
		case flagVersionLong:
			g.version = true
		default:
			out = append(out, a)
		}
	}
	return out, g
}

// resolveColor combines the environment's default (caps.ColorEnabled) with
// an explicit --no-color flag, which always wins: a user who types
// --no-color gets no color even if CLICOLOR_FORCE is set in the
// environment. (Mode-forces-off for --json is applied by the caller, not
// here — this function only knows about human-mode color.)
func resolveColor(caps term.Capabilities, noColorFlag bool) bool {
	if noColorFlag {
		return false
	}
	return caps.ColorEnabled()
}

// runHelp implements "nise help [path...]": root help with no further
// arguments, or the resolved command's help otherwise.
func runHelp(out output.Writer, root []*Command, args []string) int {
	if len(args) == 0 {
		printHelp(out, rootHelp(root))
		return int(clierr.ExitOK)
	}
	res := resolveCommand(root, args)
	if !res.ok {
		ce := unknownCommandError(res.parentPath, res.attempted, names(res.searchedIn))
		out.Error(ce)
		return int(ce.ExitCode())
	}
	printHelp(out, commandHelp(res.path, res.leaf))
	return int(clierr.ExitOK)
}

// unknownCommandError builds the usage error for an unrecognized command
// or subcommand name, including a "did you mean" suggestion when one of
// candidates is a close edit-distance match.
func unknownCommandError(parentPath []string, attempted string, candidates []string) *clierr.Error {
	label := "command"
	if len(parentPath) > 0 {
		label = fmt.Sprintf("subcommand of %q", "nise "+strings.Join(parentPath, " "))
	}
	cause := fmt.Sprintf("unknown %s %q", label, attempted)
	recovery := `Run "nise help" to see available commands.`
	if s, ok := suggest(attempted, candidates); ok {
		recovery = fmt.Sprintf("Did you mean %q? Run \"nise help\" to see available commands.", s)
	}
	return clierr.Usage(cause, recovery).WithDocs("docs/cli-output.md#usage-errors")
}
