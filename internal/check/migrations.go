package check

const invariantMigrations = "Database migrations are ordered, forward-only, and every applied migration is still present (milestone M3)."

// checkMigrations reports the migration invariants as skipped.
//
// Generated projects expose the database-backed invariant through `nise db
// status`. This offline check has no selected database endpoint, so reporting
// ok here would claim deployed-history guarantees that were never verified.
func checkMigrations() Check {
	return skip(CheckMigrations, invariantMigrations,
		"database history is not inspected by offline nise check; run \"nise db status\" against the intended database")
}
