package secure

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Response header names this package sets. They are exported so an
// application's own tests can assert on them without restating string
// literals that would drift.
const (
	// HeaderCSP carries an enforced Content-Security-Policy.
	HeaderCSP = "Content-Security-Policy"
	// HeaderCSPReportOnly carries a Content-Security-Policy that is reported
	// but not enforced. See [AllowReportOnly].
	HeaderCSPReportOnly = "Content-Security-Policy-Report-Only"
	// HeaderHSTS carries Strict-Transport-Security.
	HeaderHSTS = "Strict-Transport-Security"
	// HeaderContentTypeOptions carries X-Content-Type-Options.
	HeaderContentTypeOptions = "X-Content-Type-Options"
	// HeaderReferrerPolicy carries Referrer-Policy.
	HeaderReferrerPolicy = "Referrer-Policy"
	// HeaderCOOP carries Cross-Origin-Opener-Policy.
	HeaderCOOP = "Cross-Origin-Opener-Policy"
	// HeaderCORP carries Cross-Origin-Resource-Policy.
	HeaderCORP = "Cross-Origin-Resource-Policy"
	// HeaderCOEP carries Cross-Origin-Embedder-Policy, which this package
	// does not set unless [WithCrossOriginEmbedderPolicy] asks for it.
	HeaderCOEP = "Cross-Origin-Embedder-Policy"
	// HeaderPermissionsPolicy carries Permissions-Policy.
	HeaderPermissionsPolicy = "Permissions-Policy"
	// HeaderFrameOptions carries X-Frame-Options.
	HeaderFrameOptions = "X-Frame-Options"
	// HeaderCacheControl carries Cache-Control, which this package sets to
	// no-store on a response that carries a nonce. See [Nonce].
	HeaderCacheControl = "Cache-Control"
)

// DefaultHSTSMaxAge is the max-age a production policy declares for
// Strict-Transport-Security: one year, the value the HSTS preload list
// requires and the shortest value that survives a user who visits an
// application only occasionally.
const DefaultHSTSMaxAge = 365 * 24 * time.Hour

// ReferrerPolicy is a Referrer-Policy value. It is a closed set: an
// unrecognized string is rejected at construction rather than sent to
// browsers that would ignore it and fall back to their own default.
type ReferrerPolicy string

// The Referrer-Policy values this package recognizes.
const (
	// ReferrerNoReferrer sends no Referer header at all, same-origin
	// included.
	ReferrerNoReferrer ReferrerPolicy = "no-referrer"
	// ReferrerSameOrigin sends the full URL on same-origin requests and
	// nothing cross-origin. This is the default.
	ReferrerSameOrigin ReferrerPolicy = "same-origin"
	// ReferrerStrictOrigin sends only the origin, and only over TLS. It
	// leaks the application's hostname to third parties.
	ReferrerStrictOrigin ReferrerPolicy = "strict-origin"
	// ReferrerStrictOriginWhenCrossOrigin is the modern browser default: the
	// full URL same-origin, the bare origin cross-origin. It leaks the
	// application's hostname to third parties.
	ReferrerStrictOriginWhenCrossOrigin ReferrerPolicy = "strict-origin-when-cross-origin"
	// ReferrerOriginWhenCrossOrigin sends the origin cross-origin even when
	// downgrading to plain HTTP.
	ReferrerOriginWhenCrossOrigin ReferrerPolicy = "origin-when-cross-origin"
	// ReferrerOrigin sends the bare origin to everyone.
	ReferrerOrigin ReferrerPolicy = "origin"
	// ReferrerUnsafeURL sends the full URL to everyone, including over an
	// insecure transport. It leaks record identifiers and tenant names out
	// of the application.
	ReferrerUnsafeURL ReferrerPolicy = "unsafe-url"
)

// leaksReferrerCrossOrigin reports whether p sends any referrer information
// to a cross-origin destination, and therefore requires an explicit,
// reason-carrying waiver.
func (p ReferrerPolicy) leaksReferrerCrossOrigin() bool {
	switch p {
	case ReferrerNoReferrer, ReferrerSameOrigin:
		return false
	default:
		return true
	}
}

// valid reports whether p is one of the recognized values.
func (p ReferrerPolicy) valid() bool {
	switch p {
	case ReferrerNoReferrer, ReferrerSameOrigin, ReferrerStrictOrigin,
		ReferrerStrictOriginWhenCrossOrigin, ReferrerOriginWhenCrossOrigin,
		ReferrerOrigin, ReferrerUnsafeURL:
		return true
	default:
		return false
	}
}

