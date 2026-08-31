package totp_test

import (
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/modules/totp"
)

// rfc6238Secret is the shared secret from RFC 6238's test vectors: the ASCII
// string "12345678901234567890", which is exactly twenty bytes.
const rfc6238Secret = "12345678901234567890"

func rfcSecret(t *testing.T) totp.Secret {
	t.Helper()

	secret, err := totp.SecretFromBytes([]byte(rfc6238Secret))
	if err != nil {
		t.Fatalf("SecretFromBytes: %v", err)
	}
	return secret
}

// The published SHA-1 test vectors from RFC 6238 Appendix B, truncated to the
// six digits this package emits. They are the only real proof that this is
// TOTP and not something that merely looks like it.
func TestRFC6238TestVectors(t *testing.T) {
	t.Parallel()

	secret := rfcSecret(t)
	for _, testCase := range []struct {
		seconds int64
		want    string
	}{
		{seconds: 59, want: "287082"},
		{seconds: 1111111109, want: "081804"},
		{seconds: 1111111111, want: "050471"},
		{seconds: 1234567890, want: "005924"},
		{seconds: 2000000000, want: "279037"},
		{seconds: 20000000000, want: "353130"},
	} {
		t.Run(fmt.Sprint(testCase.seconds), func(t *testing.T) {
			t.Parallel()

			at := time.Unix(testCase.seconds, 0).UTC()
			code, err := secret.Code(totp.Step(at))
			if err != nil {
				t.Fatalf("Code: %v", err)
			}
			if code != testCase.want {
				t.Fatalf("Code(%d) = %s, want %s", testCase.seconds, code, testCase.want)
			}
		})
	}
}

func TestCodesAreSixDigitsAcrossManySteps(t *testing.T) {
	t.Parallel()

	secret, err := totp.NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	// A code with a leading zero must keep it. Formatting the truncation as a
	// plain integer would produce a five-character code roughly one time in
	// ten, which an authenticator would show and a naive comparison would
	// refuse.
	sawLeadingZero := false
	for step := range int64(2000) {
		code, err := secret.Code(step)
		if err != nil {
			t.Fatalf("Code(%d): %v", step, err)
		}
		if len(code) != totp.Digits {
			t.Fatalf("Code(%d) = %q, want %d characters", step, code, totp.Digits)
		}
		if strings.HasPrefix(code, "0") {
			sawLeadingZero = true
		}
	}
	if !sawLeadingZero {
		t.Error("no code in two thousand steps began with a zero; the padding is not being exercised")
	}
}

func TestVerifyAcceptsDriftAndReportsTheStep(t *testing.T) {
	t.Parallel()

	secret := rfcSecret(t)
	now := time.Unix(1111111111, 0).UTC()
	current := totp.Step(now)

	for _, testCase := range []struct {
		name  string
		step  int64
		skew  int
		want  bool
		match int64
	}{
		{name: "the present step", step: current, skew: 1, want: true, match: current},
		{name: "one step behind", step: current - 1, skew: 1, want: true, match: current - 1},
		{name: "one step ahead", step: current + 1, skew: 1, want: true, match: current + 1},
		{name: "two steps behind with no skew allowed", step: current - 2, skew: 1},
		{name: "two steps behind with two allowed", step: current - 2, skew: 2, want: true, match: current - 2},
		{name: "the present step with no skew", step: current, want: true, match: current},
		{name: "one step behind with no skew", step: current - 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			code, err := secret.Code(testCase.step)
			if err != nil {
				t.Fatalf("Code: %v", err)
			}
			step, ok := secret.Verify(code, now, testCase.skew)
			if ok != testCase.want {
				t.Fatalf("Verify = %t, want %t", ok, testCase.want)
			}
			if ok && step != testCase.match {
				t.Fatalf("matched step = %d, want %d", step, testCase.match)
			}
			if !ok && step != 0 {
				t.Fatalf("a refused code reported step %d", step)
			}
		})
	}
}

