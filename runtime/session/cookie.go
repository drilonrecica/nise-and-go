package session

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Cookie prefixes and the names built from them.
const (
	// HostPrefix is the browser-enforced `__Host-` prefix. A cookie carrying
	// it is refused by the browser unless it is Secure, has Path=/, and has
	// no Domain — which is what makes it impossible for a sibling subdomain,
	// or a network attacker on a related host, to write one.
	HostPrefix = "__Host-"
	// DefaultCookieName is the session cookie name, prefix included.
	DefaultCookieName = HostPrefix + "session"
	// InsecureCookieName is the name used when the prefix cannot be: a
	// browser rejects `__Host-` without Secure, so the escape hatch has to
	// change the name as well as the attribute.
	InsecureCookieName = "session"
	// MaxCookieNameBytes bounds a configured name.
	MaxCookieNameBytes = 64
)

// ErrCookiePolicy reports a cookie policy that a browser would refuse or that
// would not protect what it is supposed to.
var ErrCookiePolicy = errors.New("session cookie policy is not usable")

// CookiePolicy is the name and attributes of the session cookie.
//
// The zero value is unusable; construct one with [NewCookiePolicy].
type CookiePolicy struct {
	name     string
	secure   bool
	sameSite http.SameSite
}

// CookieOption configures a [CookiePolicy].
type CookieOption func(*CookiePolicy) error

// WithCookieName replaces the cookie's name.
//
// The name still has to match the transport: a `__Host-` name over an insecure
// policy, or an unprefixed name over a secure one, is refused rather than
// quietly corrected, because the prefix is a browser-enforced guarantee and a
// silent rename would remove it without saying so.
func WithCookieName(name string) CookieOption {
	return func(p *CookiePolicy) error {
		p.name = name
		return nil
	}
}

// NewCookiePolicy returns the policy for one deployment.
//
// secureTransport is whether the browser will treat this origin as a secure
// context. It is true in production without exception, and true in ordinary
// development too: http://localhost and http://127.0.0.1 are secure contexts
// in every current browser, so a development server on localhost carries the
// same `__Host-` cookie production does and exercises the same code path.
//
// Passing false is the escape hatch for a development origin that is not
// localhost — a LAN address, or a hostname pointed at a workstation. It drops
// the prefix and the Secure attribute together, because a browser refuses a
// `__Host-` cookie that is not Secure and the pair cannot be split. A caller
// that offers this must refuse it in production; this package cannot tell
// which environment it is in, and a policy object that guessed would be worse
// than one that is told.
func NewCookiePolicy(secureTransport bool, options ...CookieOption) (CookiePolicy, error) {
	policy := CookiePolicy{
		name:   DefaultCookieName,
		secure: secureTransport,
		// Lax rather than Strict: Strict withholds the cookie on a
		// top-level navigation from anywhere else, so following a link from
		// an email lands on a logged-out page. Lax plus a session-bound CSRF
		// token plus Origin checks is the design; Strict alone is not a
		// substitute for any of them.
		sameSite: http.SameSiteLaxMode,
	}
	if !secureTransport {
		policy.name = InsecureCookieName
	}
	for _, option := range options {
		if err := option(&policy); err != nil {
			return CookiePolicy{}, err
		}
	}
	if err := policy.validate(); err != nil {
		return CookiePolicy{}, err
	}
	return policy, nil
}

func (p CookiePolicy) validate() error {
	if len(p.name) < 1 || len(p.name) > MaxCookieNameBytes {
		return fmt.Errorf("%w: name is %d bytes, want 1..%d", ErrCookiePolicy, len(p.name), MaxCookieNameBytes)
	}
	for i := range len(p.name) {
		char := p.name[i]
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		case char == '_' || char == '-':
		default:
			return fmt.Errorf("%w: name contains a character a cookie name may not", ErrCookiePolicy)
		}
	}
	if strings.HasPrefix(p.name, HostPrefix) && len(p.name) == len(HostPrefix) {
		return fmt.Errorf("%w: the name is only the %s prefix", ErrCookiePolicy, HostPrefix)
	}
	if strings.HasPrefix(p.name, HostPrefix) != p.secure {
		if p.secure {
			return fmt.Errorf("%w: a secure policy must use the %s prefix", ErrCookiePolicy, HostPrefix)
		}
		return fmt.Errorf("%w: a browser refuses a %s cookie that is not Secure", ErrCookiePolicy, HostPrefix)
	}
	return nil
}

// Name returns the cookie's name.
func (p CookiePolicy) Name() string { return p.name }

// Secure reports whether the cookie carries the Secure attribute.
func (p CookiePolicy) Secure() bool { return p.secure }

// SameSite returns the cookie's SameSite mode.
func (p CookiePolicy) SameSite() http.SameSite { return p.sameSite }

// IsZero reports whether p is the unconstructed zero value.
func (p CookiePolicy) IsZero() bool { return p.name == "" }

// Hardened reports whether the policy is the full browser-enforced one: the
// `__Host-` prefix and Secure. A caller shows this to an operator; production
// refuses to start when it is false.
func (p CookiePolicy) Hardened() bool {
	return p.secure && strings.HasPrefix(p.name, HostPrefix)
}
