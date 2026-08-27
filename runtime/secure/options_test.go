package secure_test

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/secure"
)

// TestUnsafeSourcesAreRefused checks the property the brief states as a hard
// requirement: there is no route through this API to 'unsafe-eval', or to
// 'unsafe-inline' in script-src.
func TestUnsafeSourcesAreRefused(t *testing.T) {
	t.Parallel()

	refused := []string{
		"'unsafe-inline'",
		"'UNSAFE-INLINE'",
		"'unsafe-eval'",
		"'unsafe-hashes'",
		"'wasm-unsafe-eval'",
		"*",
	}
	makers := map[string]func(string) secure.Option{
		"AllowScriptSources": func(s string) secure.Option {
			return secure.AllowScriptSources("reason", s)
		},
		"AllowStyleSources": func(s string) secure.Option {
			return secure.AllowStyleSources("reason", s)
		},
		"AllowConnectSources": func(s string) secure.Option {
			return secure.AllowConnectSources("reason", s)
		},
		"AllowImageSources": func(s string) secure.Option {
			return secure.AllowImageSources("reason", s)
		},
		"AllowFontSources": func(s string) secure.Option {
			return secure.AllowFontSources("reason", s)
		},
		"AllowFrameAncestors": func(s string) secure.Option {
			return secure.AllowFrameAncestors("reason", s)
		},
	}
	for name, make := range makers {
		for _, source := range refused {
			t.Run(name+"/"+source, func(t *testing.T) {
				t.Parallel()
				if _, err := secure.NewDocumentPolicy(secure.Production, make(source)); err == nil {
					t.Fatalf("%s accepted %q; no option may produce this source", name, source)
				}
			})
		}
	}
}

// TestSourcesCannotBreakOutOfTheHeader checks that a source cannot terminate
// its directive, split the source list, or split the HTTP response header.
func TestSourcesCannotBreakOutOfTheHeader(t *testing.T) {
	t.Parallel()

	refused := []string{
		"https://ok.example; script-src 'unsafe-inline'",
		"https://ok.example, https://evil.example",
		"https://ok.example\r\nSet-Cookie: session=stolen",
		"https://ok.example\nX-Injected: 1",
		"https://ok.example https://evil.example",
		"https://ok.example\t",
		"https://ok.example\x00",
		"https://ok.exämple",
		"",
		strings.Repeat("a", 4096),
	}
	for _, source := range refused {
		t.Run(strings.ReplaceAll(source, "\n", `\n`), func(t *testing.T) {
			t.Parallel()
			_, err := secure.NewDocumentPolicy(
				secure.Production,
				secure.AllowConnectSources("third-party analytics", source),
			)
			if err == nil {
				t.Fatalf("accepted CSP source %q", source)
			}
		})
	}
}

func TestAllowOptionsRequireANonEmptyReason(t *testing.T) {
	t.Parallel()

	for _, reason := range []string{"", " ", "\t\n"} {
		options := map[string]secure.Option{
			"AllowScriptSources":         secure.AllowScriptSources(reason, "https://cdn.example"),
			"AllowStyleSources":          secure.AllowStyleSources(reason, "https://cdn.example"),
			"AllowConnectSources":        secure.AllowConnectSources(reason, "https://api.example"),
			"AllowImageSources":          secure.AllowImageSources(reason, "https://img.example"),
			"AllowFontSources":           secure.AllowFontSources(reason, "https://font.example"),
			"AllowFrameAncestors":        secure.AllowFrameAncestors(reason, "https://portal.example"),
			"AllowInlineStyleAttributes": secure.AllowInlineStyleAttributes(reason),
			"AllowCrossOriginPopups":     secure.AllowCrossOriginPopups(reason),
			"AllowCrossOriginResources":  secure.AllowCrossOriginResources(reason, secure.ResourceCrossOrigin),
			"AllowReferrerPolicy":        secure.AllowReferrerPolicy(reason, secure.ReferrerUnsafeURL),
			"AllowPermission":            secure.AllowPermission(reason, "camera", "self"),
			"AllowReportOnly":            secure.AllowReportOnly(reason),
		}
		for name, opt := range options {
			t.Run(name+"/"+strings.TrimSpace(reason)+"empty", func(t *testing.T) {
				t.Parallel()
				if _, err := secure.NewDocumentPolicy(secure.Production, opt); err == nil {
					t.Fatalf("%s accepted an empty reason", name)
				}
			})
		}
	}
}

