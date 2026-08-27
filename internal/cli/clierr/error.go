package clierr

import (
	"errors"
	"fmt"
)

// ExitCode is a nise CLI process exit code. This set is a stable public
// contract (see docs/cli-output.md): scripts and CI jobs may depend on
// these exact numbers, so changing what an existing code means is a
// breaking change.
type ExitCode int

const (
	// ExitOK means the command completed successfully.
	ExitOK ExitCode = 0
	// ExitError is a generic failure: the command understood what it was
	// asked to do and it did not succeed, for a reason that is not a usage
	// mistake or an environment precondition.
	ExitError ExitCode = 1
	// ExitUsage means the command line itself was invalid: an unknown
	// command, an unknown or malformed flag, or an incompatible
	// combination of flags (for example --quiet with --verbose).
	ExitUsage ExitCode = 2
	// ExitPrecondition means the command line was valid but something the
	// command depends on — a required tool, a reachable database, a
	// writable directory — was missing or unusable before the command
	// could even attempt its work.
	ExitPrecondition ExitCode = 3
)

// defaultCode maps an ExitCode to the stable machine-readable string used
// as error.code in JSON output when an Error does not set a more specific
// code of its own.
func defaultCode(exit ExitCode) string {
	switch exit {
	case ExitOK:
		return "ok"
	case ExitUsage:
		return "usage_error"
	case ExitPrecondition:
		return "precondition_error"
	default:
		return "error"
	}
}

// Error is the nise CLI's error type. Every command should return one
// instead of a bare error so the output layer can render a consistent
// cause-then-recovery message instead of a raw Go error string or a stack
// trace.
//
// Error implements Unwrap() error, so errors.Is and errors.As work through
// it exactly as they would through fmt.Errorf's %w: wrapping a sentinel
// error with Wrap does not hide it from a caller further up the chain that
// checks for it.
type Error struct {
	code     string
	cause    string
	recovery string
	docs     string
	details  map[string]string
	exit     ExitCode
	err      error
}

// New creates an Error with no wrapped underlying error. cause is a short,
// user-facing description of what went wrong; recovery is the concrete
// action the user can take next. Both are required in the sense that an
// empty string renders as an empty line — callers should always supply
// both.
func New(exit ExitCode, cause, recovery string) *Error {
	return &Error{exit: exit, cause: cause, recovery: recovery}
}

// Wrap creates an Error that carries err as its underlying cause. err is
// never shown to the user by default; it surfaces only in the --verbose
// human chain or the optional JSON "chain" field, and only after being
// passed through scrubChainText.
func Wrap(err error, exit ExitCode, cause, recovery string) *Error {
	return &Error{exit: exit, cause: cause, recovery: recovery, err: err}
}

// Usage is a convenience constructor for the common case of an
// ExitUsage error: an unknown command, unknown flag, or invalid flag
// combination.
func Usage(cause, recovery string) *Error {
	return New(ExitUsage, cause, recovery)
}

// Precondition is a convenience constructor for the common case of an
// ExitPrecondition error: something the command depends on was missing or
// unusable.
func Precondition(cause, recovery string) *Error {
	return New(ExitPrecondition, cause, recovery)
}

// WithCode sets a machine-readable error.code more specific than the
// exit-code-derived default (for example "doctor.missing_tool"). It
// returns e for chaining.
func (e *Error) WithCode(code string) *Error {
	e.code = code
	return e
}

// WithDocs sets an optional documentation reference (for example
// "docs/cli-output.md#usage-errors"), shown in JSON output and available
// to the human renderer. It returns e for chaining.
func (e *Error) WithDocs(ref string) *Error {
	e.docs = ref
	return e
}

// WithDetail adds one structured key/value detail to the error, redacting
// value automatically when key looks secret-shaped (see redactValue). It
// returns e for chaining, so a detail can never be attached without going
// through redaction — there is no lower-level path that skips it.
func (e *Error) WithDetail(key, value string) *Error {
	if e.details == nil {
		e.details = make(map[string]string)
	}
	e.details[key] = redactValue(key, value)
	return e
}

// Code returns the machine-readable error code: the value set by WithCode,
// or the exit-code-derived default when none was set.
func (e *Error) Code() string {
	if e.code != "" {
		return e.code
	}
	return defaultCode(e.exit)
}

// Cause returns the short, user-facing description of what went wrong.
func (e *Error) Cause() string { return e.cause }

// Recovery returns the concrete action the user can take next.
func (e *Error) Recovery() string { return e.recovery }

// Docs returns the optional documentation reference, or "" when none was
// set.
func (e *Error) Docs() string { return e.docs }

// Details returns a copy of the structured details attached via
// WithDetail, or nil when none were attached. Values are already redacted.
func (e *Error) Details() map[string]string {
	if len(e.details) == 0 {
		return nil
	}
	out := make(map[string]string, len(e.details))
	for k, v := range e.details {
		out[k] = v
	}
	return out
}

// ExitCode returns the process exit code this error maps to.
func (e *Error) ExitCode() ExitCode { return e.exit }

// Chain returns the message of every error in the Unwrap chain of the
// underlying error, most-wrapped first, each scrubbed of URL userinfo (see
// scrubChainText). It is empty when Error was built with New rather than
// Wrap. Callers show it only under --verbose (human) or in the optional
// JSON "chain" field — never by default.
func (e *Error) Chain() []string {
	var lines []string
	for err := e.err; err != nil; err = errors.Unwrap(err) {
		lines = append(lines, scrubChainText(err.Error()))
	}
	return lines
}

// Error implements the error interface. It is deliberately plain and short
// — this is what a %v/%w format verb or a log line elsewhere in the process
// sees, not the rendering the CLI's own output layer uses for the user
// (that's HumanLines/JSONEnvelope). It never prints details or the full
// chain (an Error wrapping another Error only shows one level here; use
// Chain for the full unwrapped list). The wrapped error's own text is
// scrubbed through scrubChainText, exactly like Chain does — Error() is
// the spelling a future caller reaches for with a bare %v or %s, often
// without knowing there is a scrubbed alternative, so it cannot be the
// one rendering path that skips redaction.
func (e *Error) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %s", e.cause, scrubChainText(e.err.Error()))
	}
	return e.cause
}

// Unwrap returns the wrapped underlying error, or nil, so errors.Is and
// errors.As traverse through an Error exactly as they would through
// fmt.Errorf's %w.
func (e *Error) Unwrap() error { return e.err }

// From returns err as an *Error: err itself when it already is one (or
// wraps one, via errors.As), or a new generic ExitError-class Error
// wrapping it otherwise. Every command's returned error passes through
// From exactly once, at the dispatch boundary, so the output layer always
// has an Error to render regardless of what a command actually returned.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}
	return Wrap(err, ExitError, "an unexpected error occurred", "Run with --verbose for more detail.")
}
