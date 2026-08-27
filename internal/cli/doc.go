// Package cli is the nise CLI's command dispatcher: it turns os.Args into a
// resolved command, a parsed flag set, and a call to that command's Run,
// with no framework (no cobra, no reflection, no init()-time registration)
// anywhere in the path.
//
// A command is a plain *Command value — a name, short help text, an
// optional flag.FlagSet builder, an optional Run function, and optional
// Subcommands for nesting (see command.go). The full set of top-level
// commands lives in exactly one place, Commands in registry.go: a later
// task adds a command by appending a *Command literal to that function's
// returned slice — nothing discovers commands by scanning packages or by a
// side effect of importing one.
//
// Execute (see dispatch.go) is the package's only public entry point. It
// resolves global flags (--json, --quiet, --verbose, --no-color, --help)
// wherever they appear in argv, detects terminal capabilities via
// internal/cli/term, builds an internal/cli/output.Writer for the resolved
// mode, walks the command tree (including nested subcommands such as the
// planned `nise db migrate`) to find the leaf command, parses that leaf's
// own flags, and calls its Run — rendering any returned error through
// internal/cli/clierr and the same Writer used for everything else.
package cli
