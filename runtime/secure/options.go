package secure

import (
	"fmt"
	"strings"
	"time"
)

// Option adjusts a policy at construction time.
//
// The naming rule is the API's whole safety story, and it is worth stating
// plainly:
//
//   - With… options never weaken a protection. They tighten one, switch on a
//     rollout mechanism, or name an exact piece of content by its hash.
//   - Allow… options weaken a protection. Every one of them takes a reason
//     as its first argument, refuses an empty reason, and records a [Waiver]
//     on the policy.
//
// There is no third category and no setter. An application cannot remove a
// header, cannot blank a directive, and cannot reach 'unsafe-inline' for
// script-src or 'unsafe-eval' at all: those source keywords are refused by
// every option that takes sources, so the weakening does not exist in the
// API rather than being merely discouraged in the documentation.
//
// The consequence a reviewer can rely on: a weakened policy is visible in the
// application's own source as a call whose name starts with Allow, and is
// visible at runtime in [Policy.Waivers].
type Option func(*builder)

// WithReportOnly sends the policy in the Content-Security-Policy-Report-Only
// header instead of Content-Security-Policy, so violations are reported but
// nothing is blocked.
//
// This is the safe way to change a policy on a running application: deploy
// the new policy report-only alongside the enforced old one on a canary,
// collect violations for long enough to cover the application's less-used
// screens, then remove the option. Pair it with [WithReportURI] so the
// reports go somewhere; a report-only policy with no report destination
// produces browser console noise and nothing an operator can act on.
//
// The enforcing header and the report-only header are never sent together:
// whichever this policy is, the other one is deleted from the response. To
// enforce one policy while reporting a stricter candidate, build two policies
// and mount both middlewares.
func WithReportOnly() Option {
	return func(b *builder) { b.reportOnly = true }
}

// WithReportURI sends CSP violation reports to uri, which must be a
// same-origin absolute path such as "/api/v1/csp-reports" or an https URL.
// Plain http is refused: a violation report names the page URL and the
// blocked resource, and those must not cross the network in cleartext.
//
// Only the report-uri directive is emitted. The newer Reporting-API pairing
// of report-to with a Reporting-Endpoints header is deliberately not built
// here: it needs a second header this package would have to own, and
// report-uri remains the form every current browser accepts.
func WithReportURI(uri string) Option {
	return func(b *builder) {
		if err := validReportURI(uri); err != nil {
			b.fail(err)
			return
		}
		b.reportURI = uri
	}
}

// WithScriptHashes adds sha256, sha384, or sha512 hash sources to script-src,
// allowing exactly the inline scripts whose contents hash to those values.
// Each hash is accepted with or without surrounding single quotes.
//
// This takes no reason and records no waiver, because a hash is not a
// weakening in the way a keyword is: it names one exact byte sequence, so an
// attacker who injects different content into the same position is still
// blocked. A malformed value — anything that is not a recognized algorithm
// followed by valid base64 — fails construction rather than being passed
// through to the browser, which would ignore it and leave the script blocked
// with no explanation.
//
// This is the option a statically built frontend needs. SvelteKit's
// adapter-static writes one inline bootstrap script into the fallback page,
// and its contents — and therefore its hash — change on every build. The
// hash must be computed from the built artifact rather than written into Go
// source; see docs/security-headers.md for how.
func WithScriptHashes(hashes ...string) Option {
	return func(b *builder) {
		if !b.documentOnly("WithScriptHashes") {
			return
		}
		for _, raw := range hashes {
			h, err := normalizeHash(raw)
			if err != nil {
				b.fail(err)
				continue
			}
			b.scriptSrc = append(b.scriptSrc, h)
		}
	}
}

// WithStyleHashes adds hash sources to style-src, allowing exactly the inline
// style elements whose contents hash to those values. See [WithScriptHashes]
// for why a hash carries no waiver.
//
// Note that a hash source does not allow an inline style attribute: browsers
// match hashes against style elements only, unless 'unsafe-hashes' is
// present, which this package refuses. See [AllowInlineStyleAttributes].
func WithStyleHashes(hashes ...string) Option {
	return func(b *builder) {
		if !b.documentOnly("WithStyleHashes") {
			return
		}
		for _, raw := range hashes {
			h, err := normalizeHash(raw)
			if err != nil {
				b.fail(err)
				continue
			}
			b.styleSrc = append(b.styleSrc, h)
		}
	}
}

