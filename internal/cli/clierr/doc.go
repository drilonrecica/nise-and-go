// Package clierr defines the nise CLI's error model: a single Error type
// that every command returns instead of a bare error, carrying a short
// cause, an optional wrapped error, a concrete recovery action, a stable
// exit code, and an optional documentation reference.
//
// Error implements the standard error interface and Unwrap() error, so
// errors.Is and errors.As work through it exactly as they would through any
// other wrapped error. The output layer (internal/cli/output) renders an
// Error differently depending on mode: human output leads with the cause
// and the recovery action, showing the underlying chain only under
// --verbose; JSON output emits a stable {"error": {...}} envelope. Neither
// path ever prints a stack trace.
//
// This package does not import runtime/logging, even though it needs the
// same "never print a secret" property that package's redaction machinery
// provides — see redact.go for why, and for the small, deliberately
// duplicated check that keeps the CLI free of a runtime/ dependency.
package clierr