// ResourcePolicy is a Cross-Origin-Resource-Policy value.
type ResourcePolicy string

// The Cross-Origin-Resource-Policy values this package recognizes.
const (
	// ResourceSameOrigin lets only this exact origin load the response as a
	// subresource. This is the default.
	ResourceSameOrigin ResourcePolicy = "same-origin"
	// ResourceSameSite lets any same-site origin load the response.
	ResourceSameSite ResourcePolicy = "same-site"
	// ResourceCrossOrigin lets any origin on the web load the response.
	ResourceCrossOrigin ResourcePolicy = "cross-origin"
)

// EmbedderPolicy is a Cross-Origin-Embedder-Policy value.
type EmbedderPolicy string

// The Cross-Origin-Embedder-Policy values this package recognizes. Neither is
// set by default; see [WithCrossOriginEmbedderPolicy].
const (
	// EmbedderRequireCorp refuses every cross-origin subresource that does
	// not explicitly opt in with CORP or CORS.
	EmbedderRequireCorp EmbedderPolicy = "require-corp"
	// EmbedderCredentialless loads cross-origin subresources without
	// credentials instead of refusing them outright.
	//
	//nolint:gosec // G101 matches the identifier against its credential-word
	// list and sees a hardcoded secret. This is the literal token the
	// Cross-Origin-Embedder-Policy header takes; it is not a credential, and
	// the value is fixed by the specification rather than chosen here.
	EmbedderCredentialless EmbedderPolicy = "credentialless"
)

// Waiver records one deliberate weakening of the default policy: which
// control was weakened, what it was weakened to, and the reason the
// application gave for it.
//
// Every option in this package whose name begins with Allow produces a
// Waiver, and no other way of weakening a control exists. An application can
// therefore enumerate its own weakenings — log them at startup, or assert in
// a test that there are none:
//
//	if got := len(policy.Waivers()); got != 0 {
//	    t.Fatalf("policy carries %d security waivers: %v", got, policy.Waivers())
//	}
type Waiver struct {
	// Control is the header or CSP directive that was weakened, for example
	// "style-src-attr" or "strict-transport-security".
	Control string
	// Detail is what the control was weakened to, for example
	// "'unsafe-inline'".
	Detail string
	// Reason is the explanation the application supplied. It is never empty:
	// an Allow option with an empty reason fails construction.
	Reason string
}

// String renders the waiver as one line suitable for a startup log.
func (w Waiver) String() string {
	return fmt.Sprintf("%s weakened to %s: %s", w.Control, w.Detail, w.Reason)
}

// profile distinguishes the two shapes of policy this package builds.
type profile int

const (
	profileDocument profile = iota
	profileAPI
)

// Policy is a validated, immutable set of security response headers. Build
// one with [NewDocumentPolicy] or [NewAPIPolicy] and hand it to [Middleware].
//
// A Policy cannot be modified after construction and has no exported fields.
// That is deliberate: a settable field is a protection an application can
// remove with an assignment that reads like configuration, and nothing in the
// resulting code says a protection was dropped. Every change goes through a
// named option instead, and every weakening leaves a [Waiver] behind.
type Policy struct {
	deployment Deployment
	reportOnly bool

	directives   []directive
	cspNoNonce   string
	nonceEnabled bool

	hsts        string
	referrer    string
	coop        string
	corp        string
	coep        string
	permissions string
	frameOption string

	waivers []Waiver
}

// Deployment returns the deployment this policy was built for.
func (p *Policy) Deployment() Deployment { return p.deployment }

// ReportOnly reports whether this policy's CSP is reported but not enforced.
// See [AllowReportOnly].
func (p *Policy) ReportOnly() bool { return p.reportOnly }

// CSPHeaderName returns the header name this policy's CSP is sent under:
// [HeaderCSPReportOnly] when the policy is report-only, [HeaderCSP]
// otherwise.
func (p *Policy) CSPHeaderName() string {
	if p.reportOnly {
		return HeaderCSPReportOnly
	}
	return HeaderCSP
}

