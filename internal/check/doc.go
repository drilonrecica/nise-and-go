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
