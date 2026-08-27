package secure

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
)

// maxSourceLength bounds a single CSP source expression. Real sources are
// short; anything longer is a mistake or an injection attempt.
const maxSourceLength = 512

// unsafeKeywords are CSP source keywords this package refuses to place in any
// directive, no matter which option asks for it.
//
// 'unsafe-inline' and 'unsafe-eval' in script-src are the two weakenings the
// project's brief forbids outright, and 'wasm-unsafe-eval' and
// 'unsafe-hashes' are the same holes wearing narrower names:
// 'wasm-unsafe-eval' restores dynamic code compilation for WebAssembly, and
// 'unsafe-hashes' extends hash matching to event-handler attributes, which is
// the injection sink hashes exist to close. '*' is not a keyword but is
// refused alongside them: a wildcard source in an application whose every
// asset is same-origin is always a mistake.
//
// AllowInlineStyleAttributes is the single, named, reason-carrying exception,
// and it writes 'unsafe-inline' into style-src-attr only — never into
// script-src, style-src, or style-src-elem. It does not route through
// validSource.
var unsafeKeywords = map[string]struct{}{
	"'unsafe-inline'":    {},
	"'unsafe-eval'":      {},
	"'unsafe-hashes'":    {},
	"'wasm-unsafe-eval'": {},
	"*":                  {},
}

// validSource checks one CSP source expression supplied by an application.
//
// The critical property is that the result cannot break out of its directive:
// a source containing ';' would start a new directive, one containing ',' or
// whitespace would split the source list, and one containing CR or LF would
// split the HTTP response header itself. Rejecting every byte outside
// printable ASCII, plus ';' and ',', makes all four impossible.
func validSource(source string) error {
	if source == "" {
		return fmt.Errorf("secure: empty CSP source")
	}
	if len(source) > maxSourceLength {
		return fmt.Errorf("secure: CSP source longer than %d bytes", maxSourceLength)
	}
	for i := 0; i < len(source); i++ {
		c := source[i]
		if c < 0x21 || c > 0x7E || c == ';' || c == ',' {
			return fmt.Errorf(
				"secure: CSP source %q contains a byte that would break out of its directive or the response header",
				source,
			)
		}
	}
	if _, bad := unsafeKeywords[strings.ToLower(source)]; bad {
		return fmt.Errorf(
			"secure: CSP source %q is refused by this package; see the runtime/secure documentation for the alternatives",
			source,
		)
	}
	return nil
}

// hashAlgorithms are the CSP hash algorithms this package accepts.
// hashAlgorithms are the CSP hash algorithms this package accepts, with the
// exact digest width each one produces. The width is validated, not just the
// prefix and the base64 alphabet: 'sha256-AAAA' decodes cleanly as base64 but
// is three bytes where SHA-256 produces thirty-two, and a browser answers a
// hash that cannot match by silently blocking the script.
//
// That silence is the whole reason this is enforced here. The one place a
// hash enters a generated application is the frontend handler computing the
// SvelteKit bootstrap script's hash from the embedded artifact at startup,
// and a mistake there must surface as a refused policy at startup rather
// than as a blank page and a console violation in a browser somewhere.
var hashAlgorithms = []struct {
	prefix      string
	digestBytes int
}{
	{prefix: "sha256-", digestBytes: sha256.Size},
	{prefix: "sha384-", digestBytes: sha512.Size384},
	{prefix: "sha512-", digestBytes: sha512.Size},
}

