package check

const invariantMigrations = "Database migrations are ordered, forward-only, and every applied migration is still present (milestone M3)."

// checkMigrations reports the migration invariants as skipped.
//
// This is not a placeholder that will be forgotten. There is no migration
// subsystem in this slice at all: `nise db migrate`, the goose integration,
// and the generated db/migrations tree arrive with milestone M3, and a
// generated project today contains db/README.md and nothing else. A check
// that inspected an empty directory and reported ok would be reporting that
// an invariant holds when nothing was verified — the precise dishonesty this
// package's Status doc comment rules out. Naming the milestone in the reason
// is what makes the gap legible instead of invisible.
func checkMigrations() Check {
	return skip(CheckMigrations, invariantMigrations,
		"this project has no migration subsystem yet: migrations, their ordering rules, and \"nise db migrate\" all arrive with milestone M3, so there is nothing to verify")
}