func TestWaiversRecordEveryWeakening(t *testing.T) {
	t.Parallel()

	clean, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	if got := clean.Waivers(); len(got) != 0 {
		t.Errorf("the default policy carries %d waivers: %v", len(got), got)
	}

	weakened, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowInlineStyleAttributes("SvelteKit's accessibility announcer sets a style attribute"),
		secure.AllowConnectSources("error reporting", "https://errors.example"),
		secure.AllowCrossOriginPopups("the identity provider signs in through a popup"),
	)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	waivers := weakened.Waivers()
	if len(waivers) != 3 {
		t.Fatalf("got %d waivers, want 3: %v", len(waivers), waivers)
	}
	controls := map[string]bool{}
	for _, w := range waivers {
		controls[w.Control] = true
		if w.Reason == "" {
			t.Errorf("waiver %v has no reason", w)
		}
		if !strings.Contains(w.String(), w.Reason) {
			t.Errorf("Waiver.String() = %q, want it to carry the reason", w.String())
		}
	}
	for _, want := range []string{"style-src-attr", "connect-src", "cross-origin-opener-policy"} {
		if !controls[want] {
			t.Errorf("no waiver recorded for %s", want)
		}
	}

	// The returned slice is a copy: mutating it must not launder a waiver
	// out of the policy.
	waivers[0] = secure.Waiver{}
	if weakened.Waivers()[0].Control == "" {
		t.Error("Waivers() returned the policy's own slice; a caller could erase its own weakenings")
	}
}

func TestAllowInlineStyleAttributesIsScopedToStyleSrcAttr(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowInlineStyleAttributes("component library sets element style attributes"),
	)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	directives := parseCSP(p.ContentSecurityPolicy())
	if got := directives["style-src-attr"]; got != "'unsafe-inline'" {
		t.Errorf("style-src-attr = %q, want %q", got, "'unsafe-inline'")
	}
	if got := directives["style-src"]; got != "'self'" {
		t.Errorf("style-src = %q; the waiver must not widen inline style elements too", got)
	}
	if got := directives["script-src"]; strings.Contains(got, "unsafe") {
		t.Errorf("script-src = %q; a style waiver must never touch script-src", got)
	}
}

func TestAllowFrameAncestorsDropsXFrameOptions(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowFrameAncestors("embedded in the partner portal", "https://portal.example"),
	)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	if got := parseCSP(p.ContentSecurityPolicy())["frame-ancestors"]; got != "https://portal.example" {
		t.Errorf("frame-ancestors = %q", got)
	}
	if got := serve(t, p, nil).Get(secure.HeaderFrameOptions); got != "" {
		t.Errorf("%s = %q after frame-ancestors was relaxed; the two headers must never disagree",
			secure.HeaderFrameOptions, got)
	}
}