// ContentSecurityPolicy returns the policy's CSP header value as sent on a
// response that carries no nonce. A response whose handler called [Nonce]
// carries the same value with a nonce source added to script-src.
func (p *Policy) ContentSecurityPolicy() string { return p.cspNoNonce }

// Waivers returns the deliberate weakenings this policy carries, in the order
// the options were applied. The returned slice is a copy.
func (p *Policy) Waivers() []Waiver {
	out := make([]Waiver, len(p.waivers))
	copy(out, p.waivers)
	return out
}

// apply writes the policy's headers onto h, overwriting whatever is already
// there.
//
// Overwriting rather than filling in blanks is the point: a handler that set
// a weaker Referrer-Policy, or deleted Strict-Transport-Security, does not
// get to keep that change. The middleware runs this immediately before the
// status line is written, so it has the last word.
func (p *Policy) apply(h http.Header, n *nonceState) {
	if nonce, ok := n.minted(); ok {
		h.Set(p.CSPHeaderName(), renderWithNonce(p.directives, nonce))
		// A nonce that a shared cache can hand to a second visitor is a
		// nonce an attacker can read and then reuse, which is the whole
		// value of the mechanism gone. A handler's own directive is kept
		// only when it already says no-store — "private, no-store,
		// max-age=0" is doing the same job with more precision, while
		// "public, max-age=3600" is the hole this closes.
		if !strings.Contains(strings.ToLower(h.Get(HeaderCacheControl)), "no-store") {
			h.Set(HeaderCacheControl, "no-store")
		}
	} else {
		h.Set(p.CSPHeaderName(), p.cspNoNonce)
	}
	// Exactly one of the two CSP headers is ever present, so that "which
	// policy is actually being enforced" has one answer.
	if p.reportOnly {
		h.Del(HeaderCSP)
	} else {
		h.Del(HeaderCSPReportOnly)
	}

	if p.hsts != "" {
		h.Set(HeaderHSTS, p.hsts)
	} else {
		h.Del(HeaderHSTS)
	}

	h.Set(HeaderContentTypeOptions, "nosniff")
	h.Set(HeaderReferrerPolicy, p.referrer)
	h.Set(HeaderCOOP, p.coop)
	h.Set(HeaderCORP, p.corp)
	h.Set(HeaderPermissionsPolicy, p.permissions)

	if p.coep != "" {
		h.Set(HeaderCOEP, p.coep)
	} else {
		h.Del(HeaderCOEP)
	}

	if p.frameOption != "" {
		h.Set(HeaderFrameOptions, p.frameOption)
	} else {
		h.Del(HeaderFrameOptions)
	}
}

// builder accumulates option effects before a Policy is rendered from them.
type builder struct {
	deployment Deployment
	profile    profile

	connectSrc      []string
	fontSrc         []string
	formAction      []string
	frameAncestors  []string
	imgSrc          []string
	scriptSrc       []string
	styleSrc        []string
	inlineStyleAttr bool

	reportOnly bool
	reportURI  string

	hstsMaxAge            time.Duration
	hstsIncludeSubdomains bool
	hstsPreload           bool

	referrer    ReferrerPolicy
	coop        string
	corp        ResourcePolicy
	coep        EmbedderPolicy
	permissions map[string][]string

	waivers []Waiver
	errs    []error
}

// fail records a construction error. Errors accumulate so that a caller with
// several bad options learns about all of them at once.
func (b *builder) fail(err error) { b.errs = append(b.errs, err) }

// waive records a weakening, refusing an empty reason.
func (b *builder) waive(control, detail, reason string) {
	if strings.TrimSpace(reason) == "" {
		b.fail(fmt.Errorf(
			"secure: weakening %s to %s requires a non-empty reason",
			control, detail,
		))
		return
	}
	b.waivers = append(b.waivers, Waiver{Control: control, Detail: detail, Reason: reason})
}

// documentOnly records an error when an option that only makes sense for a
// document policy is applied to an API policy, rather than silently doing
// nothing.
func (b *builder) documentOnly(option string) bool {
	if b.profile == profileDocument {
		return true
	}
	b.fail(fmt.Errorf("secure: %s applies to a document policy, not an API policy", option))
	return false
}

// addSources validates each source and appends the accepted ones to dst.
func (b *builder) addSources(dst *[]string, sources []string) {
	if len(sources) == 0 {
		b.fail(errors.New("secure: no CSP sources given"))
		return
	}
	for _, s := range sources {
		if err := validSource(s); err != nil {
			b.fail(err)
			continue
		}
		*dst = append(*dst, s)
	}
}

