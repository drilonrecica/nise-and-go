// Package nonetwork makes Global Constraint 4 ("no command may perform an
// implicit network request, update check, or analytics call") a property
// the repository enforces, not a claim the docs make.
//
// It does that three ways:
//
//   - Static: TestStaticNoUnexpectedNetworkImports walks the direct import
//     list of every package under ./cmd/... and ./internal/... (via
//     `go list -json`) and fails if one that is not on the short, commented
//     allowlist in static_test.go imports a network-dialing standard
//     library package. TestStaticRuntimeNeverImportsInternal checks the
//     companion assertion from ADR 0011: no package under ./runtime/...
//     imports anything under internal/.
//   - Dynamic: TestDynamicNoOutboundConnections builds the real `nise`
//     binary and runs every non-interactive command in a subprocess whose
//     HTTP_PROXY/HTTPS_PROXY/ALL_PROXY point at a local listener that
//     records every connection it receives and then refuses it, and
//     asserts the listener recorded nothing. TestDynamicNetworkNamespace
//     re-runs the same commands inside an unshared, interface-less network
//     namespace (skipping cleanly where the sandbox forbids user
//     namespaces) as a stricter check that also catches a raw net.Dial,
//     which HTTP_PROXY cannot: see dynamic_test.go's and netns_test.go's
//     doc comments for exactly what each does and does not prove.
//   - Telemetry: TestNoTelemetryMarkers greps cmd/, internal/, and
//     runtime/ for known analytics SDK names and for any hard-coded
//     https:// host that is not on the short documented allowlist in
//     telemetry_test.go.
//
// See docs/no-telemetry.md for the guarantee this package enforces, stated
// for a reader who is not going to read Go source to believe it.
package nonetwork
