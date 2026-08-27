package logging

import (
	"crypto/rand"
	"encoding/base64"
)

// MaxIDLength bounds a request or correlation ID accepted from an untrusted
// inbound header. NewID never produces an ID longer than this; ValidID
// rejects anything longer.
const MaxIDLength = 128

// idByteLength is how much randomness NewID reads from crypto/rand. 16 bytes
// (128 bits) base64-encodes to exactly 22 characters and is enough entropy
// that a collision is not a practical concern.
const idByteLength = 16

// RequestIDKey is the structured-log attribute key Middleware attaches the
// request ID under.
const RequestIDKey = "request_id"

// CorrelationIDKey is the structured-log attribute key Middleware attaches
// the correlation ID under.
const CorrelationIDKey = "correlation_id"

// NewID returns a new, collision-resistant identifier suitable for a request
// or correlation ID. It reads idByteLength bytes from crypto/rand and encodes
// them with unpadded, URL-safe base64 (RFC 4648 §5), so the result contains
// none of '=', '/', or '+' — characters that break naive log parsers and are
// awkward in a URL or header value — and is always exactly 22 characters,
// well inside MaxIDLength.
func NewID() string {
	b := make([]byte, idByteLength)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read fails only if the system CSPRNG is unavailable, a
		// condition this package cannot recover from: falling back to a
		// weaker source would silently downgrade a security property (an
		// unpredictable identifier), which the project's fail-closed
		// constraint forbids. Panic rather than return a predictable ID.
		panic("runtime/logging: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// ValidID reports whether s is acceptable as an inbound request or
// correlation ID: non-empty, at most MaxIDLength bytes, and composed only of
// ASCII letters, digits, '-', and '_'.
//
// This rejects, among other things: newline injection (log forging), ANSI
// escape sequences (terminal injection), values over MaxIDLength, the empty
// string, and non-ASCII input. Anything ValidID accepts is safe to place
// verbatim into a log line, a structured log field, or an HTTP response
// header value.
func ValidID(s string) bool {
	if s == "" || len(s) > MaxIDLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}
