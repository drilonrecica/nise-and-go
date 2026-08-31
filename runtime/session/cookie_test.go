package session_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/session"
)

func TestCookiePolicyDefaultsAreHardened(t *testing.T) {
	t.Parallel()

	policy, err := session.NewCookiePolicy(true)
	if err != nil {
		t.Fatalf("NewCookiePolicy: %v", err)
	}
	if policy.Name() != "__Host-session" {
		t.Errorf("name = %q, want __Host-session", policy.Name())
	}
	if !policy.Secure() {
		t.Error("a secure policy is not Secure")
	}
	if policy.SameSite() != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", policy.SameSite())
	}
	if !policy.Hardened() {
		t.Error("the default policy is not reported as hardened")
	}
	if policy.IsZero() {
		t.Error("a constructed policy reports IsZero")
	}
	if !(session.CookiePolicy{}).IsZero() {
		t.Error("the zero policy does not report IsZero")
	}
}

func TestCookiePolicyEscapeHatchDropsThePrefixWithSecure(t *testing.T) {
	t.Parallel()

	// A browser refuses a __Host- cookie that is not Secure, so the two
	// cannot be separated: the escape hatch has to change the name too.
	policy, err := session.NewCookiePolicy(false)
	if err != nil {
		t.Fatalf("NewCookiePolicy: %v", err)
	}
	if policy.Name() != "session" {
		t.Errorf("name = %q, want the unprefixed name", policy.Name())
	}
	if policy.Secure() {
		t.Error("the insecure policy is Secure")
	}
	if policy.Hardened() {
		t.Error("the insecure policy reports itself as hardened")
	}
	// Everything that does not depend on the transport stays put.
	if policy.SameSite() != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax even on the escape hatch", policy.SameSite())
	}
}

func TestCookiePolicyRefusesAMismatchedName(t *testing.T) {
	t.Parallel()

	// A __Host- name over an insecure transport is a cookie the browser will
	// simply drop, and an unprefixed name over a secure one silently gives up
	// a browser-enforced guarantee. Both are refused rather than corrected.
	if _, err := session.NewCookiePolicy(false, session.WithCookieName("__Host-session")); !errors.Is(err, session.ErrCookiePolicy) {
		t.Errorf("an insecure __Host- cookie was accepted: %v", err)
	}
	if _, err := session.NewCookiePolicy(true, session.WithCookieName("session")); !errors.Is(err, session.ErrCookiePolicy) {
		t.Errorf("a secure policy accepted an unprefixed name: %v", err)
	}

	renamed, err := session.NewCookiePolicy(true, session.WithCookieName("__Host-myapp_session"))
	if err != nil {
		t.Fatalf("NewCookiePolicy: %v", err)
	}
	if renamed.Name() != "__Host-myapp_session" || !renamed.Hardened() {
		t.Fatalf("policy = %#v", renamed)
	}
}

func TestCookiePolicyRefusesUnusableNames(t *testing.T) {
	t.Parallel()

	rejected := map[string]string{
		"empty":            "",
		"just the prefix":  session.HostPrefix,
		"with a space":     "__Host- session",
		"with a semicolon": "__Host-session;x",
		"with an equals":   "__Host-session=x",
		"with a comma":     "__Host-session,x",
		"with a newline":   "__Host-session\n",
		"non-ASCII":        "__Host-sessioné",
		"oversized":        session.HostPrefix + strings.Repeat("s", session.MaxCookieNameBytes),
	}
	for label, name := range rejected {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			if _, err := session.NewCookiePolicy(true, session.WithCookieName(name)); !errors.Is(err, session.ErrCookiePolicy) {
				t.Fatalf("NewCookiePolicy(%q) error = %v, want ErrCookiePolicy", name, err)
			}
		})
	}
	// "__Host-" alone is a legal cookie name to Go but not a useful one, and
	// the prefix check must not be what accidentally permits it.
	if _, err := session.NewCookiePolicy(true, session.WithCookieName(session.HostPrefix)); err == nil {
		t.Error("a policy with only the prefix as its name was accepted")
	}
}

// TestCookiePolicyNameSurvivesGoSerialization proves the accepted name set is
// one net/http will actually emit: a name Go has to escape would arrive at the
// browser as something other than what was configured.
func TestCookiePolicyNameSurvivesGoSerialization(t *testing.T) {
	t.Parallel()

	for _, secure := range []bool{true, false} {
		policy, err := session.NewCookiePolicy(secure)
		if err != nil {
			t.Fatalf("NewCookiePolicy: %v", err)
		}
		cookie := &http.Cookie{
			Name:     policy.Name(),
			Value:    "ns1_" + strings.Repeat("A", 43),
			Path:     "/",
			HttpOnly: true,
			Secure:   policy.Secure(),
			SameSite: policy.SameSite(),
		}
		serialized := cookie.String()
		if serialized == "" {
			t.Fatalf("net/http refused to serialize the %q cookie", policy.Name())
		}
		if !strings.HasPrefix(serialized, policy.Name()+"=") {
			t.Fatalf("serialized = %q, want it to begin with the configured name", serialized)
		}
		if !strings.Contains(serialized, "HttpOnly") || !strings.Contains(serialized, "SameSite=Lax") || !strings.Contains(serialized, "Path=/") {
			t.Fatalf("serialized = %q, missing a required attribute", serialized)
		}
		if strings.Contains(serialized, "Domain=") {
			t.Fatalf("serialized = %q; a __Host- cookie may not carry Domain", serialized)
		}
		if secure != strings.Contains(serialized, "Secure") {
			t.Fatalf("serialized = %q for secure=%t", serialized, secure)
		}
	}
}
