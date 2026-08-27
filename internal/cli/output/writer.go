package output

import "io"

// Mode selects how a Writer renders: prose and restrained ANSI for a human
// reading a terminal, or exactly one JSON document per line for a script or
// another program.
type Mode int

const (
	// ModeHuman renders prose to stdout and errors to stderr, with color
	// and animation gated by Options.
	ModeHuman Mode = iota
	// ModeJSON renders only JSON documents, one per Result or Error call,
	// all on stdout. See docs/cli-output.md for the envelope schema and
	// the decision to keep errors on stdout in this mode.
	ModeJSON
)

// Options configures a Writer. All fields are decisions the caller has
// already resolved (from flags and internal/cli/term.Capabilities) before
// constructing a Writer — this package does not re-derive them.
type Options struct {
	// Quiet suppresses Line, Verbosef, Success, and Banner. It never
	// suppresses Result or Error.
	Quiet bool
	// Verbose enables Verbosef and the underlying error chain in Error
	// output (human: shown; JSON: the optional "chain" field populated).
	Verbose bool
	// Color allows Success/Banner/Spinner to use ANSI styling in human
	// mode. Ignored entirely in JSON mode.
	Color bool
	// Animate allows Banner and Spinner to render in human mode. Ignored
	// entirely in JSON mode.
	Animate bool
}

// Result is a command's primary output value: something with both a
// human-readable rendering and a JSON-marshalable shape (via ordinary
// encoding/json struct tags on the concrete type). A Writer's Result method
// picks one or the other depending on Mode.
type Result interface {
	// Human renders the result for a human reading a terminal.
	Human() string
}

// Spinner is a handle to a possibly-running progress indicator. Stop always
// prints msg immediately — a Spinner that is not actually animating (JSON
// mode, quiet, non-interactive, or animation disabled) still accepts Stop
// and still ends with msg reaching the user through Success/Line, so
// command code never needs an if-animated branch of its own.
type Spinner interface {
	// Stop ends the spinner, if it was running, and prints msg. It never
	// waits for another animation frame: a completed operation's result
	// prints immediately.
	Stop(msg string)
}

// Writer is the nise CLI's sole output abstraction. Command code talks only
// to this interface — never to os.Stdout, os.Stderr, or fmt.Print*
// directly — so that swapping Mode changes what a command's calls do
// without changing a single call site.
type Writer interface {
	// Mode reports which rendering mode this Writer was built with.
	Mode() Mode

	// Line prints an informational prose line to stdout. No-op in JSON
	// mode (prose has no place in a JSON stream) and when Quiet.
	Line(msg string)
	// Verbosef prints a formatted detail line to stdout, only when Verbose
	// is set. No-op in JSON mode and when Quiet.
	Verbosef(format string, args ...any)
	// Success prints a completed-action line to stdout, styled green when
	// color is enabled. No-op in JSON mode and when Quiet.
	Success(msg string)
	// Banner prints restrained ASCII branding to stdout. No-op unless
	// human mode, not quiet, and animation is enabled.
	Banner(text string)
	// Spinner starts a progress indicator labeled label. See the Spinner
	// doc comment for what happens when animation is not appropriate.
	Spinner(label string) Spinner

	// Result emits v as the command's primary output: v.Human() to stdout
	// in human mode, or v JSON-marshaled to stdout in JSON mode. Never
	// suppressed by Quiet — this is a command's actual output, not
	// incidental progress narration.
	Result(v Result)
	// Error renders err — ideally a *clierr.Error, and always treated as
	// one via clierr.From — to the correct stream and format for Mode.
	// Never suppressed by Quiet.
	Error(err error)
}

// New builds a Writer for mode, writing to stdout and stderr as given.
// stderr is unused by the JSON writer (see docs/cli-output.md's decision to
// keep --json output entirely on stdout) but is still accepted so callers
// don't need a mode-specific constructor.
func New(mode Mode, opts Options, stdout, stderr io.Writer) Writer {
	switch mode {
	case ModeJSON:
		return &jsonWriter{stdout: &safeWriter{w: stdout}, verbose: opts.Verbose}
	default:
		return &humanWriter{
			stdout:  &safeWriter{w: stdout},
			stderr:  &safeWriter{w: stderr},
			quiet:   opts.Quiet,
			verbose: opts.Verbose,
			color:   opts.Color,
			animate: opts.Animate,
		}
	}
}
