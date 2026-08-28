// Package check runs the invariant checks behind `nise check`.
//
// # What an invariant is, and what it is not
//
// BLUEPRINT §5 draws the line this package exists to hold: "nise check
// enforces safety, compatibility, migration, and generation invariants. It
// does not enforce every starter folder, dependency, component, or style
// decision after customization."
//
// The rule every check in this package is admitted under:
//
//	A check belongs here only if a violation breaks a property Nise itself
//	guarantees — through runtime/, through the recipe, or through the
//	ownership and determinism rules of generation — and that an application
//	cannot legitimately opt out of by editing a file it owns.
//
// The corollary matters just as much: when a property is enforced by a file
// the application owns and may rewrite (internal/platform/config/config.go,
// the router's middleware list, the frontend's component tree), it is not an
// invariant this package may assert. docs/commands/check.md names the checks
// that were deliberately excluded on that ground.
//
// # The one check admitted on a different ground
//
// The secret-material check is not derived from runtime/, the recipe, or the
// rules of generation. It is admitted on Global Constraint 8 — "No secret,
// token, cookie value, password, or connection-string password may appear in
// logs, CLI output, error messages, or generated files" — extended to the
// project's own tracked files, which the M1-012 brief names explicitly as a
// security invariant checkable now.
//
// Saying so out loud matters more here than it would elsewhere, because this
// is the one check that CAN fail a project whose only fault is that it was
// customized: a repository layout the starter never produced, a file the
// application added. That is precisely the case the rule above exists to
// exclude, so the exception has to be visible rather than implied. What
// keeps it defensible is that the property it protects is not a Nise
// convention at all — a committed credential is a defect in any repository,
// under any framework, and is the one finding whose cost of being missed is
// unbounded. The price paid for that is a real false-positive risk, which is
// why every rule in secrets.go is written narrow, is paired with an explicit
// tolerance, and has both a detection and a non-detection test.
//
// # Skipped is a first-class result
//
// A check reports [StatusSkipped], with a reason, whenever it cannot
// actually verify its invariant — a subsystem that does not exist yet, an
// input the caller did not supply, or a situation in which a failure could
// not be distinguished from a legitimate difference. This is not politeness.
// `nise doctor` shipped a version that reported fail for tool directives
// that only the framework repository declares: it fired in the first context
// a user runs the command, it exited non-zero so a CI gate failed on a
// healthy project, and the remedy it printed was actively wrong. A wrong
// remedy is worse than a bare failure, and a skip with a reason is what this
// package uses wherever a fail would carry one.
//
// # No side effects
//
// Running a check never writes into the project under inspection. The
// regeneration check renders into a temporary directory and compares; the
// ownership check copies the two self-verifying files into a temporary
// directory before asking internal/agentsfile whether they still verify.
// Nothing here opens a network connection or starts a subprocess other than
// `git ls-files`, and that one only to learn which files are tracked.
package check
