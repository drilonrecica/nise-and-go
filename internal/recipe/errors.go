package recipe

import "fmt"

// FieldError reports a recipe field that is missing, malformed, or outside its
// accepted set. The CLI renders Field and Problem as the cause and Accepted as
// the recovery guidance.
type FieldError struct {
	// Field is the offending field, in the recipe's own notation: "profile",
	// "modules", or "modules[2]".
	Field string
	// Problem states what is wrong with the field, without repeating its name.
	Problem string
	// Accepted lists the values the field allows, or nil when the field is not
	// drawn from a closed set.
	Accepted []string
}

// Error implements error.
func (e *FieldError) Error() string {
	if len(e.Accepted) == 0 {
		return fmt.Sprintf("%s: %s", e.Field, e.Problem)
	}
	return fmt.Sprintf("%s: %s (accepted: %s)", e.Field, e.Problem, joinAccepted(e.Accepted))
}

// SchemaVersionError reports a recipe written by a newer nise than the one
// reading it. It is a separate type because the recovery action is to upgrade
// the CLI, not to edit the file.
type SchemaVersionError struct {
	// Found is the schema version the recipe declares.
	Found int
	// Supported is the highest schema version this build understands.
	Supported int
}

// Error implements error.
func (e *SchemaVersionError) Error() string {
	return fmt.Sprintf(
		"schemaVersion: recipe declares version %d, but this nise understands %d; upgrade nise to work with this project",
		e.Found, e.Supported,
	)
}
