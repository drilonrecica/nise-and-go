package secure

import "fmt"

// Deployment tells a policy whether it is protecting a production deployment
// or a developer's machine. It is a closed set: the zero value is not a valid
// Deployment, and [ParseDeployment] never returns a value outside
// [Development] and [Production].
//
// This package deliberately does not import runtime/config, which owns the
// application's own environment type (ADR 0011, in this repository at
// docs/adr/0011-runtime-public-api.md, forbids the import edge). The
// application converts one to the other explicitly at its wiring site, which
// is what [ParseDeployment] exists for.
type Deployment string

// The recognized values of Deployment. There are two, not three, because
// exactly one security decision in this package turns on the deployment:
// whether Strict-Transport-Security is emitted. An automated test run needs
// the same answer as a developer's machine — no HSTS — so it maps to
// Development rather than earning a third value.
const (
	// Development is a developer's machine, an automated test run, or a
	// non-production integration environment. Strict-Transport-Security is
	// not emitted, and is actively removed if an inner handler set it: a
	// stray HSTS header pins http://localhost in the developer's browser
	// for a year, and there is no way to unpin it from the application.
	Development Deployment = "development"

	// Production is a deployment serving real users over TLS.
	// Strict-Transport-Security is emitted.
	Production Deployment = "production"
)

// ParseDeployment converts raw into a Deployment. It accepts exactly the
// three values runtime/config's environment type uses — "development",
// "test", and "production" — mapping "test" to [Development], and returns an
// error for anything else rather than guessing.
//
// An application wires its configured environment through here:
//
//	dep, err := secure.ParseDeployment(string(cfg.Environment))
//
// so that a typo or an unrecognized environment name fails at startup rather
// than silently selecting the weaker of the two policies.
func ParseDeployment(raw string) (Deployment, error) {
	switch raw {
	case "development", "test":
		return Development, nil
	case "production":
		return Production, nil
	default:
		return "", fmt.Errorf(
			"secure: unrecognized deployment %q; want one of %q, %q, or %q",
			raw, "development", "test", "production",
		)
	}
}
