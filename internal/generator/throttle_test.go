package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectThrottlesAuthentication(t *testing.T) {
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
		"db/migrations/00007_throttle.sql": {
			"CREATE TABLE throttle_counters",
			"subject      bytea       NOT NULL",
			"CONSTRAINT throttle_counters_subject_length CHECK (octet_length(subject) = 32)",
			"-- +goose Down",
		},
		"internal/platform/throttle/throttle.go": {
			"func (l *Limiter) Allow(ctx context.Context, address string, client netip.Addr) (Decision, error)",
			"func (l *Limiter) Reset(ctx context.Context, address string) error",
			"ON CONFLICT (scope, subject, window_start)",
			"DO UPDATE SET attempts = throttle_counters.attempts + 1",
			"func digest(subject string) []byte",
		},
		"internal/features/auth/login.go": {
			"c.throttle.Allow(ctx, email, forwarded.ClientIP(ctx))",
			"var ErrThrottled = errors.New(",
			"ActionSessionDenied = \"session.denied\"",
			"c.auditor.Record(ctx, event)",
		},
		"internal/platform/config/config.go": {
			"LoginThrottle throttle.Policy",
			`l.Int("LOGIN_ATTEMPTS_PER_ADDRESS"`,
			`l.Int("LOGIN_ATTEMPTS_PER_CLIENT"`,
			`l.Duration("LOGIN_THROTTLE_WINDOW"`,
		},
		"internal/features/auth/throttle_test.go": {
			"TestRepeatedFailuresAreThrottled",
			"TestASuccessfulLoginClearsTheAddressCounter",
			"TestThrottlingSaysNothingAboutWhetherTheAddressExists",
			"TestAuthenticationIsAudited",
		},
		".env.example": {
			"LOGIN_ATTEMPTS_PER_ADDRESS=10",
			"LOGIN_THROTTLE_WINDOW=15m",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The throttle must be consulted before any password work. A limiter
	// consulted afterwards stops the attacker getting in and does not stop
	// them tying up every worker in the process.
	login := content["internal/features/auth/login.go"]
	throttled := strings.Index(login, "c.throttle.Allow(")
	hashed := strings.Index(login, "c.policy.Verify(")
	dummy := strings.Index(login, "c.policy.VerifyDummy(")
	if throttled < 0 || hashed < 0 || dummy < 0 {
		t.Fatal("the login path does not throttle and hash")
	}
	if throttled > hashed || throttled > dummy {
		t.Error("the throttle runs after password hashing")
	}

	// The login use case must be impossible to construct without them.
	if !strings.Contains(login, "if limiter == nil || auditor == nil {") {
		t.Error("the login use case can be constructed without a throttle or an audit recorder")
	}

	// The counter table must never hold the submitted value.
	limiter := content["internal/platform/throttle/throttle.go"]
	if !strings.Contains(limiter, "sha256.Sum256([]byte(subject))") {
		t.Error("the throttle stores subjects in the clear")
	}
}
