package check

const invariantMigrations = "Database migrations are ordered, forward-only, and every applied migration is still present (milestone M3)."

// checkMigrations reports the migration invariants as skipped.
//
// Generated projects now contain embedded Goose migrations, but this
// framework check does not yet load the generated application's provider or
// database history. M3-004 adds that explicit command/compatibility boundary;
// reporting ok before then would claim deployed-history guarantees that were
// never verified.
func checkMigrations() Check {
	return skip(CheckMigrations, invariantMigrations,
		"readable embedded Goose migrations exist, but deployed-history and schema-compatibility validation arrives with M3-004; no database history was verified")
}
