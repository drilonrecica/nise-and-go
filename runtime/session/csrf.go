package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// CSRF names the request header and the cookie suffix this package's token
// travels in.
const (
	// CSRFHeaderName is the header a browser client echoes the token in.
	CSRFHeaderName = "X-CSRF-Token"
	// CSRFCookieSuffix is appended to the session cookie's name to name the
	// cookie the token is delivered in. Deriving it keeps the two names
	// together when an application renames the session cookie.
	CSRFCookieSuffix = "_csrf"
	// csrfContext separates this derivation from any other use of the
	// session token as a key, so a future second derived value cannot
	// collide with this one.
	csrfContext = "nise-csrf-v1"
)

// CSRFToken derives the anti-forgery token bound to one session credential.
//
// The derivation uses the session token itself as the key, which is what makes
// the result bound to the session with no server-side key, no stored column,
// and no lifetime of its own: it changes exactly when the session does, and a
// token derived from one session cannot verify against another.
//
// The value is safe to hand the browser in a readable cookie. Knowing it does
// not let anyone derive the session token — the derivation is one-way — and
// script on another origin can neither read that cookie nor set the header the
// token has to arrive in.
func CSRFToken(token Token) string {
	if token.IsZero() {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(token.value))
	// A hash writer never fails and never writes short.
	_, _ = mac.Write([]byte(csrfContext))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCSRF reports whether presented is the token bound to this credential.
//
// The comparison is constant time. It is not a secret an attacker can grind at
// remotely, but a comparison whose duration depends on how much of a guess was
// right is a habit worth not having in an authentication path.
func VerifyCSRF(token Token, presented string) bool {
	if token.IsZero() || presented == "" || len(presented) > MaxTokenBytes {
		return false
	}
	expected := CSRFToken(token)
	if expected == "" {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(presented))
}

// CSRFName returns the cookie the anti-forgery token is delivered in.
//
// It shares the session cookie's prefix and attributes except for HttpOnly:
// the browser's own script has to read this one to echo it back, which is the
// entire mechanism.
func (p CookiePolicy) CSRFName() string {
	if p.name == "" {
		return ""
	}
	return p.name + CSRFCookieSuffix
}

// SafeMethod reports whether an HTTP method is one that must not change state,
// and therefore needs no anti-forgery check.
//
// The set is RFC 9110's: a request outside it is treated as unsafe, so a method
// nobody anticipated is checked rather than exempt.
func SafeMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "OPTIONS", "TRACE":
		return true
	default:
		return false
	}
}