func TestScriptAndStyleHashes(t *testing.T) {
	t.Parallel()

	const validHash = "sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="

	t.Run("accepted with and without quotes", func(t *testing.T) {
		t.Parallel()
		p, err := secure.NewDocumentPolicy(
			secure.Production,
			secure.WithScriptHashes(validHash),
			secure.WithStyleHashes("'"+validHash+"'"),
		)
		if err != nil {
			t.Fatalf("NewDocumentPolicy: %v", err)
		}
		directives := parseCSP(p.ContentSecurityPolicy())
		if got := directives["script-src"]; got != "'self' '"+validHash+"'" {
			t.Errorf("script-src = %q", got)
		}
		if got := directives["style-src"]; got != "'self' '"+validHash+"'" {
			t.Errorf("style-src = %q", got)
		}
		if got := p.Waivers(); len(got) != 0 {
			t.Errorf("a hash recorded %d waivers: %v; a hash names exact content and is not a weakening",
				len(got), got)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []string{
			"md5-abcdef",
			"sha256-",
			"sha256-not base64!",
			"sha1-47DEQpj8HBSa",
			"47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
			"'unsafe-inline'",
			"sha256-abc; script-src 'unsafe-inline'",
			// Valid base64 of the wrong width. These decode cleanly and
			// would reach the browser, which answers a hash that cannot
			// match by silently blocking the script.
			"sha256-AAAA",
			"sha256-" + validSHA384,
			"sha256-" + validSHA512,
			"sha384-" + validSHA256,
			"sha512-" + validSHA256,
		} {
			if _, err := secure.NewDocumentPolicy(secure.Production, secure.WithScriptHashes(bad)); err == nil {
				t.Errorf("WithScriptHashes accepted %q", bad)
			}
		}
	})

	t.Run("every algorithm at its own width", func(t *testing.T) {
		t.Parallel()
		for _, good := range []string{
			"sha256-" + validSHA256,
			"sha384-" + validSHA384,
			"sha512-" + validSHA512,
		} {
			if _, err := secure.NewDocumentPolicy(secure.Production, secure.WithScriptHashes(good)); err != nil {
				t.Errorf("WithScriptHashes(%q): %v", good, err)
			}
		}
	})

	t.Run("width error names the mismatch", func(t *testing.T) {
		t.Parallel()
		_, err := secure.NewDocumentPolicy(secure.Production, secure.WithScriptHashes("sha256-AAAA"))
		if err == nil {
			t.Fatal("accepted a three-byte sha256 digest")
		}
		for _, want := range []string{"3 bytes", "sha256", "32"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
}

func TestDocumentOnlyOptionsFailOnAnAPIPolicy(t *testing.T) {
	t.Parallel()

	options := map[string]secure.Option{
		"WithScriptHashes":           secure.WithScriptHashes("sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="),
		"WithStyleHashes":            secure.WithStyleHashes("sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="),
		"AllowScriptSources":         secure.AllowScriptSources("r", "https://cdn.example"),
		"AllowConnectSources":        secure.AllowConnectSources("r", "https://api.example"),
		"AllowInlineStyleAttributes": secure.AllowInlineStyleAttributes("r"),
		"AllowFrameAncestors":        secure.AllowFrameAncestors("r", "https://portal.example"),
	}
	for name, opt := range options {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := secure.NewAPIPolicy(secure.Production, opt); err == nil {
				t.Fatalf("%s silently did nothing on an API policy; it must fail instead", name)
			}
		})
	}
}

func TestReferrerPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opt     secure.Option
		want    string
		wantErr bool
	}{
		{name: "default", want: "same-origin"},
		{name: "no-referrer is not a weakening", opt: secure.WithReferrerPolicy(secure.ReferrerNoReferrer), want: "no-referrer"},
		{name: "same-origin explicitly", opt: secure.WithReferrerPolicy(secure.ReferrerSameOrigin), want: "same-origin"},
		{
			name:    "a leaking value refuses the With option",
			opt:     secure.WithReferrerPolicy(secure.ReferrerStrictOriginWhenCrossOrigin),
			wantErr: true,
		},
		{
			name: "a leaking value goes through Allow",
			opt:  secure.AllowReferrerPolicy("the payment provider requires a referrer", secure.ReferrerStrictOrigin),
			want: "strict-origin",
		},
		{
			name:    "Allow refuses a non-leaking value",
			opt:     secure.AllowReferrerPolicy("reason", secure.ReferrerNoReferrer),
			wantErr: true,
		},
		{
			name:    "unrecognized value",
			opt:     secure.WithReferrerPolicy(secure.ReferrerPolicy("no-referer")),
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var opts []secure.Option
			if tc.opt != nil {
				opts = append(opts, tc.opt)
			}
			p, err := secure.NewDocumentPolicy(secure.Production, opts...)
			if tc.wantErr {
				if err == nil {
					t.Fatal("NewDocumentPolicy succeeded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewDocumentPolicy: %v", err)
			}
			if got := serve(t, p, nil).Get(secure.HeaderReferrerPolicy); got != tc.want {
				t.Errorf("%s = %q, want %q", secure.HeaderReferrerPolicy, got, tc.want)
			}
		})
	}
}

