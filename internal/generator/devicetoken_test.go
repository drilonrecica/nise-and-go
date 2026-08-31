package generator_test

import (
	"strings"
	"testing"
)

// ADR 0024 designs native device tokens and deliberately builds none. These
// tests are what make that more than a note: the first pins the absence, and
// the rest pin the three properties of today's design that keep adding one an
// additive change rather than a rewrite.

// A generated application has exactly one API credential. An unused
// authentication path is an attack surface nobody exercises, and a reader of
// the code should find one credential, not two with a flag between them.
func TestNoDeviceTokenIsExposedYet(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"without modules", "with every module"} {
		options := defaultOptions()
		if name == "with every module" {
			options = allModulesOptions()
		}
		content := planContent(t, options)
		for path, body := range content {
			if strings.Contains(path, "device_token") || strings.Contains(path, "devicetoken") {
				t.Errorf("%s exists; ADR 0024 designs device tokens and builds none", path)
			}
			if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".sql") {
				continue
			}
			for _, forbidden := range []string{"device_tokens", "DeviceToken", "dev1_"} {
				if strings.Contains(body, forbidden) {
					t.Errorf("%s mentions %q in %s", path, forbidden, name)
				}
			}
		}
	}
}

// The anti-forgery guard must decide from the credential that actually
// authenticated the request, never from the presence of a header. A guard that
// skipped its checks when an Authorization header was present would let a
// cross-site fetch with an empty one through while the request still
// authenticated by cookie — which is the bypass adding a bearer source would
// otherwise introduce.
func TestTheGuardDecidesFromTheResolvedCredential(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())
	guard := content["internal/platform/httpauth/guard.go"]
	if guard == "" {
		t.Fatal("the anti-forgery guard is missing")
	}
	if !strings.Contains(guard, "presented, ok := g.cookies.Read(r)") {
		t.Error("the guard no longer reads the resolved session credential")
	}
	if strings.Contains(guard, "Authorization") {
		t.Error("the guard inspects the Authorization header; ADR 0024 requires it to follow how the request authenticated")
	}
}

// A second credential type must be a different prefix rather than an ambiguous
// parse, so the session token's shape stays prefixed and versioned.
func TestTheSessionTokenShapeStaysPrefixed(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())
	sessions := content["internal/features/auth/sessions.go"] + content["internal/features/auth/invitations.go"]
	if !strings.Contains(sessions, `InvitationTokenPrefix = "inv1"`) {
		t.Error("the invitation token lost its prefix; two credential types would then be one parse apart")
	}
}

// Identity is resolved once, in middleware, into one context value. A second
// credential source then adds a branch in one place, rather than a second
// notion of who is asking that the rest of the application has to learn.
func TestIdentityIsResolvedIntoOnePlace(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())
	sessionContext := content["internal/platform/httpauth/context.go"]
	if strings.Count(sessionContext, "type contextKey struct{}") != 1 {
		t.Error("the session identity no longer has exactly one context key")
	}
	if strings.Count(sessionContext, "func WithSession(") != 1 {
		t.Error("the session identity has more than one writer")
	}

	// And nothing outside that package writes one: the key is unexported, so
	// this is a check that no second package grew its own.
	for path, body := range content {
		if path == "internal/platform/httpauth/context.go" || !strings.HasSuffix(path, ".go") {
			continue
		}
		if strings.Contains(body, "func WithSession(") {
			t.Errorf("%s declares a second way to put a session in a request", path)
		}
	}
}
