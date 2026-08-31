package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectRefusesCrossSiteStateChanges(t *testing.T) {
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
		"internal/platform/httpauth/csrf.go": {
			"HeaderFetchSite = \"Sec-Fetch-Site\"",
			"func NewOriginPolicy(origins []string) (OriginPolicy, error)",
			"func (p OriginPolicy) Permits(r *http.Request, origin string) bool",
			"func presentedOrigin(r *http.Request) (string, bool)",
			"func fetchMetadataRefusal(r *http.Request) string",
			"forwarded.FromContext(r.Context())",
		},
		"internal/platform/httpauth/guard.go": {
			"func (g *Guard) Middleware(next http.Handler) http.Handler",
			"session.SafeMethod(r.Method)",
			"session.VerifyCSRF(token, submitted)",
			"func (c *Cookies) WriteCSRFCookie(",
			"func (c *Cookies) EnsureCSRFCookie(",
			"HttpOnly: false",
		},
		"internal/platform/httpauth/csrf_test.go": {
			"TestGuardRefusesCrossSiteFetchMetadata",
			"TestGuardRequiresAKnownOrigin",
			"TestGuardRequiresTheSessionBoundToken",
			"TestGuardProtectsUnauthenticatedStateChanges",
			"TestCSRFCookieIsReadableAndPairedWithTheSession",
		},
		"internal/platform/httpapi/problem/problem.go": {
			`"cross_site_request"`,
			"func CrossSiteRequest() Definition",
			"http.StatusForbidden",
		},
		"internal/app/app.go": {
			"httpauth.NewOriginPolicy(cfg.AllowedOrigins)",
			"httpauth.NewGuard(originPolicy, sessionCookies, problem.HTTPHandler(problem.CrossSiteRequest()))",
			"apiGuard.Middleware",
			"documentGuard.Middleware",
		},
		"internal/platform/config/config.go": {
			"AllowedOrigins []string",
			`l.String("ALLOWED_ORIGINS"`,
		},
		".env.example": {
			"ALLOWED_ORIGINS=",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The guard must run inside the session resolver on both surfaces, so the
	// session its token check binds to is the one already resolved for the
	// request. The positions are compared rather than the whole literal, so
	// adding another middleware to the slot does not break this contract.
	app := content["internal/app/app.go"]
	for _, guard := range []string{"apiGuard.Middleware", "documentGuard.Middleware"} {
		session := strings.Index(app, "sessionResolver.Middleware")
		position := strings.Index(app, guard)
		if session < 0 || position < 0 {
			t.Fatalf("the application wiring lacks the session resolver or %s", guard)
		}
		if session > position {
			t.Errorf("%s runs before the session resolver", guard)
		}
	}

	// The anti-forgery cookie must be script-readable and the session cookie
	// must not be. Getting these the wrong way round breaks the mechanism in
	// one direction and the credential's protection in the other.
	cookie := content["internal/platform/httpauth/cookie.go"]
	if !strings.Contains(cookie, "HttpOnly: true") {
		t.Error("the session cookie is not HttpOnly")
	}
	guard := content["internal/platform/httpauth/guard.go"]
	if strings.Contains(guard, "HttpOnly: true") {
		t.Error("the anti-forgery cookie is HttpOnly, so no script could echo it")
	}
}
