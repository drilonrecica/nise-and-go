package config

import "fmt"

// Environment identifies the typed deployment environment an application is
// running in. It is a closed set: the zero value is not a valid Environment,
// and [ParseEnvironment] never returns a value outside [Development], [Test],
// and [Production].
type Environment string

// The recognized values of Environment. There is no fourth value and no
// default: an application that needs a finer distinction (staging, for
// example) should treat it as a Production deployment for validation
// purposes and distinguish it through a separate, application-owned setting.
const (
	// Development is a developer's own machine or a shared, non-production
	// integration environment. Fail-closed production validation does not
	// run.
	Development Environment = "development"

	// Test is an automated test run. Fail-closed production validation does
	// not run.
	Test Environment = "test"

	// Production is a deployment serving real users or real data.
	// [Validate] enforces its checks only in this environment.
	Production Environment = "production"
)

// ParseEnvironment converts raw into an Environment. It returns an error for
// any value other than exactly "development", "test", or "production" —
// there is no default. See the package doc comment for why an unrecognized
// value fails closed instead of guessing.
func ParseEnvironment(raw string) (Environment, error) {
	switch Environment(raw) {
	case Development, Test, Production:
		return Environment(raw), nil
	default:
		return "", fmt.Errorf(
			"unrecognized environment %q; want one of %q, %q, or %q",
			raw, Development, Test, Production,
		)
	}
}
