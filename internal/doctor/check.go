package doctor

// Status is the outcome of a single doctor check.
type Status string

const (
	// StatusOK means the check verified its requirement is met.
	StatusOK Status = "ok"
	// StatusWarn means the check found something worth the developer's
	// attention, but it does not block the golden profile from working
	// (for example a project recipe created by a newer nise than the one
	// currently running).
	StatusWarn Status = "warn"
	// StatusFail means a required tool or condition is missing, unusable,
	// or older than the minimum version the golden profile needs. One or
	// more StatusFail checks is what makes `nise doctor` exit with
	// clierr.ExitPrecondition.
	StatusFail Status = "fail"
	// StatusSkipped means the check does not apply right now (for
	// example the recipe checks, run outside any generated project) and
	// was not evaluated at all — it is neither a pass nor a failure.
	StatusSkipped Status = "skipped"
)

// Check is the result of verifying one requirement. Found, Required, and
// Remedy are plain, human-readable strings so the same value renders
// directly in both human and JSON output (see internal/cli/doctor.go)
// without a second, parallel description living only in one of the two
// modes.
type Check struct {
	// Name identifies the check, e.g. "go", "node", "container-runtime",
	// "recipe". Stable across releases: scripts may key off it.
	Name string `json:"name"`
	// Status is this check's outcome.
	Status Status `json:"status"`
	// Found describes what was actually observed — a version string, an
	// error, or a reason the check does not apply. Never left empty: a
	// doctor check that reports ok without saying what it verified is
	// exactly the "honest status" failure mode this field exists to
	// prevent.
	Found string `json:"found,omitempty"`
	// Required describes what the check demands, in a form a developer
	// can compare against Found without reading source.
	Required string `json:"required,omitempty"`
	// Remedy is the exact recovery action, set whenever Status is not
	// StatusOK (StatusSkipped included, when there's something
	// actionable to say — most skips don't need one, since "you are not
	// inside a generated project" isn't something to fix).
	Remedy string `json:"remedy,omitempty"`
}

// Report is the full result of a `nise doctor` run: every check, in the
// fixed order Run evaluated them (never a map, so output order is
// deterministic across runs — constraints.md §5).
type Report struct {
	Checks []Check `json:"checks"`
}

// Failed reports whether any check in the report is StatusFail. This is
// the sole condition under which `nise doctor` exits with
// clierr.ExitPrecondition instead of clierr.ExitOK.
func (r Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}
