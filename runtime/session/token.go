package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// TokenVersion is the prefix every token this package issues begins with. A
// future format is a different prefix, not a flag inside the same one.
const TokenVersion = "ns1"

// tokenSeparator divides the version prefix from the secret. It is an
// underscore rather than a dot so a token is one word in a log grep and one
// token in a URL, and never looks like a dotted structured credential a reader
// might try to decode.
const tokenSeparator = "_"

const (
	// SecretBytes is the amount of randomness in every token.
	SecretBytes = 32
	// DigestBytes is the length of the stored digest.
	DigestBytes = sha256.Size
	// MaxTokenBytes bounds an accepted token string. It is checked before
	// any decoding, so an oversized value costs a length comparison.
	MaxTokenBytes = 128
)

// encodedSecretLength is how many base64url characters SecretBytes produces
// without padding.
var encodedSecretLength = base64.RawURLEncoding.EncodedLen(SecretBytes)

// redacted is what every formatting and serialization path on Token returns.
const redacted = "[REDACTED]"

// Errors reported by token handling.
var (
	// ErrTokenFormat reports a value that is not a well-formed session
	// token. It deliberately does not distinguish which rule failed: the
	// only caller is an authentication path, and a parser that explains
	// itself to an attacker is answering questions.
	ErrTokenFormat = errors.New("value is not a well-formed session token")
)

// Token is one session credential.
//
// It is unprintable through every formatting and serialization path Go offers:
// String, GoString, MarshalJSON, MarshalText, and LogValue all return a fixed
// placeholder. [Token.Value] is the single explicit accessor, for the one
// moment the value is handed to the client.
type Token struct {
	value string
}

// New mints a token from the system's cryptographic random source.
func New() (Token, error) {
	secret := make([]byte, SecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return Token{}, fmt.Errorf("generating a session token: %w", err)
	}
	return Token{value: TokenVersion + tokenSeparator + base64.RawURLEncoding.EncodeToString(secret)}, nil
}

// Parse validates a token presented by a client.
//
// It checks the shape only. A well-formed token is not an authenticated one:
// the caller still has to find its digest in storage and evaluate the session
// it belongs to.
func Parse(raw string) (Token, error) {
	if len(raw) > MaxTokenBytes {
		return Token{}, ErrTokenFormat
	}
	version, secret, found := strings.Cut(raw, tokenSeparator)
	if !found || version != TokenVersion || len(secret) != encodedSecretLength {
		return Token{}, ErrTokenFormat
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	if err != nil || len(decoded) != SecretBytes {
		return Token{}, ErrTokenFormat
	}
	return Token{value: raw}, nil
}

// Value returns the token string to hand the client. It is the only accessor
// that reveals it; call it at the one boundary that needs to.
func (t Token) Value() string { return t.value }

// Digest returns what the server stores: SHA-256 of the token.
func (t Token) Digest() []byte {
	sum := sha256.Sum256([]byte(t.value))
	return sum[:]
}

// IsZero reports whether t is the unconstructed zero value.
func (t Token) IsZero() bool { return t.value == "" }

// String implements [fmt.Stringer] with a fixed redaction placeholder.
func (t Token) String() string { return redacted }

// GoString implements [fmt.GoStringer], which fmt consults for "%#v".
func (t Token) GoString() string { return redacted }

// MarshalJSON implements [encoding/json.Marshaler]. It always encodes the
// placeholder, never the token.
func (t Token) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }

// MarshalText implements [encoding.TextMarshaler]. It always returns the
// placeholder.
func (t Token) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// LogValue implements [log/slog.LogValuer] so a token passed directly to a log
// call is redacted rather than recorded.
func (t Token) LogValue() slog.Value { return slog.StringValue(redacted) }

// Digest computes the stored digest for a client-presented value, validating
// its shape first.
//
// It is the lookup helper: a caller reads the cookie or header, calls this, and
// queries by the result. Nothing between the two ever holds the token in a
// variable that could be logged by accident.
func Digest(raw string) ([]byte, error) {
	token, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	return token.Digest(), nil
}

// EqualDigest reports whether two digests match, in constant time.
//
// Lookup by digest is the normal path and needs no comparison of its own. This
// exists for the paths that fetch a row some other way — by session id, say —
// and must still confirm the presented token belongs to it.
func EqualDigest(a, b []byte) bool {
	if len(a) != DigestBytes || len(b) != DigestBytes {
		return false
	}
	var difference byte
	for i := range a {
		difference |= a[i] ^ b[i]
	}
	return difference == 0
}

// CloneDigest returns a copy, so a caller cannot hand storage a slice it later
// mutates.
func CloneDigest(digest []byte) []byte { return slices.Clone(digest) }