func TestCrossOriginEmbedderPolicyIsOptIn(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	if got := serve(t, p, nil).Get(secure.HeaderCOEP); got != "" {
		t.Errorf("%s = %q by default; COEP breaks cross-origin subresources and must be opt-in",
			secure.HeaderCOEP, got)
	}

	opted, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.WithCrossOriginEmbedderPolicy(secure.EmbedderCredentialless),
	)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	if got := serve(t, opted, nil).Get(secure.HeaderCOEP); got != "credentialless" {
		t.Errorf("%s = %q, want %q", secure.HeaderCOEP, got, "credentialless")
	}
	if _, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.WithCrossOriginEmbedderPolicy(secure.EmbedderPolicy("unsafe-none")),
	); err == nil {
		t.Error("WithCrossOriginEmbedderPolicy accepted an unrecognized value")
	}
}

func TestCrossOriginResourcePolicy(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowCrossOriginResources("the docs site embeds our status badge", secure.ResourceSameSite),
	)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	if got := serve(t, p, nil).Get(secure.HeaderCORP); got != "same-site" {
		t.Errorf("%s = %q, want %q", secure.HeaderCORP, got, "same-site")
	}
	if _, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowCrossOriginResources("reason", secure.ResourceSameOrigin),
	); err == nil {
		t.Error("AllowCrossOriginResources accepted the default value; that would record a waiver for nothing")
	}
}

func TestAllowPermission(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowPermission("avatar capture", "camera", "self"),
	)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	got := serve(t, p, nil).Get(secure.HeaderPermissionsPolicy)
	if !strings.Contains(got, "camera=(self)") {
		t.Errorf("%s = %q, want it to contain camera=(self)", secure.HeaderPermissionsPolicy, got)
	}
	if !strings.Contains(got, "microphone=()") {
		t.Errorf("%s = %q, want the other capabilities still denied", secure.HeaderPermissionsPolicy, got)
	}

	for _, tc := range []struct {
		name      string
		feature   string
		allowlist []string
	}{
		{name: "wildcard allowlist", feature: "camera", allowlist: []string{"*"}},
		{name: "unquoted origin", feature: "camera", allowlist: []string{"https://example.com"}},
		{name: "http origin", feature: "camera", allowlist: []string{`"http://example.com"`}},
		{name: "uppercase feature", feature: "Camera", allowlist: []string{"self"}},
		{name: "feature with a separator", feature: "camera;geolocation", allowlist: []string{"self"}},
		{name: "empty feature", feature: "", allowlist: []string{"self"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := secure.NewDocumentPolicy(
				secure.Production,
				secure.AllowPermission("reason", tc.feature, tc.allowlist...),
			); err == nil {
				t.Fatalf("AllowPermission accepted feature %q allowlist %v", tc.feature, tc.allowlist)
			}
		})
	}
}

func TestReportURIValidation(t *testing.T) {
	t.Parallel()

	accepted := []string{"/api/v1/csp-reports", "https://reports.example.com/csp"}
	for _, uri := range accepted {
		if _, err := secure.NewDocumentPolicy(secure.Production, secure.WithReportURI(uri)); err != nil {
			t.Errorf("WithReportURI(%q): %v", uri, err)
		}
	}
	refused := []string{
		"",
		"http://reports.example.com/csp",
		"//reports.example.com/csp",
		"api/v1/csp-reports",
		"/reports; default-src *",
		"/reports\r\nX-Injected: 1",
		"https://",
	}
	for _, uri := range refused {
		if _, err := secure.NewDocumentPolicy(secure.Production, secure.WithReportURI(uri)); err == nil {
			t.Errorf("WithReportURI accepted %q", uri)
		}
	}
}

func TestConstructionErrorsAccumulate(t *testing.T) {
	t.Parallel()

	_, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowConnectSources("", "https://one.example"),
		secure.WithScriptHashes("not-a-hash"),
		secure.WithReportURI("http://insecure.example"),
	)
	if err == nil {
		t.Fatal("NewDocumentPolicy succeeded despite three bad options")
	}
	for _, want := range []string{"reason", "hash", "report URI"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q; all bad options should be reported at once", err, want)
		}
	}
}

