// Package doctor implements the checks behind `nise doctor`: verifying that
// the tools the golden profile needs are present and new enough, and that a
// project recipe found in or above the current directory parses and is
// compatible with the running nise build.
//
// Every check in this package runs a local executable or reads a local
// file — never the network. `go tool sqlc`, `go tool goose`, and
// `go tool oapi-codegen` resolve entirely from the module cache these
// binaries were already built from (see docs/toolchain.md's own
// verification section, which runs the same commands); this package never
// contacts a registry, proxy, or update endpoint, and never should
// (constraints.md §4).
//
// This package is private to Nise and imports only the standard library
// plus internal/recipe, which it reuses rather than reimplementing project
// recipe parsing.
package doctor