// normalizeHash validates a CSP hash source and returns it in canonical
// quoted form, accepting it with or without the surrounding single quotes.
//
// A hash is not a weakening in the way a keyword is: it names one exact byte
// sequence, so an attacker who injects different content into the same
// position is still blocked. That is why WithScriptHashes and WithStyleHashes
// take no reason and record no waiver — but it is only true if the value
// really is a hash, which is what this function enforces.
func normalizeHash(raw string) (string, error) {
	h := strings.TrimSpace(raw)
	h = strings.Trim(h, "'")
	if err := validSource("'" + h + "'"); err != nil {
		return "", err
	}
	for _, algorithm := range hashAlgorithms {
		if !strings.HasPrefix(h, algorithm.prefix) {
			continue
		}
		encoded := strings.TrimPrefix(h, algorithm.prefix)
		if encoded == "" {
			return "", fmt.Errorf("secure: CSP hash %q has no digest", raw)
		}
		digest, err := decodeBase64Any(encoded)
		if err != nil {
			return "", fmt.Errorf("secure: CSP hash %q is not valid base64", raw)
		}
		if len(digest) != algorithm.digestBytes {
			return "", fmt.Errorf(
				"secure: CSP hash %q decodes to %d bytes; %s produces %d",
				raw, len(digest), strings.TrimSuffix(algorithm.prefix, "-"), algorithm.digestBytes,
			)
		}
		return "'" + h + "'", nil
	}

	prefixes := make([]string, 0, len(hashAlgorithms))
	for _, algorithm := range hashAlgorithms {
		prefixes = append(prefixes, algorithm.prefix)
	}
	return "", fmt.Errorf(
		"secure: CSP hash %q must start with one of %s",
		raw, strings.Join(prefixes, ", "),
	)
}

// decodeBase64Any decodes a CSP base64-value, whose grammar accepts both the
// standard and the URL-safe alphabet, with or without padding.
func decodeBase64Any(encoded string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		if decoded, err := encoding.DecodeString(encoded); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("secure: %q is not valid base64", encoded)
}

// validReportURI checks a CSP report-uri value.
//
// A same-origin absolute path is the expected form and the only one that
// needs no further trust. An absolute https URL is accepted for an external
// collector. Plain http is refused: a violation report names the page URL and
// the blocked resource, which is exactly the kind of detail that must not
// cross the network in cleartext.
func validReportURI(uri string) error {
	if uri == "" {
		return fmt.Errorf("secure: empty CSP report URI")
	}
	if err := validSource(uri); err != nil {
		return fmt.Errorf("secure: CSP report URI %q is not a usable header value", uri)
	}
	switch {
	case strings.HasPrefix(uri, "/") && !strings.HasPrefix(uri, "//"):
		return nil
	case strings.HasPrefix(uri, "https://") && len(uri) > len("https://"):
		return nil
	default:
		return fmt.Errorf(
			"secure: CSP report URI %q must be a same-origin absolute path or an https:// URL",
			uri,
		)
	}
}

// directive is one rendered CSP directive: a name and its source list. A nil
// or empty sources slice renders as the bare directive name, which is how
// `sandbox` is expressed.
type directive struct {
	name    string
	sources []string
}

// render joins directives into a CSP header value.
//
// Ordering is fixed and deterministic — `default-src` first because it is the
// fallback every other directive is read against, then the rest sorted by
// name. Two policies built from the same options always produce the same
// bytes, so a test can assert the exact header value and a reviewer can diff
// two policies by eye.
func render(ds []directive) string {
	sorted := make([]directive, len(ds))
	copy(sorted, ds)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].name == "default-src" {
			return sorted[j].name != "default-src"
		}
		if sorted[j].name == "default-src" {
			return false
		}
		return sorted[i].name < sorted[j].name
	})

	var b strings.Builder
	for i, d := range sorted {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(d.name)
		for _, s := range d.sources {
			b.WriteByte(' ')
			b.WriteString(s)
		}
	}
	return b.String()
}

// renderWithNonce renders ds with nonce appended to the script-src directive.
// Directives are otherwise untouched. If ds has no script-src the nonce is
// dropped rather than invented into some other directive, which keeps the
// API policy — which has no script-src at all — from silently gaining one.
func renderWithNonce(ds []directive, nonce string) string {
	withNonce := make([]directive, len(ds))
	for i, d := range ds {
		if d.name != "script-src" {
			withNonce[i] = d
			continue
		}
		sources := make([]string, 0, len(d.sources)+1)
		sources = append(sources, d.sources...)
		sources = append(sources, "'nonce-"+nonce+"'")
		withNonce[i] = directive{name: d.name, sources: sources}
	}
	return render(withNonce)
}
