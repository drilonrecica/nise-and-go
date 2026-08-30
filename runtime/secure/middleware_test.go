package secure_test

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/secure"
)

// nonceHandler is a handler that asks for a nonce, as an HTML handler would,
// and records what it got.
func nonceHandler(t *testing.T, got *string, ok *bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got, *ok = secure.Nonce(r.Context())
		if _, err := w.Write([]byte("<!doctype html>")); err != nil {
			t.Errorf("write: %v", err)
		}
	})
}

func TestNonceIsUniquePerResponse(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}

	const responses = 512
	seen := make(map[string]struct{}, responses)
	for i := 0; i < responses; i++ {
		var nonce string
		var ok bool
		h := serve(t, p, nonceHandler(t, &nonce, &ok))
		if !ok {
			t.Fatalf("response %d: Nonce reported no nonce under a document policy", i)
		}
		if len(nonce) != 22 {
			t.Fatalf("response %d: nonce %q is %d characters, want 22 (128 bits)", i, nonce, len(nonce))
		}
		if _, duplicate := seen[nonce]; duplicate {
			t.Fatalf("response %d reused nonce %q", i, nonce)
		}
		seen[nonce] = struct{}{}

		want := "'nonce-" + nonce + "'"
		csp := h.Get(secure.HeaderCSP)
		if !strings.Contains(csp, want) {
			t.Fatalf("response %d: CSP %q does not name the handler's nonce %s", i, csp, want)
		}
		if !strings.Contains(csp, "script-src 'self' "+want) {
			t.Fatalf("response %d: nonce is not in script-src: %q", i, csp)
		}
	}
	if len(seen) != responses {
		t.Fatalf("got %d distinct nonces across %d responses", len(seen), responses)
	}
}

func TestNonceIsStableWithinOneResponse(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	var first, second, third string
	h := serve(t, p, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first, _ = secure.Nonce(r.Context())
		second, _ = secure.Nonce(r.Context())
		w.WriteHeader(http.StatusOK)
		third, _ = secure.Nonce(r.Context())
	}))
	if first == "" || first != second || second != third {
		t.Fatalf("nonce changed within one response: %q, %q, %q", first, second, third)
	}
	if !strings.Contains(h.Get(secure.HeaderCSP), "'nonce-"+first+"'") {
		t.Errorf("CSP %q does not carry the nonce", h.Get(secure.HeaderCSP))
	}
}

func TestNonceIsNotMintedUnlessAsked(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	h := serve(t, p, nil)
	if got := h.Get(secure.HeaderCSP); strings.Contains(got, "nonce-") {
		t.Errorf("CSP names a nonce no handler asked for: %q", got)
	}
	if got := h.Get(secure.HeaderCacheControl); got != "" {
		t.Errorf("%s = %q on a response with no nonce; an immutable build asset must stay cacheable",
			secure.HeaderCacheControl, got)
	}
}

func TestNonceResponseIsNotStoredByCaches(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}

	var nonce string
	var ok bool
	h := serve(t, p, nonceHandler(t, &nonce, &ok))
	if got := h.Get(secure.HeaderCacheControl); got != "no-store" {
		t.Errorf("%s = %q on a nonce-bearing response, want %q; a cached nonce is a nonce an attacker knows",
			secure.HeaderCacheControl, got, "no-store")
	}

	// A handler that made its own, more precise choice keeps it.
	h = serve(t, p, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(secure.HeaderCacheControl, "private, no-store, max-age=0")
		if _, minted := secure.Nonce(r.Context()); !minted {
			t.Error("Nonce reported no nonce")
		}
		w.WriteHeader(http.StatusOK)
	}))
	if got := h.Get(secure.HeaderCacheControl); got != "private, no-store, max-age=0" {
		t.Errorf("%s = %q, want the handler's own value to survive", secure.HeaderCacheControl, got)
	}
}

