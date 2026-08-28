package check

// Status is the outcome of one invariant check.
//
// There are deliberately only three. `nise doctor` has a fourth, StatusWarn,
// because it reports on a developer's machine, where "your Docker is older
// than we test against" is real information that must not fail a build.
// `nise check` reports on a project, in CI, and answers one question: does
// this project still hold the invariants Nise guarantees. A "warn" there
// would be a finding nobody is obliged to act on and nothing is obliged to
// gate on — which is how a check quietly stops meaning anything. Anything
// worth reporting is either a violation (StatusFail) or something this run
// could not actually verify (StatusSkipped, always with a reason).
type Status string

const (
	// StatusOK means the check verified its invariant holds.
	StatusOK Status = "ok"
	// StatusFail means the check verified its invariant is broken. One or
	// more of these is what makes `nise check` exit non-zero.
	StatusFail Status = "fail"
	// StatusSkipped means the check could not evaluate its invariant at
	// all: the subsystem does not exist yet, the caller did not supply an
	// input it needs, or a difference could not be distinguished from a
	// legitimate one. Detail always says which. A skip is never counted as
	// a pass and never fails the run.
	StatusSkipped Status = "skipped"
)

// Check is the result of evaluating one invariant.
//
// The field set is the stable JSON schema `nise check --json` emits, one
// document per check per line, matching docs/cli-output.md's JSON mode
// contract and `nise doctor`'s existing per-check shape. Adding a field is
// backward compatible; renaming or removing one is a breaking change.
type Check struct {
	// Name identifies the check. Stable across releases: scripts and CI
	// jobs may key off it.
	Name string `json:"name"`
	// Status is this check's outcome.
	Status Status `json:"status"`
	// Invariant states, in one sentence, the property this check protects
	// — not what it did. It is present on every check regardless of
	// status, so a reader of a skipped check still learns what went
	// unverified.
	Invariant string `json:"invariant"`
	// Detail is what was actually observed, or — for StatusSkipped — the
	// reason the check did not run. Never empty: a check that reports a
	// status without saying what it saw is the "honest status" failure
	// this field exists to prevent.
	Detail string `json:"detail"`
	// Remedy is the exact recovery action. Always set when Status is
	// StatusFail, and set on a StatusSkipped check only when there is a
	// genuinely correct action for the reason it skipped (for example:
	// re-run the check with the nise that generated the project). Left
	// empty otherwise, because a remedy invented to fill the field is
	// exactly the wrong-remedy defect this package is built to avoid.
	Remedy string `json:"remedy,omitempty"`
	// Paths names the offending files, relative to the project root and
	// slash-separated, sorted. Omitted when a check is not about
	// particular files.
	Paths []string `json:"paths,omitempty"`
}

// Report is the full result of a `nise check` run: every check, in the fixed
// order Run evaluated them. Never a map, so output order is identical across
// runs and machines (Global Constraint 5).
type Report struct {
	// Checks are the evaluated checks, in evaluation order.
	Checks []Check `json:"checks"`
}

// Failed reports whether any check violated its invariant. This is the sole
// condition under which `nise check` exits non-zero after having run: a
// skipped check never fails the run.
func (r Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// Counts returns how many checks passed, failed, and were skipped.
func (r Report) Counts() (ok, failed, skipped int) {
	for _, c := range r.Checks {
		switch c.Status {
		case StatusOK:
			ok++
		case StatusFail:
			failed++
		case StatusSkipped:
			skipped++
		}
	}
	return ok, failed, skipped
}

// skip builds a StatusSkipped check. reason must say why the invariant could
// not be evaluated, never merely that it was not.
func skip(name, invariant, reason string) Check {
	return Check{Name: name, Status: StatusSkipped, Invariant: invariant, Detail: reason}
}

// pass builds a StatusOK check. detail must say what was actually verified.
func pass(name, invariant, detail string) Check {
	return Check{Name: name, Status: StatusOK, Invariant: invariant, Detail: detail}
}

// fail builds a StatusFail check. remedy must be an action that is correct
// for the situation actually observed — see the package doc comment on why a
// wrong remedy is worse than a bare failure.
func fail(name, invariant, detail, remedy string) Check {
	return Check{Name: name, Status: StatusFail, Invariant: invariant, Detail: detail, Remedy: remedy}
}
