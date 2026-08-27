package clierr

import (
	"regexp"
	"strings"
)

// RedactPlaceholder replaces a value this package decides not to print.
const RedactPlaceholder = "[REDACTED]"

// denyFragments is a small, independent copy of the key-fragment idea in
// runtime/logging's redaction deny-set (see runtime/logging/redact.go). It
// is NOT imported from there on purpose: Constraint 2 in
// .superpowers/sdd/execution-plan/constraints.md keeps cmd/ and internal/
// standard-library-only for the CLI, and the controller's brief for this
// task is explicit that internal/cli must not depend on runtime/ at all —
// doctor, dev, and friends run before a runtime/logging instance even
// exists. Do not "fix" this duplication by adding the import; that import
// is exactly what was ruled out. If the two lists drift, that is an
// accepted cost of keeping the CLI dependency-free, not a bug in either
// package.
//
// This copy is intentionally smaller than runtime/logging's: it skips that
// package's camelCase/word-boundary normalization and its narrower
// "session"-only bounding, because CLI error detail keys are Nise's own
// short, hand-written identifiers (e.g. "dsn", "recipe_path"), not
// arbitrary structured-log attribute names pulled from third-party
// libraries. Plain case-insensitive substring containment is enough for
// that narrower job.
var denyFragments = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"authorization",
	"cookie",
	"apikey",
	"session",
	"csrf",
	"privatekey",
	"credential",
}

// isDeniedKey reports whether key, once lowercased and stripped of common
// separators, contains any deny fragment.
func isDeniedKey(key string) bool {
	k := strings.ToLower(key)
	k = strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(k)
	for _, f := range denyFragments {
		if strings.Contains(k, f) {
			return true
		}
	}
	return false
}

// redactValue returns RedactPlaceholder when key looks secret-shaped, and
// value unchanged otherwise. It is applied automatically by
// Error.WithDetail so a detail can never be added un-redacted by omission.
func redactValue(key, value string) string {
	if isDeniedKey(key) {
		return RedactPlaceholder
	}
	return value
}

// userinfoPattern matches the userinfo portion of a URL-shaped string, such
// as the "user:pass@" in "postgres://user:pass@host/db". It is the one
// pattern-based check this package makes on free text (as opposed to
// key/value pairs), because a wrapped database or SMTP driver error is the
// most realistic way a raw secret reaches this package's error chain.
var userinfoPattern = regexp.MustCompile(`://[^/@\s]+@`)

// scrubChainText redacts URL userinfo from s. It is applied to every line
// of Error.Chain, which is only ever shown under --verbose (human mode) or
// in the optional JSON "chain" field. It is not a general-purpose secret
// scanner: free text that names a secret without URL-embedding it (for
// example a bare API key printed by a third-party library) will not be
// caught here. Structured details added through WithDetail go through the
// stronger, key-based redactValue instead.
func scrubChainText(s string) string {
	return userinfoPattern.ReplaceAllString(s, "://"+RedactPlaceholder+"@")
}
