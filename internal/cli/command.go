package cli

import (
	"context"
	"flag"
	"io"

	"github.com/drilonrecica/nise-and-go/internal/cli/output"
	"github.com/drilonrecica/nise-and-go/internal/cli/term"
)

// Command is a single nise command or command group. It is a plain value —
// no interface to implement, no registration side effect — so building one
// is just constructing a struct literal (commonly from a small constructor
// function, so flag-bound variables and Run can close over the same
// locals; see registry.go for the pattern).
type Command struct {
	// Name is the token typed on the command line, e.g. "doctor" or
	// "migrate" for the nested "nise db migrate".
	Name string
	// Short is a one-line description shown in help listings.
	Short string
	// Args, if non-empty, is this command's positional-argument
	// signature, written the way a user reads it in a usage line — for
	// example "<name>" for `nise generate feature <name>`, or
	// "[name]" for an argument the command can prompt for instead.
	//
	// It exists because a command's positional arguments are otherwise
	// invisible: help renders "nise new [flags]", and a user reading only
	// --help has no way to discover that `nise new` takes a project name
	// at all. The flag package describes flags for us; nothing described
	// these, so the help system could not express them and the
	// documentation had to send readers to an output that did not contain
	// the answer.
	//
	// Empty means the command takes no positional arguments. Nothing
	// validates Args against what Run actually accepts — it is help text,
	// and Run remains the authority on argument count.
	Args string
	// NewFlagSet, if non-nil, is called fresh on every dispatch to build
	// this command's own flag.FlagSet. Building it fresh (rather than
	// storing a single *flag.FlagSet on the Command) means the same
	// Command value is safe to dispatch more than once — every call gets
	// its own zero-valued flags, with no state left over from a previous
	// invocation. A constructor typically declares the bound variables
	// and both NewFlagSet and Run in the same closure scope, e.g.:
	//
	//	func doctorCommand() *Command {
	//		var fix bool
	//		return &Command{
	//			Name: "doctor",
	//			NewFlagSet: func() *flag.FlagSet {
	//				fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	//				fs.BoolVar(&fix, "fix", false, "attempt automatic fixes")
	//				return fs
	//			},
	//			Run: func(ctx context.Context, env *Env) error {
	//				if fix { ... }
	//				return nil
	//			},
	//		}
	//	}
	//
	// nil means this command takes no flags of its own.
	NewFlagSet func() *flag.FlagSet
	// Run executes the command. It may be nil for a command that exists
	// only to group Subcommands (e.g. a future "nise db" with no direct
	// action); dispatch then shows this command's help instead of
	// running it when no subcommand token follows.
	Run func(ctx context.Context, env *Env) error
	// Subcommands nests further commands under this one, e.g. Subcommands
	// on a "db" Command holding "migrate", "backup", "restore", and
	// "verify-backup". Nesting is not limited to one level.
	Subcommands []*Command
}

// Env is what a Command's Run receives: the output writer already built
// for the resolved mode, the detected terminal capabilities, the
// command's own positional arguments (after its flags are parsed), and
// stdin. Run should never reach for os.Stdout, os.Stderr, or os.Args
// directly — Env is the whole surface a command needs.
type Env struct {
	// Out is where Run must send all output — informational, result, and
	// error alike. See internal/cli/output for why this is the only
	// correct way for a command to print anything.
	Out output.Writer
	// Caps is the detected terminal capabilities, in case a command needs
	// to make its own presentation decision beyond what Out already
	// applies (Out itself already gates color/animation/quiet/JSON).
	Caps term.Capabilities
	// Args are this command's own positional arguments: whatever remained
	// after global flags were removed, the command path was resolved, and
	// this command's own flag.FlagSet consumed its flags.
	Args []string
	// Stdin is the process's standard input, for a command that reads one
	// (e.g. a future "nise new" prompt).
	Stdin io.Reader
}
