package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
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
			return runLeaf(vc, newCommandFlagSet(vc), nil, nil, out, caps, ioc.Stdin)
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

	// leafFS is built once here and reused for the collision check, the
	// awareness pass below, and (via runLeaf) the actual parse — never
	// rebuilt a second or third time, so a command's NewFlagSet closure
	// only ever runs once per dispatch, exactly as its own "fresh every
	// call" contract promises.
	leafFS := newCommandFlagSet(res.leaf)

	// A command whose own flag definitions collide with a reserved global
	// flag name (e.g. it defines "--quiet") can never actually receive
	// that flag — extractGlobalFlags already stripped it before leafFS
	// ever saw it — which otherwise fails silently: the command's own
	// variable stays at its zero value while the *global* flag of the
	// same name takes effect instead, with no error anywhere. This is a
	// bug in the command's own definition, not something a user typed,
	// so every invocation of a broken command fails loudly here,
	// regardless of whether this particular invocation even passed the
	// colliding flag.
	if collisions := reservedFlagCollisions(leafFS); len(collisions) > 0 {
		ce := reservedFlagCollisionError(res.path, collisions)
		out.Error(ce)
		return int(ce.ExitCode())
	}

	// Re-derive the global flags and the leaf's own argv with awareness
	// of leafFS's value-consuming flags, so a value that merely looks
	// like a reserved spelling (e.g. "gen --name --json", where --json is
	// the value of --name, not the global JSON flag) is never misread as
	// global or stolen from the flag it actually belongs to. See
	// extractGlobalFlagsAware's doc comment for why this must run over
	// the whole of args, not just the portion after the resolved path.
	awareArgs, g2 := extractGlobalFlagsAware(args, leafFS)
	if g2 != g {
		g = g2
		mode = output.ModeHuman
		if g.json {
			mode = output.ModeJSON
		}
		opts = output.Options{
			Quiet:   g.quiet,
			Verbose: g.verbose,
			Color:   mode == output.ModeHuman && resolveColor(caps, g.noColor),
			Animate: mode == output.ModeHuman && !g.quiet && caps.AnimationEnabled(),
		}
		out = output.New(mode, opts, ioc.Stdout, ioc.Stderr)
		if g.quiet && g.verbose {
			ce := clierr.Usage(
				`--quiet and --verbose cannot be used together`,
				`Choose one: drop "--quiet" or drop "--verbose".`,
			).WithDocs("docs/cli-output.md#output-modes")
			out.Error(ce)
			return int(ce.ExitCode())
		}
		reResolved := resolveCommand(root, awareArgs)
		if !reResolved.ok {
			// Defensive: the path itself cannot change between the blind
			// and aware passes (see extractGlobalFlagsAware's doc
			// comment), only the leaf's own trailing argv can.
			ce := unknownCommandError(reResolved.parentPath, reResolved.attempted, names(reResolved.searchedIn))
			out.Error(ce)
			return int(ce.ExitCode())
		}
		res = reResolved
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

	return runLeaf(res.leaf, leafFS, res.path, res.remaining, out, caps, ioc.Stdin)
}