// defaultPermissions is the Permissions-Policy an application starts with:
// every browser capability a generated operational application does not use,
// denied to itself and to every embedder.
//
// The list is deliberately explicit rather than clever. A browser ignores a
// feature token it does not recognize, so naming a capability that a given
// browser has not shipped costs nothing, while omitting one that it has
// leaves the capability at its own default — which for several of these is
// "allowed for this origin".
//
// fullscreen is the one entry that is allowed rather than denied: a data
// table or a chart going full-screen is ordinary behavior in this kind of
// application, and (self) still denies it to any embedder.
func defaultPermissions() map[string][]string {
	return map[string][]string{
		"accelerometer":             {},
		"ambient-light-sensor":      {},
		"autoplay":                  {},
		"battery":                   {},
		"browsing-topics":           {},
		"camera":                    {},
		"display-capture":           {},
		"encrypted-media":           {},
		"fullscreen":                {"self"},
		"geolocation":               {},
		"gyroscope":                 {},
		"idle-detection":            {},
		"local-fonts":               {},
		"magnetometer":              {},
		"microphone":                {},
		"midi":                      {},
		"payment":                   {},
		"publickey-credentials-get": {},
		"screen-wake-lock":          {},
		"serial":                    {},
		"usb":                       {},
		"xr-spatial-tracking":       {},
	}
}

// renderPermissions renders the Permissions-Policy header value, sorted by
// feature name so the output is byte-identical across runs and map
// iterations.
func renderPermissions(features map[string][]string) string {
	names := make([]string, 0, len(features))
	for name := range features {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"=("+strings.Join(features[name], " ")+")")
	}
	return strings.Join(parts, ", ")
}

// newPolicy builds a policy for one profile from the shared defaults plus
// opts.
func newPolicy(p profile, d Deployment, opts []Option) (*Policy, error) {
	if d != Development && d != Production {
		return nil, fmt.Errorf("secure: invalid deployment %q; use ParseDeployment", string(d))
	}

	b := &builder{
		deployment: d,
		profile:    p,
		referrer:   ReferrerSameOrigin,
		coop:       "same-origin",
		corp:       ResourceSameOrigin,
		// Cross-Origin-Embedder-Policy is deliberately unset. See the
		// package documentation and ADR 0013.
		coep:        "",
		permissions: defaultPermissions(),
	}
	if d == Production {
		b.hstsMaxAge = DefaultHSTSMaxAge
		b.hstsIncludeSubdomains = true
	}
	if p == profileDocument {
		b.connectSrc = []string{"'self'"}
		b.fontSrc = []string{"'self'"}
		b.formAction = []string{"'self'"}
		b.frameAncestors = []string{"'none'"}
		b.imgSrc = []string{"'self'", "data:"}
		b.scriptSrc = []string{"'self'"}
		b.styleSrc = []string{"'self'"}
	}

	for _, opt := range opts {
		if opt == nil {
			b.fail(errors.New("secure: nil Option"))
			continue
		}
		opt(b)
	}
	if err := errors.Join(b.errs...); err != nil {
		return nil, err
	}
	return b.build()
}