// WithHSTSPreload adds the preload token to Strict-Transport-Security.
//
// It is off by default, and that is a decision rather than an oversight.
// Submitting a domain to the browser-vendor preload list is an approximately
// irreversible act taken by the domain's operator, not by the framework that
// generated the application: it covers the registrable domain and every
// subdomain, present and future, and removal takes months to reach the
// browsers that already shipped the list. Emitting the token by default would
// also assert, on the operator's behalf, a submission the operator has not
// made.
//
// The token is meaningless until the operator submits the domain at
// hstspreload.org. Use this only when they have decided to, and only when
// every subdomain of the registrable domain — including ones that do not
// exist yet — can serve TLS.
//
// Preloading requires a max-age of at least one year and includeSubDomains,
// so this option fails construction if [AllowReducedHSTS] has taken either
// away.
func WithHSTSPreload() Option {
	return func(b *builder) { b.hstsPreload = true }
}

// WithReferrerPolicy sets Referrer-Policy to a value that sends nothing
// cross-origin: [ReferrerSameOrigin], the default, or [ReferrerNoReferrer].
//
// The default is same-origin rather than no-referrer because it is strictly
// more useful at identical privacy: neither sends anything to a third party,
// and same-origin keeps the Referer header available for the application's
// own same-origin requests, where a request-forgery check may use it as a
// fallback when Origin is absent. It is stricter than every browser's own
// default, which leaks the application's hostname to any site a user
// navigates to.
//
// Any value that does send referrer information cross-origin has to go
// through [AllowReferrerPolicy] instead.
func WithReferrerPolicy(p ReferrerPolicy) Option {
	return func(b *builder) {
		if !p.valid() {
			b.fail(fmt.Errorf("secure: unrecognized Referrer-Policy %q", string(p)))
			return
		}
		if p.leaksReferrerCrossOrigin() {
			b.fail(fmt.Errorf(
				"secure: Referrer-Policy %q sends referrer information cross-origin; use AllowReferrerPolicy with a reason",
				string(p),
			))
			return
		}
		b.referrer = p
	}
}

// WithCrossOriginEmbedderPolicy sets Cross-Origin-Embedder-Policy, which this
// package otherwise does not send.
//
// COEP is off by default, and that is a decision. It is a capability enabler,
// not a protection: its purpose is to earn cross-origin isolation so a page
// may use SharedArrayBuffer and high-resolution timers. A generated
// operational application uses neither. What it does do, immediately, is
// refuse every cross-origin subresource that has not opted in with CORP or
// CORS — which in a Nise application means avatars and attachments served
// from object storage through presigned URLs stop loading, with a console
// error rather than a visible failure. Paying that for a capability nothing
// uses is a bad trade, so the application has to ask.
//
// [EmbedderCredentialless] is the softer of the two: it loads the
// non-opted-in subresource without credentials instead of refusing it.
func WithCrossOriginEmbedderPolicy(p EmbedderPolicy) Option {
	return func(b *builder) {
		switch p {
		case EmbedderRequireCorp, EmbedderCredentialless:
			b.coep = p
		default:
			b.fail(fmt.Errorf("secure: unrecognized Cross-Origin-Embedder-Policy %q", string(p)))
		}
	}
}

// AllowScriptSources adds origins to script-src.
//
// The unsafe source keywords are refused here as everywhere: an origin, a
// scheme, or a hash is accepted, 'unsafe-inline', 'unsafe-eval',
// 'wasm-unsafe-eval', 'unsafe-hashes', and '*' are not. Loading executable
// code from a third-party origin means that origin can take over every
// session in the application, so the reason should say who the origin is and
// why the application trusts them with that.
func AllowScriptSources(reason string, sources ...string) Option {
	return func(b *builder) {
		if !b.documentOnly("AllowScriptSources") {
			return
		}
		b.addSources(&b.scriptSrc, sources)
		b.waive("script-src", strings.Join(sources, " "), reason)
	}
}

// AllowStyleSources adds origins to style-src. See [AllowScriptSources] for
// the refused keywords. A third-party stylesheet can read and exfiltrate page
// content through selectors and background-image URLs, so this is a smaller
// hole than a script but not a small one.
func AllowStyleSources(reason string, sources ...string) Option {
	return func(b *builder) {
		if !b.documentOnly("AllowStyleSources") {
			return
		}
		b.addSources(&b.styleSrc, sources)
		b.waive("style-src", strings.Join(sources, " "), reason)
	}
}

// AllowConnectSources adds origins to connect-src, letting the frontend call
// something other than its own API.
//
// connect-src is the directive that decides where injected script can send
// what it has stolen, so widening it widens the exfiltration channel by
// exactly that much. Name the specific origin; never a scheme.
func AllowConnectSources(reason string, sources ...string) Option {
	return func(b *builder) {
		if !b.documentOnly("AllowConnectSources") {
			return
		}
		b.addSources(&b.connectSrc, sources)
		b.waive("connect-src", strings.Join(sources, " "), reason)
	}
}

