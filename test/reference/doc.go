// Package reference holds the checks that the reference application under
// examples/reference/ is what it claims to be.
//
// The reference application is an overlay (ADR 0028): examples/reference/app/
// holds only the files a developer wrote, and build.sh produces the running
// application by generating a project and copying them over it. That design
// has one invariant it lives or dies by — **no overlay file may be one the
// generator owns** — and an invariant nobody checks is a hope.
package reference
