package secure_test

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/secure"
)

// wantProductionPermissions is the Permissions-Policy every policy sends. It
// is written out in full rather than derived, so that adding or removing a
// denied capability has to be a deliberate edit to this line.
const wantProductionPermissions = "accelerometer=(), ambient-light-sensor=(), autoplay=(), " +
	"battery=(), browsing-topics=(), camera=(), display-capture=(), encrypted-media=(), " +
	"fullscreen=(self), geolocation=(), gyroscope=(), idle-detection=(), local-fonts=(), " +
	"magnetometer=(), microphone=(), midi=(), payment=(), publickey-credentials-get=(), " +
	"screen-wake-lock=(), serial=(), usb=(), xr-spatial-tracking=()"

// wantDocumentCSP is the document policy's Content-Security-Policy for a
// response that carries no nonce.
const wantDocumentCSP = "default-src 'none'; base-uri 'none'; connect-src 'self'; " +
	"font-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; " +
	"object-src 'none'; script-src 'self'; style-src 'self'"

// wantAPICSP is the API policy's Content-Security-Policy.
const wantAPICSP = "default-src 'none'; base-uri 'none'; form-action 'none'; " +
	"frame-ancestors 'none'; sandbox"

// serve runs one request through Middleware(p) and returns the response
// headers. The handler writes a 200 with a short body, the ordinary case.
func serve(t *testing.T, p *secure.Policy, handler http.Handler) http.Header {
	t.Helper()
	if handler == nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if _, err := w.Write([]byte("ok")); err != nil {
				t.Fatalf("write: %v", err)
			}
		})
	}
	rec := httptest.NewRecorder()
	secure.Middleware(p)(handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Result().Header
}

// assertExactHeaders fails unless h contains exactly the keys in want, with
// exactly those values. Both halves matter: a missing header is a lost
// protection, and an unexpected header is a change nobody reviewed.
func assertExactHeaders(t *testing.T, h http.Header, want map[string]string) {
	t.Helper()
	for name, wantValue := range want {
		if got := h.Get(name); got != wantValue {
			t.Errorf("header %s:\n got  %q\n want %q", name, got, wantValue)
		}
	}
	var unexpected []string
	for name := range h {
		if _, ok := want[http.CanonicalHeaderKey(name)]; !ok {
			unexpected = append(unexpected, name+": "+h.Get(name))
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Errorf("unexpected response headers: %v", unexpected)
	}
}

func TestDocumentPolicyProductionHeaders(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	assertExactHeaders(t, serve(t, p, nil), map[string]string{
		"Content-Type":                 "text/plain; charset=utf-8",
		"Content-Security-Policy":      wantDocumentCSP,
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Permissions-Policy":           wantProductionPermissions,
		"Referrer-Policy":              "same-origin",
		"Strict-Transport-Security":    "max-age=31536000; includeSubDomains",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	})
}

func TestDocumentPolicyDevelopmentHeaders(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Development)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	h := serve(t, p, nil)
	assertExactHeaders(t, h, map[string]string{
		"Content-Type":                 "text/plain; charset=utf-8",
		"Content-Security-Policy":      wantDocumentCSP,
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Permissions-Policy":           wantProductionPermissions,
		"Referrer-Policy":              "same-origin",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	})
	if got := h.Get(secure.HeaderHSTS); got != "" {
		t.Errorf("development policy sent %s: %q; a stray HSTS header pins http://localhost for a year",
			secure.HeaderHSTS, got)
	}
}

func TestDevelopmentPolicyDiffersFromProductionOnlyInHSTS(t *testing.T) {
	t.Parallel()

	dev, err := secure.NewDocumentPolicy(secure.Development)
	if err != nil {
		t.Fatalf("NewDocumentPolicy(Development): %v", err)
	}
	prod, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy(Production): %v", err)
	}

	devHeaders, prodHeaders := serve(t, dev, nil), serve(t, prod, nil)
	for name := range prodHeaders {
		if name == secure.HeaderHSTS {
			continue
		}
		if devHeaders.Get(name) != prodHeaders.Get(name) {
			t.Errorf("header %s differs between deployments:\n dev  %q\n prod %q",
				name, devHeaders.Get(name), prodHeaders.Get(name))
		}
	}
}

