package lifecycle

import "fmt"

// Mode selects which components of a generated application's single binary
// a given process runs. It is a closed set: the zero value is not a valid
// Mode, and [ParseMode] never returns a value outside [ModeAll], [ModeWeb],
// and [ModeWorker].
type Mode string

// The recognized values of Mode. There is no fourth value and no default.
const (
	// ModeAll runs the HTTP server and the worker together in one process.
	// It is the default for small deployments.
	ModeAll Mode = "all"

	// ModeWeb runs only the HTTP server; the worker never starts.
	ModeWeb Mode = "web"

	// ModeWorker runs only the worker; the HTTP port is never bound.
	ModeWorker Mode = "worker"
)

// ParseMode converts raw into a Mode. It returns an error for any value
// other than exactly "all", "web", or "worker" — there is no default and no
// case-insensitive match, consistent with [runtime/config.ParseEnvironment]:
// guessing which mode an unrecognized value meant risks starting a
// deployment with the wrong components running, so an unrecognized value
// fails closed instead.
func ParseMode(raw string) (Mode, error) {
	switch Mode(raw) {
	case ModeAll, ModeWeb, ModeWorker:
		return Mode(raw), nil
	default:
		return "", fmt.Errorf(
			"lifecycle: unrecognized mode %q; want one of %q, %q, or %q",
			raw, ModeAll, ModeWeb, ModeWorker,
		)
	}
}
