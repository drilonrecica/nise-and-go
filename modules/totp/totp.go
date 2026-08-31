package totp

import (
	"crypto/hmac"
	"crypto/rand"
	// G505: RFC 6238 fixes HMAC-SHA1 for interoperability with authenticator
	// applications. See this package's doc comment for why that is a
	// compatibility decision and not a weakness: HMAC's security rests on the
	// key, and the collision work on SHA-1 does not apply to it.
	"crypto/sha1" //nolint:gosec
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

// The parameters this package fixes. See the package doc for why none of them
// is configurable.
const (
	// Period is how long one code is valid.
	Period = 30 * time.Second
	// Digits is the length of a code.
	Digits = 6
	// SecretBytes is the length of a shared secret: 160 bits, the size RFC
	// 4226 recommends for an HMAC-SHA1 key.
	SecretBytes = 20
	// MaxSkewSteps is the largest number of periods either side of the
	// present that Verify will accept. Two periods is a minute of clock
	// drift in each direction, which is generous for a phone; more than that
	// is not clock drift, it is a longer validity window.
	MaxSkewSteps = 2
	// DefaultSkewSteps is the accepted drift when a caller has no reason to
	// choose. One period either side covers a phone with a slightly wrong
	// clock and a person who typed the last digit as the code changed.
	DefaultSkewSteps = 1
)

// Errors this package reports.
var (
	// ErrSecret reports a secret that is not a usable shared secret.
	ErrSecret = errors.New("the value is not a usable TOTP secret")
	// ErrCode reports a submitted value that is not shaped like a code. It
	// is not "the code is wrong": a caller must answer both the same way,
	// and the distinction exists so a malformed submission does not consume
	// the same audit vocabulary as a failed guess.
	ErrCode = errors.New("the value is not shaped like a one-time code")
)

// secretEncoding is the alphabet authenticator applications expect: RFC 4648
// base32, uppercase, without padding. Padding characters in a provisioning URI
// are accepted by some applications and rejected by others.
var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Secret is one account's shared secret.
//
// It is unprintable through every formatting and serialization path Go offers.
// A secret in a log line is a second factor an attacker can compute codes from
// forever, which is worse than a leaked code and quieter than a leaked
// password.
type Secret struct {
	key []byte
}

// NewSecret generates a shared secret from the cryptographic source.
func NewSecret() (Secret, error) {
	key := make([]byte, SecretBytes)
	if _, err := rand.Read(key); err != nil {
		return Secret{}, fmt.Errorf("generating a TOTP secret: %w", err)
	}
	return Secret{key: key}, nil
}

// ParseSecret reads a secret from its base32 form, as stored or as scanned.
//
// It accepts the spacing and lowercase a person retypes by hand, and padding
// an application may have added, because those are transcription differences
// rather than different secrets.
func ParseSecret(encoded string) (Secret, error) {
	cleaned := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(encoded))
	if cleaned == "" {
		return Secret{}, fmt.Errorf("%w: it is empty", ErrSecret)
	}
	key, err := secretEncoding.DecodeString(cleaned)
	if err != nil {
		return Secret{}, fmt.Errorf("%w: it is not base32", ErrSecret)
	}
	return SecretFromBytes(key)
}

// SecretFromBytes adopts stored key material.
func SecretFromBytes(key []byte) (Secret, error) {
	if len(key) != SecretBytes {
		return Secret{}, fmt.Errorf("%w: %d bytes, want %d", ErrSecret, len(key), SecretBytes)
	}
	adopted := make([]byte, SecretBytes)
	copy(adopted, key)
	return Secret{key: adopted}, nil
}

// IsZero reports whether s holds no key material.
func (s Secret) IsZero() bool { return len(s.key) == 0 }

// Bytes returns a copy of the key material, for the one moment it is written
// to storage. It is a named accessor rather than an exported field so that
// every place the secret escapes is greppable.
func (s Secret) Bytes() []byte {
	out := make([]byte, len(s.key))
	copy(out, s.key)
	return out
}

// Base32 returns the secret in the form an authenticator application accepts,
// for the one moment it is shown to the person enrolling.
func (s Secret) Base32() string {
	if s.IsZero() {
		return ""
	}
	return secretEncoding.EncodeToString(s.key)
}

// The redaction surface. Every path Go might take to render a Secret answers
// with a placeholder; Base32 and Bytes are the two deliberate exits.
func (s Secret) String() string               { return "[REDACTED]" }
func (s Secret) GoString() string             { return "[REDACTED]" }
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (s Secret) MarshalText() ([]byte, error) { return []byte("[REDACTED]"), nil }
func (s Secret) LogValue() slog.Value         { return slog.StringValue("[REDACTED]") }