func TestAPIPolicyIsStricterThanDocumentPolicy(t *testing.T) {
	t.Parallel()

	api, err := secure.NewAPIPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewAPIPolicy: %v", err)
	}
	doc, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}

	assertExactHeaders(t, serve(t, api, nil), map[string]string{
		"Content-Type":                 "text/plain; charset=utf-8",
		"Content-Security-Policy":      wantAPICSP,
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Permissions-Policy":           wantProductionPermissions,
		"Referrer-Policy":              "same-origin",
		"Strict-Transport-Security":    "max-age=31536000; includeSubDomains",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	})

	if api.ContentSecurityPolicy() == doc.ContentSecurityPolicy() {
		t.Fatal("API and document policies produced the same CSP; the API policy must be stricter")
	}

	apiDirectives := parseCSP(api.ContentSecurityPolicy())
	for _, name := range []string{"script-src", "style-src", "img-src", "connect-src", "font-src"} {
		if _, present := apiDirectives[name]; present {
			t.Errorf("API policy names %s; it must fall through to default-src 'none'", name)
		}
	}
	if _, present := apiDirectives["sandbox"]; !present {
		t.Error("API policy is missing the sandbox directive")
	}
	if got := apiDirectives["form-action"]; got != "'none'" {
		t.Errorf("API form-action = %q, want %q", got, "'none'")
	}
}

func TestPolicyRenderingIsDeterministic(t *testing.T) {
	t.Parallel()

	build := func() *secure.Policy {
		p, err := secure.NewDocumentPolicy(
			secure.Production,
			secure.AllowPermission("avatar capture from the device camera", "camera", "self"),
			secure.AllowConnectSources("metrics collector", "https://metrics.example.com"),
			secure.WithScriptHashes("sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="),
		)
		if err != nil {
			t.Fatalf("NewDocumentPolicy: %v", err)
		}
		return p
	}
	first, second := build(), build()
	if first.ContentSecurityPolicy() != second.ContentSecurityPolicy() {
		t.Errorf("CSP is not deterministic:\n %q\n %q",
			first.ContentSecurityPolicy(), second.ContentSecurityPolicy())
	}
	if serve(t, first, nil).Get(secure.HeaderPermissionsPolicy) !=
		serve(t, second, nil).Get(secure.HeaderPermissionsPolicy) {
		t.Error("Permissions-Policy is not deterministic across builds of the same policy")
	}
}

func TestReportOnlyEmitsOnlyTheReportOnlyHeader(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowReportOnly("rolling out a tightened connect-src; enforce after the canary week"),
		secure.WithReportURI("/api/v1/csp-reports"),
	)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	if !p.ReportOnly() {
		t.Error("ReportOnly() = false after AllowReportOnly()")
	}

	h := serve(t, p, nil)
	if got := h.Get(secure.HeaderCSP); got != "" {
		t.Errorf("report-only policy also sent the enforcing header %s: %q", secure.HeaderCSP, got)
	}
	got := h.Get(secure.HeaderCSPReportOnly)
	if got == "" {
		t.Fatalf("report-only policy sent no %s header", secure.HeaderCSPReportOnly)
	}
	if !strings.Contains(got, "report-uri /api/v1/csp-reports") {
		t.Errorf("%s = %q, want it to carry the report-uri directive", secure.HeaderCSPReportOnly, got)
	}
	if p.CSPHeaderName() != secure.HeaderCSPReportOnly {
		t.Errorf("CSPHeaderName() = %q, want %q", p.CSPHeaderName(), secure.HeaderCSPReportOnly)
	}
}