// AllowImageSources adds origins to img-src, which defaults to 'self' and
// data:.
//
// data: is in the default because the application renders images it generates
// itself — a TOTP enrollment QR code is the concrete case — and a data: URL
// in an img element cannot execute script. An external origin here mostly
// leaks which of the application's pages a user visited to that origin.
func AllowImageSources(reason string, sources ...string) Option {
	return func(b *builder) {
		if !b.documentOnly("AllowImageSources") {
			return
		}
		b.addSources(&b.imgSrc, sources)
		b.waive("img-src", strings.Join(sources, " "), reason)
	}
}

// AllowFontSources adds origins to font-src, which defaults to 'self'.
//
// The generated frontend uses a system font stack precisely so that no font
// is fetched from a third party, which is a privacy property as much as a
// performance one. Widening this gives that away.
func AllowFontSources(reason string, sources ...string) Option {
	return func(b *builder) {
		if !b.documentOnly("AllowFontSources") {
			return
		}
		b.addSources(&b.fontSrc, sources)
		b.waive("font-src", strings.Join(sources, " "), reason)
	}
}

// AllowFrameAncestors replaces frame-ancestors 'none' with an explicit list
// of origins permitted to frame the application.
//
// This is the clickjacking defense, so the reason should name the embedding
// product. Doing this also removes the X-Frame-Options header, because
// X-Frame-Options cannot express a list of origins and leaving DENY behind
// would make the application load inside the frame in one browser and refuse
// to in another.
func AllowFrameAncestors(reason string, sources ...string) Option {
	return func(b *builder) {
		if !b.documentOnly("AllowFrameAncestors") {
			return
		}
		replaced := make([]string, 0, len(sources))
		b.addSources(&replaced, sources)
		if len(replaced) == 0 {
			return
		}
		b.frameAncestors = replaced
		b.waive("frame-ancestors", strings.Join(replaced, " "), reason)
	}
}

// AllowInlineStyleAttributes adds 'unsafe-inline' to style-src-attr, allowing
// inline style attributes on elements while continuing to block inline style
// elements.
//
// This is the narrowest form of the one weakening a Svelte frontend may
// genuinely need. It is scoped to style-src-attr on purpose: putting
// 'unsafe-inline' into style-src instead would also allow arbitrary inline
// style elements, which nothing in this stack requires.
//
// Before reaching for it, check whether the specific attribute can be
// replaced by a rule in the application's stylesheet — an external stylesheet
// is allowed by style-src 'self', and a rule there applies whether or not the
// inline attribute was blocked. docs/security-headers.md works through the
// one case in SvelteKit's own runtime where that substitution is what is
// wanted.
func AllowInlineStyleAttributes(reason string) Option {
	return func(b *builder) {
		if !b.documentOnly("AllowInlineStyleAttributes") {
			return
		}
		b.inlineStyleAttr = true
		b.waive("style-src-attr", "'unsafe-inline'", reason)
	}
}

// AllowCrossOriginPopups relaxes Cross-Origin-Opener-Policy from same-origin
// to same-origin-allow-popups, so the application may open a cross-origin
// window and keep a handle to it.
//
// The default severs the opener relationship in both directions, which closes
// the cross-window attacks — tab-nabbing, and the cross-site leaks that read
// a victim window's frame count or navigation history. Relaxing it is
// normally wanted for a popup-based sign-in or payment flow. It grants the
// popup a handle back to the application's window; it does not grant it
// access to that window's contents.
func AllowCrossOriginPopups(reason string) Option {
	return func(b *builder) {
		b.coop = "same-origin-allow-popups"
		b.waive("cross-origin-opener-policy", "same-origin-allow-popups", reason)
	}
}

// AllowCrossOriginResources relaxes Cross-Origin-Resource-Policy from
// same-origin, letting other sites load this application's responses as
// subresources.
//
// The default refuses that, which is right for an application that is not a
// content delivery network: it stops another site from embedding this
// application's images or scripts, and it is one of the mitigations against
// a cross-origin read.
func AllowCrossOriginResources(reason string, p ResourcePolicy) Option {
	return func(b *builder) {
		switch p {
		case ResourceSameSite, ResourceCrossOrigin:
			b.corp = p
			b.waive("cross-origin-resource-policy", string(p), reason)
		case ResourceSameOrigin:
			b.fail(fmt.Errorf(
				"secure: Cross-Origin-Resource-Policy %q is already the default; no waiver needed",
				string(p),
			))
		default:
			b.fail(fmt.Errorf("secure: unrecognized Cross-Origin-Resource-Policy %q", string(p)))
		}
	}
}