func TestAPIPolicyHasNoNonce(t *testing.T) {
	t.Parallel()

	p, err := secure.NewAPIPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewAPIPolicy: %v", err)
	}
	var nonce string
	var ok bool
	h := serve(t, p, nonceHandler(t, &nonce, &ok))
	if ok || nonce != "" {
		t.Errorf("Nonce under an API policy returned (%q, %v), want (\"\", false)", nonce, ok)
	}
	if got := h.Get(secure.HeaderCSP); strings.Contains(got, "nonce-") {
		t.Errorf("API CSP carries a nonce: %q", got)
	}
	if got := h.Get(secure.HeaderCacheControl); got != "" {
		t.Errorf("API response set %s = %q with no nonce to protect", secure.HeaderCacheControl, got)
	}
}

func TestNonceWithoutMiddlewareReportsFalse(t *testing.T) {
	t.Parallel()

	got, ok := secure.Nonce(context.Background())
	if ok || got != "" {
		t.Errorf("Nonce(background) = (%q, %v), want (\"\", false)", got, ok)
	}
}

// TestHandlerCannotRemoveAProtection is the runtime half of the "an
// application cannot silently weaken a protection" property. The construction
// half is enforced by the option names; this is what stops a handler from
// undoing the result afterwards.
func TestHandlerCannotRemoveAProtection(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}

	sabotage := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Del(secure.HeaderHSTS)
		h.Del(secure.HeaderFrameOptions)
		h.Del(secure.HeaderContentTypeOptions)
		h.Set(secure.HeaderCSP, "default-src *; script-src 'unsafe-inline'")
		h.Set(secure.HeaderReferrerPolicy, "unsafe-url")
		h.Set(secure.HeaderCOOP, "unsafe-none")
		h.Set(secure.HeaderCORP, "cross-origin")
		h.Set(secure.HeaderPermissionsPolicy, "camera=*")
		if _, err := w.Write([]byte("ok")); err != nil {
			t.Errorf("write: %v", err)
		}
	})

	assertExactHeaders(t, serve(t, p, sabotage), map[string]string{
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

func TestInformationalResponseDoesNotFreezeAWeakenedFinalPolicy(t *testing.T) {
	t.Parallel()
	policy, err := secure.NewAPIPolicy(secure.Development)
	if err != nil {
		t.Fatalf("NewAPIPolicy: %v", err)
	}
	target := &policyStatusRecorder{header: make(http.Header)}
	secure.Middleware(policy)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(secure.HeaderCSP, "default-src *")
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Set(secure.HeaderCSP, "default-src *; script-src *")
		w.WriteHeader(http.StatusCreated)
	})).ServeHTTP(target, httptest.NewRequest(http.MethodGet, "/", nil))

	if got, want := target.statuses, []int{http.StatusEarlyHints, http.StatusCreated}; !reflect.DeepEqual(got, want) {
		t.Fatalf("status sequence = %v, want %v", got, want)
	}
	for index, header := range target.snapshots {
		if got := header.Get(secure.HeaderCSP); got != policy.ContentSecurityPolicy() {
			t.Errorf("status %d CSP = %q, want %q", target.statuses[index], got, policy.ContentSecurityPolicy())
		}
	}
}

func TestHijackFinalizesSecurityPolicyHeaders(t *testing.T) {
	t.Parallel()
	policy, err := secure.NewAPIPolicy(secure.Development)
	if err != nil {
		t.Fatalf("NewAPIPolicy: %v", err)
	}
	target := newPolicyHijacker()
	secure.Middleware(policy)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(secure.HeaderCSP, "default-src *")
		if _, _, err := http.NewResponseController(w).Hijack(); err != nil {
			t.Errorf("Hijack: %v", err)
		}
	})).ServeHTTP(target, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := target.snapshot.Get(secure.HeaderCSP); got != policy.ContentSecurityPolicy() {
		t.Fatalf("hijack CSP = %q, want %q", got, policy.ContentSecurityPolicy())
	}
}

func TestDevelopmentPolicyDeletesAStrayHSTSHeader(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Development)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	h := serve(t, p, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(secure.HeaderHSTS, "max-age=31536000")
		w.WriteHeader(http.StatusOK)
	}))
	if got := h.Get(secure.HeaderHSTS); got != "" {
		t.Errorf("%s = %q in development; it must be removed, not merely not added", secure.HeaderHSTS, got)
	}
}