// The matched step is the replay window. Without it the same code works for
// its whole thirty seconds, which is long enough to read it over a shoulder or
// pull it out of a proxy log and use it.
func TestTheMatchedStepIsWhatStopsAReplay(t *testing.T) {
	t.Parallel()

	secret := rfcSecret(t)
	now := time.Unix(1111111111, 0).UTC()
	code, err := secret.Code(totp.Step(now))
	if err != nil {
		t.Fatalf("Code: %v", err)
	}

	step, ok := secret.Verify(code, now, totp.DefaultSkewSteps)
	if !ok {
		t.Fatal("the code did not verify")
	}
	// A caller storing `step` refuses anything at or below it. The same code
	// verifies again — arithmetically it is still valid — which is exactly why
	// the caller has to hold that number rather than trust the answer.
	again, ok := secret.Verify(code, now.Add(10*time.Second), totp.DefaultSkewSteps)
	if !ok || again != step {
		t.Fatalf("the second verification reported %d/%t, want %d/true", again, ok, step)
	}
}

func TestVerifyRefusesMalformedAndOutOfBoundsInput(t *testing.T) {
	t.Parallel()

	secret := rfcSecret(t)
	now := time.Unix(1111111111, 0).UTC()
	code, err := secret.Code(totp.Step(now))
	if err != nil {
		t.Fatalf("Code: %v", err)
	}

	for _, testCase := range []struct {
		name      string
		submitted string
		skew      int
	}{
		{name: "empty", skew: 1},
		{name: "too short", submitted: code[:5], skew: 1},
		{name: "too long", submitted: code + "0", skew: 1},
		{name: "not digits", submitted: "12a456", skew: 1},
		{name: "punctuated", submitted: "1-2-3-4-5-6", skew: 1},
		{name: "a negative skew", submitted: code, skew: -1},
		{name: "a skew past the maximum", submitted: code, skew: totp.MaxSkewSteps + 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := secret.Verify(testCase.submitted, now, testCase.skew); ok {
				t.Fatal("Verify accepted input it should refuse")
			}
		})
	}

	// A secret that was never constructed verifies nothing, rather than
	// verifying everything.
	var empty totp.Secret
	if _, ok := empty.Verify(code, now, totp.DefaultSkewSteps); ok {
		t.Fatal("the zero secret accepted a code")
	}
}

func TestNormalizeCode(t *testing.T) {
	t.Parallel()

	if got, err := totp.NormalizeCode("  123 456 "); err != nil || got != "123456" {
		t.Fatalf("NormalizeCode = %q, %v", got, err)
	}
	for _, submitted := range []string{"", "12345", "1234567", "12345x", "12 34 5"} {
		if _, err := totp.NormalizeCode(submitted); !errors.Is(err, totp.ErrCode) {
			t.Errorf("NormalizeCode(%q) error = %v, want ErrCode", submitted, err)
		}
	}
}

func TestSecretRoundTripsAndTolerateTranscription(t *testing.T) {
	t.Parallel()

	secret, err := totp.NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	encoded := secret.Base32()
	if len(encoded) != 32 {
		t.Fatalf("Base32() = %d characters, want 32", len(encoded))
	}
	if strings.Contains(encoded, "=") {
		t.Error("the encoded secret is padded; some authenticators refuse padding")
	}

	for _, form := range []string{
		encoded,
		strings.ToLower(encoded),
		encoded[:8] + " " + encoded[8:],
		encoded[:8] + "-" + encoded[8:],
		base32.StdEncoding.EncodeToString(secret.Bytes()),
	} {
		parsed, err := totp.ParseSecret(form)
		if err != nil {
			t.Fatalf("ParseSecret(%q): %v", form, err)
		}
		if parsed.Base32() != encoded {
			t.Fatalf("ParseSecret(%q) round-tripped to a different secret", form)
		}
	}

	for _, form := range []string{"", "not base32!", encoded[:16]} {
		if _, err := totp.ParseSecret(form); !errors.Is(err, totp.ErrSecret) {
			t.Errorf("ParseSecret(%q) error = %v, want ErrSecret", form, err)
		}
	}
}

