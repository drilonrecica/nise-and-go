package clierr

import (
	"regexp"
	"strings"
)

// RedactPlaceholder replaces a value this package decides not to print.
const RedactPlaceholder = "[REDACTED]"

// denyFragments is a small, independent list serving the same idea as
// runtime/logging's redaction deny-set (see runtime/logging/redact.go),
// deliberately kept separate rather than imported.
//
// The reason is what the two lists are for, not a rule about import
// direction. ADR 0011 permits internal/ to import runtime/ precisely when
// the point is to reuse a rule the runtime enforces unconditionally, so the
// CLI cannot validate a divergent copy of it — internal/check does exactly
// that for ParseEnvironment and ParseMode. Redaction is not that shape.
// runtime/logging's list is tuned for arbitrary structured-log attribute
// names arriving from third-party libraries, and carries camelCase and
// word-boundary normalization plus a narrower "session"-only bounding to
// cope with them. This one covers CLI error-detail keys, which are Nise's
// own short, hand-written identifiers ("dsn", "recipe_path"), where plain
// case-insensitive substring containment is enough. There is no single
// source of truth here for the two to drift apart from: they are two
// answers to two different questions, and sharing one list would force each
// to carry the other's compromises.
//
// A second consideration reinforces it rather than carrying it alone:
// clierr runs in commands (doctor, dev) that execute before any
// runtime/logging instance exists, so there is nothing to borrow at the
// moment it is needed.
//
// So: adding a fragment here does not oblige anyone to add it there, or the
// reverse. What each list must stay is complete for its own input
// population; redact_test.go is where that is pinned for this one. If a
// future change does make the two genuinely one question — a shared
// runtime/internal redaction package, say, which ADR 0011 already names as
// the expected first resident of runtime/internal/ — reconsidering this
// split is legitimate, not forbidden.
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