// build renders the accumulated state into an immutable Policy.
func (b *builder) build() (*Policy, error) {
	var ds []directive
	switch b.profile {
	case profileDocument:
		ds = []directive{
			{name: "default-src", sources: []string{"'none'"}},
			{name: "base-uri", sources: []string{"'none'"}},
			{name: "connect-src", sources: b.connectSrc},
			{name: "font-src", sources: b.fontSrc},
			{name: "form-action", sources: b.formAction},
			{name: "frame-ancestors", sources: b.frameAncestors},
			{name: "img-src", sources: b.imgSrc},
			{name: "object-src", sources: []string{"'none'"}},
			{name: "script-src", sources: b.scriptSrc},
			{name: "style-src", sources: b.styleSrc},
		}
		if b.inlineStyleAttr {
			ds = append(ds, directive{
				name:    "style-src-attr",
				sources: []string{"'unsafe-inline'"},
			})
		}
	case profileAPI:
		ds = []directive{
			{name: "default-src", sources: []string{"'none'"}},
			{name: "base-uri", sources: []string{"'none'"}},
			{name: "form-action", sources: []string{"'none'"}},
			{name: "frame-ancestors", sources: []string{"'none'"}},
			{name: "sandbox"},
		}
	}
	if b.reportURI != "" {
		ds = append(ds, directive{name: "report-uri", sources: []string{b.reportURI}})
	}

	if b.hstsPreload && b.hstsMaxAge > 0 {
		if b.hstsMaxAge < DefaultHSTSMaxAge || !b.hstsIncludeSubdomains {
			return nil, fmt.Errorf(
				"secure: WithHSTSPreload requires max-age of at least %s and includeSubDomains; AllowReducedHSTS took that away",
				DefaultHSTSMaxAge,
			)
		}
	}

	hsts := ""
	if b.hstsMaxAge > 0 {
		hsts = "max-age=" + strconv.FormatInt(int64(b.hstsMaxAge.Seconds()), 10)
		if b.hstsIncludeSubdomains {
			hsts += "; includeSubDomains"
		}
		if b.hstsPreload {
			hsts += "; preload"
		}
	}

	// X-Frame-Options can express "deny" and nothing else that a current
	// browser honors: ALLOW-FROM was never implemented outside Firefox and
	// is gone. So it is emitted only while frame-ancestors also denies
	// framing outright. Leaving DENY in place after an application allowed a
	// framing ancestor would make the application work in one browser and
	// break in another, with the CSP looking correct in both.
	frameOption := ""
	if len(b.frameAncestors) == 1 && b.frameAncestors[0] == "'none'" {
		frameOption = "DENY"
	}
	if b.profile == profileAPI {
		frameOption = "DENY"
	}

	nonceEnabled := false
	for _, d := range ds {
		if d.name == "script-src" {
			nonceEnabled = true
			break
		}
	}

	return &Policy{
		deployment:   b.deployment,
		reportOnly:   b.reportOnly,
		directives:   ds,
		cspNoNonce:   render(ds),
		nonceEnabled: nonceEnabled,
		hsts:         hsts,
		referrer:     string(b.referrer),
		coop:         b.coop,
		corp:         string(b.corp),
		coep:         string(b.coep),
		permissions:  renderPermissions(b.permissions),
		frameOption:  frameOption,
		waivers:      b.waivers,
	}, nil
}

// NewDocumentPolicy builds the policy for responses a browser renders as a
// document: the embedded single-page application's HTML shell, its build
// assets, and any HTML the Go server renders itself.
//
// The production default, with no options, is:
//
//	Content-Security-Policy: default-src 'none'; base-uri 'none';
//	  connect-src 'self'; font-src 'self'; form-action 'self';
//	  frame-ancestors 'none'; img-src 'self' data:; object-src 'none';
//	  script-src 'self'; style-src 'self'
//	Cross-Origin-Opener-Policy: same-origin
//	Cross-Origin-Resource-Policy: same-origin
//	Permissions-Policy: …every unused capability denied…
//	Referrer-Policy: same-origin
//	Strict-Transport-Security: max-age=31536000; includeSubDomains
//	X-Content-Type-Options: nosniff
//	X-Frame-Options: DENY
//
// A [Development] policy is identical except that
// Strict-Transport-Security is absent. There is no 'unsafe-inline' and no
// 'unsafe-eval' anywhere, in either deployment.
//
// See [Nonce] for how an inline script is allowed, and [WithScriptHashes] for
// the alternative that suits a statically built frontend.
func NewDocumentPolicy(d Deployment, opts ...Option) (*Policy, error) {
	return newPolicy(profileDocument, d, opts)
}

// NewAPIPolicy builds the policy for JSON responses under the API mount.
//
// It is strictly stricter than the document policy. Its CSP names no
// script-src, style-src, img-src, or connect-src at all — everything falls
// through to default-src 'none' — and it adds the sandbox directive, so an
// API response that a browser is talked into treating as a document (a
// direct navigation, a download opened in a tab, a content-type confusion
// that survived nosniff) has no origin, runs no script, submits no form, and
// opens no window. Its form-action is 'none' rather than 'self', because no
// API response contains a form.
//
// sandbox is inert for the requests that matter: it constrains a document
// created from the response, and a fetch or XMLHttpRequest never creates one.
//
// The non-CSP headers are the same as the document policy's.
func NewAPIPolicy(d Deployment, opts ...Option) (*Policy, error) {
	return newPolicy(profileAPI, d, opts)
}