func TestEnforcingPolicyDeletesAStrayReportOnlyHeader(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	h := serve(t, p, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(secure.HeaderCSPReportOnly, "default-src *")
		w.WriteHeader(http.StatusOK)
	}))
	if got := h.Get(secure.HeaderCSPReportOnly); got != "" {
		t.Errorf("%s survived an enforcing policy: %q", secure.HeaderCSPReportOnly, got)
	}
	if got := h.Get(secure.HeaderCSP); got != wantDocumentCSP {
		t.Errorf("%s = %q, want %q", secure.HeaderCSP, got, wantDocumentCSP)
	}
}

func TestParseDeployment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw     string
		want    secure.Deployment
		wantErr bool
	}{
		{raw: "development", want: secure.Development},
		{raw: "test", want: secure.Development},
		{raw: "production", want: secure.Production},
		{raw: "staging", wantErr: true},
		{raw: "Production", wantErr: true},
		{raw: "", wantErr: true},
		{raw: "prod", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got, err := secure.ParseDeployment(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDeployment(%q) = %q, want an error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDeployment(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("ParseDeployment(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestNewPolicyRejectsAnUnsetDeployment(t *testing.T) {
	t.Parallel()

	if _, err := secure.NewDocumentPolicy(""); err == nil {
		t.Error("NewDocumentPolicy(\"\") succeeded; the zero Deployment must not be usable")
	}
	if _, err := secure.NewAPIPolicy(secure.Deployment("staging")); err == nil {
		t.Error("NewAPIPolicy(\"staging\") succeeded; the deployment set is closed")
	}
}

func TestHSTS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		deployment secure.Deployment
		opts       []secure.Option
		want       string
		wantErr    bool
	}{
		{
			name:       "production default",
			deployment: secure.Production,
			want:       "max-age=31536000; includeSubDomains",
		},
		{
			name:       "development sends none",
			deployment: secure.Development,
			want:       "",
		},
		{
			name:       "preload is opt-in",
			deployment: secure.Production,
			opts:       []secure.Option{secure.WithHSTSPreload()},
			want:       "max-age=31536000; includeSubDomains; preload",
		},
		{
			name:       "reduced max-age is recorded",
			deployment: secure.Production,
			opts: []secure.Option{
				secure.AllowReducedHSTS("staged TLS rollout, raised after the first week", time.Hour, true),
			},
			want: "max-age=3600; includeSubDomains",
		},
		{
			name:       "dropping includeSubDomains is recorded",
			deployment: secure.Production,
			opts: []secure.Option{
				secure.AllowReducedHSTS("legacy plain-HTTP subdomain outside our control", 24*time.Hour, false),
			},
			want: "max-age=86400",
		},
		{
			name:       "preload refuses a reduced max-age",
			deployment: secure.Production,
			opts: []secure.Option{
				secure.AllowReducedHSTS("rollout", time.Hour, true),
				secure.WithHSTSPreload(),
			},
			wantErr: true,
		},
		{
			name:       "preload refuses a policy without includeSubDomains",
			deployment: secure.Production,
			opts: []secure.Option{
				secure.AllowReducedHSTS("rollout", secure.DefaultHSTSMaxAge, false),
				secure.WithHSTSPreload(),
			},
			wantErr: true,
		},
		{
			name:       "HSTS cannot be removed from production",
			deployment: secure.Production,
			opts:       []secure.Option{secure.AllowReducedHSTS("we do not want it", 0, true)},
			wantErr:    true,
		},
		{
			name:       "reducing a development policy is an error, not a no-op",
			deployment: secure.Development,
			opts:       []secure.Option{secure.AllowReducedHSTS("rollout", time.Hour, true)},
			wantErr:    true,
		},
		{
			name:       "a reduction still requires a reason",
			deployment: secure.Production,
			opts:       []secure.Option{secure.AllowReducedHSTS("  ", time.Hour, true)},
			wantErr:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := secure.NewDocumentPolicy(tc.deployment, tc.opts...)
			if tc.wantErr {
				if err == nil {
					t.Fatal("NewDocumentPolicy succeeded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewDocumentPolicy: %v", err)
			}
			if got := serve(t, p, nil).Get(secure.HeaderHSTS); got != tc.want {
				t.Errorf("%s = %q, want %q", secure.HeaderHSTS, got, tc.want)
			}
		})
	}
}
