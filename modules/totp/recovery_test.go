package totp_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/modules/totp"
)

func TestNewRecoveryCodesShapeAndUniqueness(t *testing.T) {
	t.Parallel()

	codes, err := totp.NewRecoveryCodes()
	if err != nil {
		t.Fatalf("NewRecoveryCodes: %v", err)
	}
	if len(codes) != totp.RecoveryCodeCount {
		t.Fatalf("issued %d codes, want %d", len(codes), totp.RecoveryCodeCount)
	}

	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		value := code.Value()
		if len(value) != totp.RecoveryCodeCharacters+1 {
			t.Fatalf("code %q has %d characters, want %d", value, len(value), totp.RecoveryCodeCharacters+1)
		}
		if value[totp.RecoveryCodeGroup] != '-' {
			t.Fatalf("code %q is not grouped with a hyphen", value)
		}
		for _, r := range strings.ReplaceAll(value, "-", "") {
			if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", r) {
				t.Fatalf("code %q uses a character outside the alphabet", value)
			}
		}
		if seen[value] {
			t.Fatalf("two codes in one set are identical: %q", value)
		}
		seen[value] = true
	}

	// Two sets must not overlap either.
	second, err := totp.NewRecoveryCodes()
	if err != nil {
		t.Fatalf("NewRecoveryCodes: %v", err)
	}
	for _, code := range second {
		if seen[code.Value()] {
			t.Fatalf("a second set repeated %q", code.Value())
		}
	}
}

// Somebody typing a code off a printed sheet is transcribing, not
// authenticating. The characters the alphabet excludes are exactly the ones
// that get mistaken for digits, and they are mapped rather than refused.
func TestParseRecoveryCodeToleratesTranscription(t *testing.T) {
	t.Parallel()

	codes, err := totp.NewRecoveryCodes()
	if err != nil {
		t.Fatalf("NewRecoveryCodes: %v", err)
	}
	canonical := codes[0].Value()
	bare := strings.ReplaceAll(canonical, "-", "")

	for _, form := range []string{
		canonical,
		bare,
		strings.ToLower(canonical),
		" " + canonical + " ",
		bare[:5] + " " + bare[5:],
	} {
		parsed, err := totp.ParseRecoveryCode(form)
		if err != nil {
			t.Fatalf("ParseRecoveryCode(%q): %v", form, err)
		}
		if parsed.Value() != canonical {
			t.Fatalf("ParseRecoveryCode(%q) = %q, want %q", form, parsed.Value(), canonical)
		}
		if !bytes.Equal(parsed.Digest(), codes[0].Digest()) {
			t.Fatalf("ParseRecoveryCode(%q) produced a different digest", form)
		}
	}

	// I, L, and O are not in the alphabet, so a code containing them was
	// misread and the reading is unambiguous.
	confusable, err := totp.ParseRecoveryCode("OI2LO-67890")
	if err != nil {
		t.Fatalf("ParseRecoveryCode with confusable characters: %v", err)
	}
	if confusable.Value() != "01210-67890" {
		t.Fatalf("confusable characters mapped to %q", confusable.Value())
	}
}

func TestParseRecoveryCodeRefusesWhatIsNotOne(t *testing.T) {
	t.Parallel()

	for _, submitted := range []string{
		"",
		"12345",
		"12345-678901",
		"12345-6789!",
		"UUUUU-UUUUU",
	} {
		if _, err := totp.ParseRecoveryCode(submitted); !errors.Is(err, totp.ErrRecoveryCode) {
			t.Errorf("ParseRecoveryCode(%q) error = %v, want ErrRecoveryCode", submitted, err)
		}
	}
}

// The digest is over the significant characters only, so how a code was
// printed cannot change what is stored.
func TestRecoveryCodeDigestIgnoresPresentation(t *testing.T) {
	t.Parallel()

	codes, err := totp.NewRecoveryCodes()
	if err != nil {
		t.Fatalf("NewRecoveryCodes: %v", err)
	}
	code := codes[0]
	bare := strings.ReplaceAll(code.Value(), "-", "")
	want := sha256.Sum256([]byte(bare))

	if !bytes.Equal(code.Digest(), want[:]) {
		t.Fatal("the digest is not SHA-256 of the significant characters")
	}
	if len(code.Digest()) != sha256.Size {
		t.Fatalf("digest is %d bytes, want %d", len(code.Digest()), sha256.Size)
	}

	var empty totp.RecoveryCode
	if !empty.IsZero() || empty.Digest() != nil {
		t.Error("the zero code reports a digest")
	}

	// Two codes must not share a digest.
	if bytes.Equal(codes[0].Digest(), codes[1].Digest()) {
		t.Error("two codes share a digest")
	}
}

func TestRecoveryCodeIsUnprintable(t *testing.T) {
	t.Parallel()

	codes, err := totp.NewRecoveryCodes()
	if err != nil {
		t.Fatalf("NewRecoveryCodes: %v", err)
	}
	code := codes[0]
	value := code.Value()

	rendered := []string{
		fmt.Sprintf("%v %s %q %#v", code, code, code, code),
		fmt.Sprint(code),
		code.LogValue().String(),
	}
	asJSON, err := json.Marshal(code)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rendered = append(rendered, string(asJSON))
	asText, err := code.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	rendered = append(rendered, string(asText))

	// A whole set marshalled at once must not leak either, which is the shape
	// a handler returning the list would use.
	set, err := json.Marshal(codes)
	if err != nil {
		t.Fatalf("Marshal(set): %v", err)
	}
	rendered = append(rendered, string(set))

	for _, out := range rendered {
		if strings.Contains(out, value) {
			t.Errorf("a formatting path rendered the code: %s", out)
		}
		if !strings.Contains(out, "REDACTED") {
			t.Errorf("a formatting path produced %q, which does not say it was redacted", out)
		}
	}
}
