package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectLogsInGenerically(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}

	wants := map[string][]string{
		"internal/features/auth/login.go": {
			"var ErrInvalidCredentials = errors.New(",
			"func (c *Credentials) Authenticate(ctx context.Context, email, secret string) (Login, error)",
			"c.policy.VerifyDummy(secret)",
			"verification.NeedsRehash",
			"func (c *Credentials) ChangePassword(",
			"func (c *Credentials) SetPassword(",
			"OutcomeUnverifiable",
		},
		"internal/platform/passwords/passwords.go": {
			"MinLength = 12",
			"func Check(ctx context.Context, secret, email string, compromised password.Compromised) error",
			"ErrCheckUnavailable",
			"ErrDerivedFromAddress",
			"password.BuiltinCommonList()",
		},
		"internal/features/auth/login_test.go": {
			"TestEveryFailureLooksTheSameToTheCaller",
			"TestAnUnknownAddressCostsWhatARealOneCosts",
			"TestSuccessfulLoginRehashesAnOutdatedRecord",
			"TestChangePasswordEndsOtherSessions",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	login := content["internal/features/auth/login.go"]

	// The status check must come after verification, or a disabled account is
	// distinguishable from a wrong password by how long the answer took.
	verify := strings.Index(login, "c.policy.Verify(credential.PasswordHash, secret)")
	status := strings.Index(login, "credential.Status != StatusActive")
	if verify < 0 || status < 0 {
		t.Fatal("the login path does not verify and check status")
	}
	if verify > status {
		t.Error("the account status is checked before the password is verified")
	}

	// There must be exactly one error for every *credential* failure, so a
	// third would eventually be returned to distinguish a case. ErrThrottled
	// is the one legitimate second error: it is not a statement about the
	// credentials at all, and a caller answers it with a retry hint rather
	// than a rejection.
	declared := strings.Count(login, "= errors.New(")
	if declared != 2 {
		t.Errorf("the login path declares %d caller-visible errors, want ErrInvalidCredentials and ErrThrottled", declared)
	}
	for _, required := range []string{"var ErrInvalidCredentials = errors.New(", "var ErrThrottled = errors.New("} {
		if !strings.Contains(login, required) {
			t.Errorf("the login path lacks %q", required)
		}
	}

	// The policy must not add composition rules: they push people towards
	// predictable substitutions rather than towards length.
	policy := content["internal/platform/passwords/passwords.go"]
	for _, forbidden := range []string{"IsDigit", "IsPunct", "IsUpper", "requireSymbol", "requireDigit"} {
		if strings.Contains(policy, forbidden) {
			t.Errorf("the password policy contains a composition rule: %q", forbidden)
		}
	}
}