// AllowReferrerPolicy sets a Referrer-Policy that sends referrer information
// to cross-origin destinations.
//
// An operational application's URLs carry record identifiers, tenant names,
// and search terms. Every value this option accepts hands some part of that
// to a third party on outbound navigation. [ReferrerUnsafeURL] hands over the
// whole URL, over plain HTTP as well as TLS, and should be treated as a
// mistake in every case a reviewer is likely to see.
//
// For the two values that leak nothing, use [WithReferrerPolicy].
func AllowReferrerPolicy(reason string, p ReferrerPolicy) Option {
	return func(b *builder) {
		if !p.valid() {
			b.fail(fmt.Errorf("secure: unrecognized Referrer-Policy %q", string(p)))
			return
		}
		if !p.leaksReferrerCrossOrigin() {
			b.fail(fmt.Errorf(
				"secure: Referrer-Policy %q leaks nothing cross-origin; use WithReferrerPolicy",
				string(p),
			))
			return
		}
		b.referrer = p
		b.waive("referrer-policy", string(p), reason)
	}
}

// AllowPermission grants a browser capability that the default
// Permissions-Policy denies, or adds an origin to one it already grants.
//
// allowlist entries are Permissions-Policy allowlist items: self, src, or a
// quoted origin such as `"https://example.com"`. An empty allowlist denies
// the feature to everyone, which is what the defaults already do. The
// wildcard * is refused: granting a capability to every possible embedder is
// never what an application that also denies framing means to say.
func AllowPermission(reason string, feature string, allowlist ...string) Option {
	return func(b *builder) {
		if err := validPermissionFeature(feature); err != nil {
			b.fail(err)
			return
		}
		items := make([]string, 0, len(allowlist))
		for _, item := range allowlist {
			if err := validPermissionAllowlistItem(item); err != nil {
				b.fail(err)
				continue
			}
			items = append(items, item)
		}
		b.permissions[feature] = items
		b.waive("permissions-policy", feature+"=("+strings.Join(items, " ")+")", reason)
	}
}

// AllowReducedHSTS shortens Strict-Transport-Security below the one-year
// default, or drops includeSubDomains, or both.
//
// Both defaults exist for a reason. A year is what the preload list requires
// and what survives a user who signs in monthly. includeSubDomains is what
// stops an attacker from using a forgotten plain-HTTP subdomain to set a
// cookie that the application's own origin will then send.
//
// The legitimate uses are narrow: a staged rollout that starts at a few
// minutes so a TLS mistake can still be undone, or a registrable domain that
// carries a subdomain outside the application's control which cannot serve
// TLS. maxAge must be greater than zero — there is no option that removes
// Strict-Transport-Security from a production deployment entirely.
//
// In a [Development] deployment no HSTS header is sent at all, so this option
// has nothing to reduce and fails construction rather than pretending to.
func AllowReducedHSTS(reason string, maxAge time.Duration, includeSubdomains bool) Option {
	return func(b *builder) {
		if b.deployment != Production {
			b.fail(fmt.Errorf(
				"secure: AllowReducedHSTS applies to a production policy; a %s policy sends no Strict-Transport-Security",
				b.deployment,
			))
			return
		}
		if maxAge <= 0 {
			b.fail(fmt.Errorf(
				"secure: AllowReducedHSTS max-age must be greater than zero; Strict-Transport-Security cannot be removed from a production policy",
			))
			return
		}
		b.hstsMaxAge = maxAge
		b.hstsIncludeSubdomains = includeSubdomains
		detail := "max-age=" + maxAge.String()
		if !includeSubdomains {
			detail += ", no includeSubDomains"
		}
		b.waive("strict-transport-security", detail, reason)
	}
}

// validPermissionFeature checks a Permissions-Policy feature name.
func validPermissionFeature(feature string) error {
	if feature == "" {
		return fmt.Errorf("secure: empty Permissions-Policy feature name")
	}
	if len(feature) > 64 {
		return fmt.Errorf("secure: Permissions-Policy feature name %q is too long", feature)
	}
	for i := 0; i < len(feature); i++ {
		c := feature[i]
		if (c >= 'a' && c <= 'z') || c == '-' {
			continue
		}
		return fmt.Errorf(
			"secure: Permissions-Policy feature name %q must be lowercase ASCII letters and hyphens",
			feature,
		)
	}
	return nil
}

// validPermissionAllowlistItem checks one Permissions-Policy allowlist entry.
func validPermissionAllowlistItem(item string) error {
	switch item {
	case "self", "src":
		return nil
	case "*":
		return fmt.Errorf("secure: Permissions-Policy allowlist item %q is refused; name the origins", item)
	}
	if !strings.HasPrefix(item, `"https://`) || !strings.HasSuffix(item, `"`) || len(item) <= len(`"https://"`) {
		return fmt.Errorf(
			`secure: Permissions-Policy allowlist item %q must be self, src, or a quoted https origin such as "https://example.com"`,
			item,
		)
	}
	for i := 0; i < len(item); i++ {
		c := item[i]
		if c < 0x21 || c > 0x7E || c == ',' || c == ';' {
			return fmt.Errorf("secure: Permissions-Policy allowlist item %q is not a usable header value", item)
		}
	}
	return nil
}