// A secret in a log line is a second factor an attacker can compute codes from
// forever. Every formatting path has to be closed, not most of them.
func TestSecretIsUnprintable(t *testing.T) {
	t.Parallel()

	secret, err := totp.NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	encoded := secret.Base32()

	rendered := []string{
		fmt.Sprintf("%v %s %q %#v", secret, secret, secret, secret),
		fmt.Sprint(secret),
		secret.LogValue().String(),
	}
	asJSON, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rendered = append(rendered, string(asJSON))

	asText, err := secret.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	rendered = append(rendered, string(asText))

	// A struct that merely contains one must not leak it either.
	type holder struct {
		Secret totp.Secret `json:"secret"`
	}
	nested, err := json.Marshal(holder{Secret: secret})
	if err != nil {
		t.Fatalf("Marshal(holder): %v", err)
	}
	rendered = append(rendered, string(nested))
	rendered = append(rendered, slog.AnyValue(secret).String())

	for _, out := range rendered {
		if strings.Contains(out, encoded) {
			t.Errorf("a formatting path rendered the secret: %s", out)
		}
		if !strings.Contains(out, "REDACTED") {
			t.Errorf("a formatting path produced %q, which does not say it was redacted", out)
		}
	}
}

func TestProvisioningURI(t *testing.T) {
	t.Parallel()

	secret := rfcSecret(t)
	raw, err := secret.ProvisioningURI("Example App", "person@example.test")
	if err != nil {
		t.Fatalf("ProvisioningURI: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("the URI does not parse: %v", err)
	}
	if parsed.Scheme != "otpauth" || parsed.Host != "totp" {
		t.Fatalf("scheme/host = %s://%s, want otpauth://totp", parsed.Scheme, parsed.Host)
	}
	if parsed.Path != "/Example App:person@example.test" {
		t.Fatalf("label = %q", parsed.Path)
	}
	query := parsed.Query()
	for field, want := range map[string]string{
		"secret":    secret.Base32(),
		"issuer":    "Example App",
		"algorithm": "SHA1",
		"digits":    "6",
		"period":    "30",
	} {
		if got := query.Get(field); got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}

	// A colon in either name is refused rather than escaped: the label is
	// itself colon-separated, and an authenticator splitting on the first one
	// would show the wrong account.
	for _, testCase := range [][2]string{
		{"Example:App", "person@example.test"},
		{"Example App", "person:1@example.test"},
		{"", "person@example.test"},
		{"Example App", ""},
		{"Example App", "person\n@example.test"},
	} {
		if _, err := secret.ProvisioningURI(testCase[0], testCase[1]); err == nil {
			t.Errorf("ProvisioningURI(%q, %q) was accepted", testCase[0], testCase[1])
		}
	}

	var empty totp.Secret
	if _, err := empty.ProvisioningURI("Example App", "person@example.test"); !errors.Is(err, totp.ErrSecret) {
		t.Errorf("the zero secret produced a URI: %v", err)
	}
}

func TestStepAndStartOfStep(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 31, 12, 0, 15, 0, time.UTC)
	step := totp.Step(at)
	start := totp.StartOfStep(step)

	if start.After(at) || at.Sub(start) >= totp.Period {
		t.Fatalf("StartOfStep(%d) = %s, which does not contain %s", step, start, at)
	}
	if totp.Step(at.Add(totp.Period)) != step+1 {
		t.Error("a period later is not the next step")
	}
	if totp.Step(at.Add(-totp.Period)) != step-1 {
		t.Error("a period earlier is not the previous step")
	}
	if start.Location() != time.UTC {
		t.Errorf("StartOfStep returned %s, want UTC", start.Location())
	}
}

func TestCodeRefusesAStepBeforeTheEpoch(t *testing.T) {
	t.Parallel()

	secret := rfcSecret(t)
	if _, err := secret.Code(-1); err == nil {
		t.Fatal("Code accepted a step before the epoch")
	}
	var empty totp.Secret
	if _, err := empty.Code(0); !errors.Is(err, totp.ErrSecret) {
		t.Fatalf("the zero secret produced a code: %v", err)
	}
}
