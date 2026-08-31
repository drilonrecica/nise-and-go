package totp

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// The recovery-code shape.
const (
	// RecoveryCodeCount is how many codes one account is issued at a time.
	// Ten is enough that losing a phone is survivable and few enough that a
	// person will store the list somewhere rather than ignore it.
	RecoveryCodeCount = 10
	// RecoveryCodeCharacters is the number of significant characters in one
	// code: fifty bits from the alphabet below. It is not a password and has
	// no dictionary to search, so fifty bits is far past guessable.
	RecoveryCodeCharacters = 10
	// RecoveryCodeGroup is how many characters appear before the hyphen. The
	// hyphen is presentation only and is discarded on the way in.
	RecoveryCodeGroup = 5
)

// recoveryAlphabet is Crockford's base32: the digits and the uppercase letters
// without I, L, O, and U. The first three are excluded because a person
// reading a printed code cannot reliably tell them from 1 and 0; U is excluded
// because its absence keeps the alphabet from spelling a short unfortunate
// word by accident.
const recoveryAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ErrRecoveryCode reports a value that is not shaped like a recovery code.
var ErrRecoveryCode = errors.New("the value is not shaped like a recovery code")

// RecoveryCode is one single-use code that stands in for an authenticator.
//
// Like a session token, it exists in the returned value and nowhere else: only
// its digest is stored, so the list cannot be recovered from the database, a
// backup, or a log line. It is unprintable through every formatting path;
// Value is the single deliberate exit, for the one moment the list is shown.
type RecoveryCode struct {
	value string
}

// NewRecoveryCodes generates a fresh set from the cryptographic source.
//
// The whole set is replaced together, never topped up. A set that could be
// extended would mean a code from an older list stayed valid after somebody
// regenerated their codes because they thought the old list had leaked.
func NewRecoveryCodes() ([]RecoveryCode, error) {
	codes := make([]RecoveryCode, 0, RecoveryCodeCount)
	for range RecoveryCodeCount {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func newRecoveryCode() (RecoveryCode, error) {
	raw := make([]byte, RecoveryCodeCharacters)
	if _, err := rand.Read(raw); err != nil {
		return RecoveryCode{}, fmt.Errorf("generating a recovery code: %w", err)
	}
	// The alphabet is exactly 32 characters, so masking the low five bits of
	// each byte is uniform. A modulo against a non-power-of-two alphabet
	// would not be, and "slightly biased" is not a thing to write in a
	// credential generator.
	var builder strings.Builder
	for i, b := range raw {
		if i > 0 && i%RecoveryCodeGroup == 0 {
			builder.WriteByte('-')
		}
		builder.WriteByte(recoveryAlphabet[b&0x1f])
	}
	return RecoveryCode{value: builder.String()}, nil
}

// ParseRecoveryCode reads a submitted code.
//
// It folds case, discards hyphens and spaces, and maps the characters the
// alphabet deliberately excludes onto the ones they are mistaken for: I and L
// read as 1, O reads as 0. Somebody typing a code off a printed sheet is
// transcribing, not authenticating, and refusing a transcription error would
// send them to support rather than into their account.
func ParseRecoveryCode(submitted string) (RecoveryCode, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '-', ' ':
			return -1
		case 'i', 'I', 'l', 'L':
			return '1'
		case 'o', 'O':
			return '0'
		}
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}, strings.TrimSpace(submitted))

	if len(cleaned) != RecoveryCodeCharacters {
		return RecoveryCode{}, fmt.Errorf("%w: %d characters, want %d", ErrRecoveryCode, len(cleaned), RecoveryCodeCharacters)
	}
	for _, r := range cleaned {
		if !strings.ContainsRune(recoveryAlphabet, r) {
			return RecoveryCode{}, fmt.Errorf("%w: it contains a character the alphabet does not use", ErrRecoveryCode)
		}
	}
	var builder strings.Builder
	for i, r := range cleaned {
		if i > 0 && i%RecoveryCodeGroup == 0 {
			builder.WriteByte('-')
		}
		builder.WriteRune(r)
	}
	return RecoveryCode{value: builder.String()}, nil
}

// Value returns the code as it is shown and typed. It is the single accessor,
// so every place a code escapes is greppable.
func (c RecoveryCode) Value() string { return c.value }

// IsZero reports whether c holds no code.
func (c RecoveryCode) IsZero() bool { return c.value == "" }

// Digest is what the server stores: SHA-256 of the significant characters,
// with the presentation hyphen removed so a stored digest does not depend on
// how the code was printed.
//
// It is a plain hash rather than a slow one because the code is fifty bits of
// uniform randomness from this package: there is no dictionary to search and
// nothing for a pepper to defend. Argon2id belongs to secrets a person chose.
func (c RecoveryCode) Digest() []byte {
	if c.IsZero() {
		return nil
	}
	sum := sha256.Sum256([]byte(strings.ReplaceAll(c.value, "-", "")))
	return sum[:]
}

// The redaction surface, matching Secret's. Value is the deliberate exit.
func (c RecoveryCode) String() string               { return "[REDACTED]" }
func (c RecoveryCode) GoString() string             { return "[REDACTED]" }
func (c RecoveryCode) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (c RecoveryCode) MarshalText() ([]byte, error) { return []byte("[REDACTED]"), nil }
func (c RecoveryCode) LogValue() slog.Value         { return slog.StringValue("[REDACTED]") }