func TestHeadersAreSetOnEveryResponseShape(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}

	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name:    "writes nothing at all",
			handler: func(http.ResponseWriter, *http.Request) {},
		},
		{
			name: "writes a status only",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			name: "writes a body without a status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				if _, err := w.Write([]byte("body")); err != nil {
					t.Errorf("write: %v", err)
				}
			},
		},
		{
			name: "fails with an error status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "nope", http.StatusInternalServerError)
			},
		},
		{
			name: "redirects",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/elsewhere", http.StatusFound)
			},
		},
		{
			name: "flushes mid-response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				if _, err := w.Write([]byte("chunk")); err != nil {
					t.Errorf("write: %v", err)
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				} else {
					t.Error("wrapped ResponseWriter does not implement http.Flusher")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := serve(t, p, tc.handler)
			for _, name := range []string{
				secure.HeaderCSP,
				secure.HeaderHSTS,
				secure.HeaderContentTypeOptions,
				secure.HeaderReferrerPolicy,
				secure.HeaderCOOP,
				secure.HeaderCORP,
				secure.HeaderPermissionsPolicy,
				secure.HeaderFrameOptions,
			} {
				if h.Get(name) == "" {
					t.Errorf("header %s is missing", name)
				}
			}
		})
	}
}

func TestMiddlewarePanicsOnANilPolicy(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("Middleware(nil) did not panic; a nil policy would serve every response unprotected")
		}
	}()
	secure.Middleware(nil)
}

func TestResponseWriterUnwrapsForResponseController(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	var flushErr error
	rec := httptest.NewRecorder()
	secure.Middleware(p)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, writeErr := w.Write([]byte("x")); writeErr != nil {
			t.Errorf("write: %v", writeErr)
		}
		flushErr = http.NewResponseController(w).Flush()
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if flushErr != nil {
		t.Errorf("ResponseController.Flush through the wrapper: %v", flushErr)
	}
	if got := rec.Result().Header.Get(secure.HeaderCSP); got != wantDocumentCSP {
		t.Errorf("%s = %q, want %q", secure.HeaderCSP, got, wantDocumentCSP)
	}
}

// TestNonceResponseCannotBePubliclyCached closes the hole the self-review
// found: a handler that set a caching directive of its own would have kept
// it, and a nonce a shared cache hands to a second visitor is a nonce an
// attacker can read and reuse.
func TestNonceResponseCannotBePubliclyCached(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}

	tests := []struct {
		name    string
		handler string
		want    string
	}{
		{name: "handler sets nothing", handler: "", want: "no-store"},
		{name: "handler asks for public caching", handler: "public, max-age=3600", want: "no-store"},
		{name: "handler asks for private caching", handler: "private, max-age=600", want: "no-store"},
		{name: "handler asks for revalidation only", handler: "no-cache", want: "no-store"},
		{
			name:    "handler already says no-store",
			handler: "private, no-store, max-age=0",
			want:    "private, no-store, max-age=0",
		},
		{name: "handler says NO-STORE in caps", handler: "NO-STORE", want: "NO-STORE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := serve(t, p, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.handler != "" {
					w.Header().Set(secure.HeaderCacheControl, tc.handler)
				}
				if _, minted := secure.Nonce(r.Context()); !minted {
					t.Error("Nonce reported no nonce")
				}
				w.WriteHeader(http.StatusOK)
			}))
			if got := h.Get(secure.HeaderCacheControl); got != tc.want {
				t.Errorf("%s = %q, want %q", secure.HeaderCacheControl, got, tc.want)
			}
		})
	}
}

// TestHeadersSurviveAPanickingHandler checks that a recovery middleware
// mounted outside this one still sends its response with the policy in
// place.
func TestHeadersSurviveAPanickingHandler(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}

	rec := httptest.NewRecorder()
	inner := secure.Middleware(p)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not propagate to an outer recovery middleware")
			}
			rec.WriteHeader(http.StatusInternalServerError)
		}()
		inner.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	if got := rec.Result().Header.Get(secure.HeaderCSP); got != wantDocumentCSP {
		t.Errorf("%s after a panic = %q, want %q", secure.HeaderCSP, got, wantDocumentCSP)
	}
	if got := rec.Result().Header.Get(secure.HeaderHSTS); got == "" {
		t.Errorf("%s is missing after a panic", secure.HeaderHSTS)
	}
}