func TestNilOptionIsRejected(t *testing.T) {
	t.Parallel()

	if _, err := secure.NewDocumentPolicy(secure.Production, nil); err == nil {
		t.Error("NewDocumentPolicy accepted a nil Option")
	}
}

// Base64 digests of the empty string at each accepted width, used to check
// that a hash of the right shape but the wrong algorithm width is refused.
var (
	validSHA256 = base64.StdEncoding.EncodeToString(func() []byte { s := sha256.Sum256(nil); return s[:] }())
	validSHA384 = base64.StdEncoding.EncodeToString(func() []byte { s := sha512.Sum384(nil); return s[:] }())
	validSHA512 = base64.StdEncoding.EncodeToString(func() []byte { s := sha512.Sum512(nil); return s[:] }())
)

// TestAllowPermissionWaivesOnlyWhenItGrants checks that the waiver list stays
// a signal. A waiver recorded for a call that grants nothing erodes exactly
// the assertion the package documents as the proof a policy is unweakened.
func TestAllowPermissionWaivesOnlyWhenItGrants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		feature    string
		allowlist  []string
		wantWaiver bool
		wantHeader string
	}{
		{
			name:       "granting a denied capability",
			feature:    "camera",
			allowlist:  []string{"self"},
			wantWaiver: true,
			wantHeader: "camera=(self)",
		},
		{
			name:       "adding an origin to a granted capability",
			feature:    "fullscreen",
			allowlist:  []string{"self", `"https://viewer.example"`},
			wantWaiver: true,
			wantHeader: `fullscreen=(self "https://viewer.example")`,
		},
		{
			name:       "restating what is already allowed",
			feature:    "fullscreen",
			allowlist:  []string{"self"},
			wantWaiver: false,
			wantHeader: "fullscreen=(self)",
		},
		{
			name:       "tightening a granted capability to nothing",
			feature:    "fullscreen",
			allowlist:  nil,
			wantWaiver: false,
			wantHeader: "fullscreen=()",
		},
		{
			name:       "re-denying an already denied capability",
			feature:    "camera",
			allowlist:  nil,
			wantWaiver: false,
			wantHeader: "camera=()",
		},
		{
			name:       "an unfamiliar feature errs toward recording",
			feature:    "compute-pressure",
			allowlist:  []string{"self"},
			wantWaiver: true,
			wantHeader: "compute-pressure=(self)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := secure.NewDocumentPolicy(
				secure.Production,
				secure.AllowPermission("a stated reason", tc.feature, tc.allowlist...),
			)
			if err != nil {
				t.Fatalf("NewDocumentPolicy: %v", err)
			}
			if got := len(p.Waivers()) > 0; got != tc.wantWaiver {
				t.Errorf("recorded a waiver = %v, want %v (waivers: %v)", got, tc.wantWaiver, p.Waivers())
			}
			if got := serve(t, p, nil).Get(secure.HeaderPermissionsPolicy); !strings.Contains(got, tc.wantHeader) {
				t.Errorf("%s = %q, want it to contain %q", secure.HeaderPermissionsPolicy, got, tc.wantHeader)
			}
		})
	}
}

// TestAllowPermissionReplacesRatherThanExtends pins the documented semantics.
// An extending option could only widen, which would make it impossible to
// narrow a capability the defaults already grant.
func TestAllowPermissionReplacesRatherThanExtends(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowPermission("first", "camera", "self", `"https://one.example"`),
		secure.AllowPermission("second", "camera", `"https://two.example"`),
	)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	got := serve(t, p, nil).Get(secure.HeaderPermissionsPolicy)
	if !strings.Contains(got, `camera=("https://two.example")`) {
		t.Errorf("%s = %q, want the last call to replace the allowlist", secure.HeaderPermissionsPolicy, got)
	}
	if strings.Contains(got, "one.example") {
		t.Errorf("%s = %q, want the first allowlist gone, not accumulated", secure.HeaderPermissionsPolicy, got)
	}
}