// ProvisioningURI is the otpauth:// URI an authenticator application scans.
//
// issuer names the application and account names the person, both as they will
// appear in the authenticator's list. Both are escaped, and a colon in either
// is refused rather than escaped: the label before the "?" is itself
// colon-separated, and an application that splits on the first colon would
// show the wrong name for an account whose address contained one.
func (s Secret) ProvisioningURI(issuer, account string) (string, error) {
	switch {
	case s.IsZero():
		return "", fmt.Errorf("%w: it is empty", ErrSecret)
	case issuer == "" || account == "":
		return "", errors.New("totp: a provisioning URI needs both an issuer and an account")
	case strings.ContainsAny(issuer, ":\n\r") || strings.ContainsAny(account, ":\n\r"):
		return "", errors.New("totp: the issuer and account must not contain a colon or a line break")
	}
	query := url.Values{}
	query.Set("secret", s.Base32())
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(Digits))
	query.Set("period", fmt.Sprint(int(Period.Seconds())))
	label := url.PathEscape(issuer + ":" + account)
	return "otpauth://totp/" + label + "?" + query.Encode(), nil
}

// Step is the counter value RFC 6238 derives from a moment.
//
// It is exported because it is the replay window: a caller stores the step a
// code was accepted at and refuses anything at or below it, which is what
// stops the same code being used twice inside its thirty seconds.
func Step(at time.Time) int64 {
	return at.Unix() / int64(Period.Seconds())
}

// StartOfStep is when a step began, for a caller that wants to say how long a
// code has left.
func StartOfStep(step int64) time.Time {
	return time.Unix(step*int64(Period.Seconds()), 0).UTC()
}

// Code returns the code for one step.
func (s Secret) Code(step int64) (string, error) {
	if s.IsZero() {
		return "", fmt.Errorf("%w: it is empty", ErrSecret)
	}
	// A step before the epoch is not a moment this application can have
	// observed, and the counter RFC 4226 defines is unsigned. Refusing here
	// keeps a negative clock from wrapping into a valid-looking counter.
	if step < 0 {
		return "", fmt.Errorf("totp: step %d is before the epoch", step)
	}
	counterValue := uint64(step)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], counterValue)

	//nolint:gosec // G401: HMAC-SHA1 is what RFC 6238 and every authenticator
	// application implement. See the package doc.
	mac := hmac.New(sha1.New, s.key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	// RFC 4226 dynamic truncation: the low nibble of the last byte selects
	// the four-byte window, whose top bit is cleared so the result is
	// positive on every platform.
	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", Digits, truncated%pow10(Digits)), nil
}

// pow10 is ten to the n, for n small enough that the result is exact.
func pow10(n int) uint32 {
	result := uint32(1)
	for range n {
		result *= 10
	}
	return result
}

// NormalizeCode trims the spacing an authenticator application shows and a
// person copies, and refuses anything that is not exactly Digits ASCII digits.
//
// It does not strip arbitrary punctuation. Accepting "1-2-3-4-5-6" would mean
// accepting a value the person did not read anywhere, and every character
// class quietly discarded is a class an attacker can pad a guess with.
func NormalizeCode(submitted string) (string, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(submitted), " ", "")
	if len(cleaned) != Digits {
		return "", fmt.Errorf("%w: %d characters, want %d", ErrCode, len(cleaned), Digits)
	}
	for _, r := range cleaned {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("%w: it is not all digits", ErrCode)
		}
	}
	return cleaned, nil
}

// Verify checks a submitted code against the steps around a moment, and
// reports the step it matched.
//
// The returned step is the point of the two-value form: the caller stores it
// and refuses anything at or below it next time, so a code observed over a
// shoulder or replayed from a proxy log cannot be used a second time inside
// the window it is still arithmetically valid for.
//
// Every candidate step is evaluated rather than returning at the first match,
// and each comparison is subtle.ConstantTimeCompare, so the work done does not
// depend on the submitted digits.
func (s Secret) Verify(submitted string, at time.Time, skewSteps int) (int64, bool) {
	if s.IsZero() || skewSteps < 0 || skewSteps > MaxSkewSteps {
		return 0, false
	}
	cleaned, err := NormalizeCode(submitted)
	if err != nil {
		return 0, false
	}

	current := Step(at)
	matched := 0
	var matchedStep int64
	for offset := -skewSteps; offset <= skewSteps; offset++ {
		step := current + int64(offset)
		candidate, err := s.Code(step)
		if err != nil {
			return 0, false
		}
		equal := subtle.ConstantTimeCompare([]byte(candidate), []byte(cleaned))
		if equal == 1 {
			matchedStep = step
		}
		matched |= equal
	}
	if matched != 1 {
		return 0, false
	}
	return matchedStep, true
}