// TestReportOnlyIsARecordedWaiver is the other half of the self-review fix.
// A report-only policy enforces nothing, so an application asserting it
// carries no waivers must not pass while shipping one.
func TestReportOnlyIsARecordedWaiver(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(
		secure.Production,
		secure.AllowReportOnly("tightening connect-src; enforcing again after the canary week"),
	)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	waivers := p.Waivers()
	if len(waivers) != 1 {
		t.Fatalf("got %d waivers, want 1: %v", len(waivers), waivers)
	}
	if waivers[0].Control != "content-security-policy" {
		t.Errorf("waiver control = %q, want %q", waivers[0].Control, "content-security-policy")
	}
	if !strings.Contains(waivers[0].Detail, "not enforced") {
		t.Errorf("waiver detail = %q, want it to say the policy is not enforced", waivers[0].Detail)
	}
}

// TestMiddlewareRejectsAnUnconstructedPolicy covers the composite-literal
// route into the same defect the nil check closes.
//
// Policy is exported with only unexported fields, which stops an application
// from setting one but not from writing secure.Policy{}, nor from declaring
// a struct field of type secure.Policy and passing its address. Such a value
// is non-nil, so the nil check alone let it through: it produced an empty
// Content-Security-Policy, no Strict-Transport-Security, and no
// X-Frame-Options, while Waivers() returned an empty slice — so the
// assertion this package documents as the proof that a policy is unweakened
// passed on a policy that enforced nothing.
func TestMiddlewareRejectsAnUnconstructedPolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]func() *secure.Policy{
		"nil pointer":        func() *secure.Policy { return nil },
		"composite literal":  func() *secure.Policy { return &secure.Policy{} },
		"zero struct field":  func() *secure.Policy { var p secure.Policy; return &p },
		"copied zero policy": func() *secure.Policy { p := secure.Policy{}; q := p; return &q },
	}
	for name, makePolicy := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Error("Middleware accepted an unconstructed Policy; it would serve every response unprotected")
				}
			}()
			secure.Middleware(makePolicy())
		})
	}
}

// TestConstructedPolicyIsAccepted is the companion to the test above: the
// guard must not reject a policy a constructor really produced.
func TestConstructedPolicyIsAccepted(t *testing.T) {
	t.Parallel()

	for _, deployment := range []secure.Deployment{secure.Development, secure.Production} {
		for name, build := range map[string]func(secure.Deployment) (*secure.Policy, error){
			"document": func(d secure.Deployment) (*secure.Policy, error) {
				return secure.NewDocumentPolicy(d)
			},
			"api": func(d secure.Deployment) (*secure.Policy, error) {
				return secure.NewAPIPolicy(d)
			},
		} {
			t.Run(string(deployment)+"/"+name, func(t *testing.T) {
				t.Parallel()
				p, err := build(deployment)
				if err != nil {
					t.Fatalf("building policy: %v", err)
				}
				if got := serve(t, p, nil).Get(secure.HeaderCSP); got == "" {
					t.Error("a constructed policy produced no Content-Security-Policy")
				}
			})
		}
	}
}

type policyStatusRecorder struct {
	header    http.Header
	statuses  []int
	snapshots []http.Header
}

func (w *policyStatusRecorder) Header() http.Header { return w.header }

func (w *policyStatusRecorder) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
	w.snapshots = append(w.snapshots, w.header.Clone())
}

func (w *policyStatusRecorder) Write(data []byte) (int, error) { return len(data), nil }

type policyHijacker struct {
	header   http.Header
	snapshot http.Header
	rw       *bufio.ReadWriter
}

func newPolicyHijacker() *policyHijacker {
	var output bytes.Buffer
	return &policyHijacker{
		header: make(http.Header),
		rw: bufio.NewReadWriter(
			bufio.NewReader(strings.NewReader("")),
			bufio.NewWriter(&output),
		),
	}
}

func (w *policyHijacker) Header() http.Header { return w.header }

func (w *policyHijacker) WriteHeader(int) {}

func (w *policyHijacker) Write(data []byte) (int, error) { return len(data), nil }

func (w *policyHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.snapshot = w.header.Clone()
	return nil, w.rw, nil
}
