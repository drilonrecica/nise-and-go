// Package release holds the checks that the release configuration and the
// documentation describing it agree.
//
// A release configuration is the one file in this repository whose mistakes
// are invisible until a release: `goreleaser check` proves the YAML parses,
// and nothing proves it builds what the documentation promises. These tests
// close that gap by reading both and comparing them.
package release