// runLeaf parses argv against leafFS (already built for leaf — see
// execute, which builds it once and reuses it here rather than calling
// leaf.NewFlagSet a second time) and calls leaf.Run, rendering any error
// the same way every other error is rendered. path is leaf's full command
// path (nil for the --version alias, which has no path of its own to
// report).
func runLeaf(leaf *Command, leafFS *flag.FlagSet, path, argv []string, out output.Writer, caps term.Capabilities, stdin io.Reader) int {
	leafFS.SetOutput(io.Discard)
	leafFS.Usage = func() {}
	if err := leafFS.Parse(argv); err != nil {
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

	env := &Env{Out: out, Caps: caps, Args: leafFS.Args(), Stdin: stdin}
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
// not reuse these names (documented on the flag constants above, and
// enforced loudly at dispatch — see reservedFlagCollisions).
//
// Recognition stops at the first literal "--" token: everything from "--"
// onward, including "--" itself, is passed through completely unstripped.
// This matters for a command that forwards its own trailing arguments to a
// subprocess (the planned `dev` and `test` commands both need this) —
// `nise test -- --verbose` must hand the subprocess a real "--verbose"
// token, not have it silently consumed as nise's own global --verbose
// flag. It also matches how Go's flag package itself treats "--": a
// command's own flag.FlagSet.Parse stops at the same token for the same
// reason, so a "--" surviving this pass lands exactly where a value flag
// expects to see it.
func extractGlobalFlags(args []string) ([]string, globalFlags) {
	var g globalFlags
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
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

// reservedFlagNames is the set of flag names (without leading dashes) a
// command's own flag.FlagSet must never define — each one shadows a
// global flag of the same spelling. "h" is included alongside "help"
// because Go's flag package itself treats -h as an alias for -help.
var reservedFlagNames = map[string]struct{}{
	"json":     {},
	"quiet":    {},
	"verbose":  {},
	"no-color": {},
	"help":     {},
	"h":        {},
	"version":  {},
}

// reservedFlagCollisions returns the sorted names of every flag fs defines
// that collides with a reserved global flag name. A non-empty result means
// the command itself has a bug: extractGlobalFlags strips a reserved
// spelling from argv before any command's own FlagSet ever sees it, so a
// command that also defines, say, "quiet" can never actually receive its
// own --quiet — the global flag silently wins instead, and the command's
// local variable stays at its zero value while global quiet mode turns on
// with no error anywhere. Detecting this from the FlagSet's own
// definitions (rather than from what a particular invocation happened to
// type) means a broken command fails loudly on every invocation, not only
// when a user happens to pass the colliding flag.
func reservedFlagCollisions(fs *flag.FlagSet) []string {
	if fs == nil {
		return nil
	}
	var collisions []string
	fs.VisitAll(func(f *flag.Flag) {
		if _, reserved := reservedFlagNames[f.Name]; reserved {
			collisions = append(collisions, f.Name)
		}
	})
	sort.Strings(collisions)
	return collisions
}

// reservedFlagCollisionError builds the loud, deterministic failure for a
// command whose own flag definitions collide with a reserved global flag
// name. This is a defect in the command's own definition, not a mistake
// the invoking user made, but it still has to surface as an ExitError
// result since Execute has no other channel to report it through.
func reservedFlagCollisionError(path []string, collisions []string) *clierr.Error {
	full := "nise " + strings.Join(path, " ")
	return clierr.New(
		clierr.ExitError,
		fmt.Sprintf("command %q defines a flag that collides with a reserved global flag: --%s",
			full, strings.Join(collisions, ", --")),
		"This is a bug in the command's own definition — rename the colliding flag.",
	).WithDocs("docs/cli-output.md#global-flags")
}

// boolFlag mirrors the private interface the standard flag package itself
// checks for (a Value with an IsBoolFlag() bool method returning true):
// see the flag package's own doc comment on FlagSet.BoolVar's sibling
// helpers. Every flag.FlagSet built via a *Var constructor for a bool
// satisfies it, which is what lets extractGlobalFlagsAware tell a
// boolean flag (no following value token) apart from a value flag (the
// next token belongs to it, whatever it looks like) without knowing
// anything else about the command.
type boolFlag interface {
	IsBoolFlag() bool
}

// valueConsumingFlagNames returns the names (without leading dashes) of
// every flag fs defines that is NOT boolean — i.e. every flag whose next
// command-line token, when given in the separate "-name value" form
// rather than "-name=value", is that flag's value rather than a fresh
// token of its own.
func valueConsumingFlagNames(fs *flag.FlagSet) map[string]struct{} {
	names := map[string]struct{}{}
	if fs == nil {
		return names
	}
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(boolFlag); ok && bf.IsBoolFlag() {
			return
		}
		names[f.Name] = struct{}{}
	})
	return names
}

// parseFlagToken reports whether a is flag-shaped ("-x" or "--x", but not
// bare "-" or "--"), and if so, its name with any leading dashes removed
// and whether the token already carries its value via "=" (e.g.
// "--name=value").
func parseFlagToken(a string) (name string, hasEmbeddedValue, ok bool) {
	if len(a) < 2 || a[0] != '-' {
		return "", false, false
	}
	t := a[1:]
	if len(t) > 0 && t[0] == '-' {
		t = t[1:]
	}
	if t == "" {
		return "", false, false
	}
	if eq := strings.IndexByte(t, '='); eq >= 0 {
		return t[:eq], true, true
	}
	return t, false, true
}

// extractGlobalFlagsAware re-derives the reserved-global-flag view of args
// with awareness of leafFS's own value-consuming flags, so a value that
// merely looks like a reserved spelling is never mistaken for the global
// flag of the same name.
//
// extractGlobalFlags alone cannot tell "gen --name --json" (a value flag
// "--name" whose value happens to be the literal text "--json") from
// "gen --json" (the global JSON flag): scanning blindly, it strips
// "--json" out of the args either way, which either silently turns on
// global JSON mode when the user meant a value, or — if "--name" required
// a value and there was nothing else after "--json" got removed — leaves
// the command's own FlagSet.Parse failing with "flag needs an argument"
// for a flag the user did supply a value for. This function is only safe
// to run once leafFS is known (and, by construction, cannot itself define
// a reserved name — see reservedFlagCollisions, checked before this
// runs): value-consuming flag names never collide with reserved names, so
// treating "--name"'s next token as its value, whatever that token looks
// like, is always correct.
//
// Running this over the whole of args (not just the portion after the
// resolved command path) is safe: a command path segment is always a
// bare word, never flag-shaped, so parseFlagToken never matches one, and
// the value-consuming-flag check therefore never fires on a path token.
func extractGlobalFlagsAware(args []string, leafFS *flag.FlagSet) ([]string, globalFlags) {
	valueFlags := valueConsumingFlagNames(leafFS)
	var g globalFlags
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if name, embedded, isFlag := parseFlagToken(a); isFlag && !embedded {
			if _, isValueFlag := valueFlags[name]; isValueFlag {
				out = append(out, a)
				if i+1 < len(args) {
					i++
					out = append(out, args[i])
				}
				continue
			}
		}
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
	if collisions := reservedFlagCollisions(newCommandFlagSet(res.leaf)); len(collisions) > 0 {
		ce := reservedFlagCollisionError(res.path, collisions)
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
