package session_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/session"
)

func TestCSRFTokenIsBoundToItsSession(t *testing.T) {
	t.Parallel()

	first, err := session.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	second, err := session.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	firstCSRF := session.CSRFToken(first)
	if firstCSRF == "" {
		t.Fatal("a valid session produced no CSRF token")
	}
	if firstCSRF != session.CSRFToken(first) {
		t.Fatal("the derivation is not deterministic")
	}
	if firstCSRF == session.CSRFToken(second) {
		t.Fatal("two sessions share a CSRF token")
	}

	if !session.VerifyCSRF(first, firstCSRF) {
		t.Fatal("a session's own token did not verify")
	}
	if session.VerifyCSRF(second, firstCSRF) {
		t.Fatal("one session's token verified against another")
	}
}

func TestCSRFTokenDoesNotRevealTheSession(t *testing.T) {
	t.Parallel()

	token, err := session.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	csrf := session.CSRFToken(token)

	// The value is handed to the browser in a script-readable cookie, so it
	// must not contain the credential it was derived from, and must not equal
	// the digest the server stores either.
	if strings.Contains(csrf, token.Value()) {
		t.Fatal("the CSRF token contains the session token")
	}
	if strings.Contains(csrf, strings.TrimPrefix(token.Value(), session.TokenVersion+"_")) {
		t.Fatal("the CSRF token contains the session secret")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(csrf)
	if err != nil {
		t.Fatalf("the CSRF token is not strict base64url: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("the CSRF token carries %d bytes, want a full 32-byte tag", len(decoded))
	}
	if base64.RawURLEncoding.EncodeToString(token.Digest()) == csrf {
		t.Fatal("the CSRF token is the stored digest, which would make a database leak forge requests")
	}
}

func TestVerifyCSRFRejectsEverythingElse(t *testing.T) {
	t.Parallel()

	token, err := session.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	valid := session.CSRFToken(token)

	rejected := map[string]string{
		"empty":                "",
		"truncated":            valid[:len(valid)-1],
		"lengthened":           valid + "A",
		"one character off":    "A" + valid[1:],
		"the session token":    token.Value(),
		"oversized":            strings.Repeat("A", session.MaxTokenBytes+1),
		"padded base64":        base64.URLEncoding.EncodeToString(make([]byte, 32)),
		"whitespace around it": " " + valid + " ",
	}
	for name, presented := range rejected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if session.VerifyCSRF(token, presented) {
				t.Fatalf("VerifyCSRF accepted %q", presented)
			}
		})
	}

	// A zero session has no token, and must not accept the empty string as
	// one either.
	if session.CSRFToken(session.Token{}) != "" {
		t.Error("the zero session produced a CSRF token")
	}
	if session.VerifyCSRF(session.Token{}, "") || session.VerifyCSRF(session.Token{}, valid) {
		t.Error("the zero session verified a CSRF token")
	}
}

func TestCSRFCookieNameFollowsTheSessionCookie(t *testing.T) {
	t.Parallel()

	hardened, err := session.NewCookiePolicy(true)
	if err != nil {
		t.Fatalf("NewCookiePolicy: %v", err)
	}
	if got := hardened.CSRFName(); got != "__Host-session_csrf" {
		t.Errorf("CSRFName = %q, want __Host-session_csrf", got)
	}

	renamed, err := session.NewCookiePolicy(true, session.WithCookieName("__Host-myapp_session"))
	if err != nil {
		t.Fatalf("NewCookiePolicy: %v", err)
	}
	if got := renamed.CSRFName(); got != "__Host-myapp_session_csrf" {
		t.Errorf("CSRFName = %q; renaming the session cookie must rename this one too", got)
	}

	insecure, err := session.NewCookiePolicy(false)
	if err != nil {
		t.Fatalf("NewCookiePolicy: %v", err)
	}
	if got := insecure.CSRFName(); got != "session_csrf" {
		t.Errorf("CSRFName = %q, want the unprefixed name", got)
	}
	if (session.CookiePolicy{}).CSRFName() != "" {
		t.Error("the zero policy named a CSRF cookie")
	}
}

func TestSafeMethod(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"GET", "HEAD", "OPTIONS", "TRACE", "get", "head"} {
		if !session.SafeMethod(method) {
			t.Errorf("%q is not reported safe", method)
		}
	}
	// Anything unrecognized is unsafe, so a method nobody anticipated is
	// checked rather than exempt.
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "post", "PROPFIND", "PURGE", "", "0"} {
		if session.SafeMethod(method) {
			t.Errorf("%q is reported safe", method)
		}
	}
}
