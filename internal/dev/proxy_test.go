package dev

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRouterUpstream(t *testing.T) {
	t.Parallel()
	r := NewRouter(nil)
	tests := []struct {
		name string
		path string
		want Upstream
	}{
		{"api root", "/api", UpstreamApp},
		{"api versioned", "/api/v1/widgets", UpstreamApp},
		{"api trailing slash", "/api/", UpstreamApp},
		{"healthz probe", "/healthz/ready", UpstreamApp},
		{"healthz root", "/healthz", UpstreamApp},
		{"operator subtree", "/internal/metrics", UpstreamApp},
		{"document", "/", UpstreamWeb},
		{"client route", "/settings/profile", UpstreamWeb},
		{"vite client", "/@vite/client", UpstreamWeb},
		{"vite fs", "/@fs/home/x/src/main.ts", UpstreamWeb},
		{"immutable asset", "/_app/immutable/entry/app.js", UpstreamWeb},
		{"prefix is not a substring", "/apiary", UpstreamWeb},
		{"prefix is not a substring, deeper", "/apiary/bees", UpstreamWeb},
		{"internal-looking page", "/internally", UpstreamWeb},
		{"empty path", "", UpstreamWeb},
		{"relative path", "api/v1", UpstreamApp},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := r.Upstream(tc.path); got != tc.want {
				t.Fatalf("Upstream(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestNewRouterNormalizesAndOrders(t *testing.T) {
	t.Parallel()
	r := NewRouter([]string{"api/", " /api ", "/a", "/api/v1", "", "/"})
	got := r.Prefixes()
	want := []string{"/api/v1", "/api", "/a"}
	if len(got) != len(want) {
		t.Fatalf("Prefixes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Prefixes() = %v, want %v", got, want)
		}
	}
	// "/" must never be accepted: it would route the whole URL space to
	// the Go child and leave Vite unreachable.
	if r.Upstream("/some/page") != UpstreamWeb {
		t.Fatal(`a "/" prefix was accepted and swallowed the whole URL space`)
	}
}

func TestNewRouterEmptyFallsBackToDefaults(t *testing.T) {
	t.Parallel()
	for _, in := range [][]string{nil, {}, {"", "  ", "/"}} {
		r := NewRouter(in)
		if r.Upstream("/api/v1") != UpstreamApp || r.Upstream("/") != UpstreamWeb {
			t.Fatalf("NewRouter(%v) did not fall back to the default prefixes: %v", in, r.Prefixes())
		}
	}
}

// capturedRequest is what a recording upstream saw.
type capturedRequest struct {
	Method string
	Path   string
	Host   string
	Header http.Header
}

// recordingUpstream returns a server that records the last request it saw
// and answers with body.
func recordingUpstream(t *testing.T, body string) (*httptest.Server, *capturedRequest) {
	t.Helper()
	var got capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = capturedRequest{Method: r.Method, Path: r.URL.Path, Host: r.Host, Header: r.Header.Clone()}
		w.Header().Set("X-Upstream", body)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// newTestProxy wires a Proxy in front of two upstreams and serves it on a
// real listener, returning the proxy's base URL and the errors it reported.
func newTestProxy(t *testing.T, appURL, webURL string) (base string, errs *[]string) {
	t.Helper()
	au, err := url.Parse(appURL)
	if err != nil {
		t.Fatalf("parsing app URL: %v", err)
	}
	wu, err := url.Parse(webURL)
	if err != nil {
		t.Fatalf("parsing web URL: %v", err)
	}
	reported := make([]string, 0, 2)
	p, err := NewProxy(ProxyConfig{
		App: au,
		Web: wu,
		OnError: func(up Upstream, reason string) {
			reported = append(reported, string(up)+": "+reason)
		},
	})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return srv.URL, &reported
}

func TestProxyRoutesByPath(t *testing.T) {
	t.Parallel()
	app, appGot := recordingUpstream(t, "app")
	web, webGot := recordingUpstream(t, "web")
	base, _ := newTestProxy(t, app.URL, web.URL)

	for _, tc := range []struct {
		path string
		want string
		got  *capturedRequest
	}{
		{"/api/v1/widgets", "app", appGot},
		{"/healthz/ready", "app", appGot},
		{"/internal/metrics", "app", appGot},
		{"/", "web", webGot},
		{"/@vite/client", "web", webGot},
		{"/settings", "web", webGot},
	} {
		resp, err := http.Get(base + tc.path) //nolint:bodyclose // closed below
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != tc.want {
			t.Fatalf("GET %s reached the %q upstream, want %q", tc.path, body, tc.want)
		}
		if tc.got.Path != tc.path {
			t.Fatalf("upstream saw path %q, want %q", tc.got.Path, tc.path)
		}
	}
}

func TestProxyPreservesHostAndBrowserOriginHeaders(t *testing.T) {
	t.Parallel()
	app, appGot := recordingUpstream(t, "app")
	web, _ := recordingUpstream(t, "web")
	base, _ := newTestProxy(t, app.URL, web.URL)

	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/session", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Origin", base)
	req.Header.Set("Referer", base+"/sign-in")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Cookie", "session=opaque")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()

	// The Host the Go child sees must be the browser's host, not the
	// loopback address of the child itself. This is what lets an
	// Origin/Host request-forgery check work in development with no
	// development-only exemption.
	wantHost := strings.TrimPrefix(base, "http://")
	if appGot.Host != wantHost {
		t.Fatalf("upstream saw Host %q, want the browser's host %q", appGot.Host, wantHost)
	}
	for _, h := range []string{"Origin", "Referer", "Sec-Fetch-Site", "Cookie"} {
		if got, want := appGot.Header.Get(h), req.Header.Get(h); got != want {
			t.Fatalf("upstream saw %s = %q, want %q", h, got, want)
		}
	}
	if appGot.Header.Get("Origin") != "http://"+appGot.Host {
		t.Fatalf("Origin %q and Host %q disagree; the single-origin property is broken",
			appGot.Header.Get("Origin"), appGot.Host)
	}
}

func TestProxyStripsHopByHopHeaders(t *testing.T) {
	t.Parallel()
	app, appGot := recordingUpstream(t, "app")
	web, _ := recordingUpstream(t, "web")
	base, _ := newTestProxy(t, app.URL, web.URL)

	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/ping", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	// Connection names an additional header to drop, per RFC 9110 7.6.1.
	req.Header.Set("Connection", "X-Custom-Hop")
	req.Header.Set("X-Custom-Hop", "must-not-arrive")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Proxy-Authenticate", "Basic")
	req.Header.Set("Proxy-Authorization", "Basic Zm9v")
	req.Header.Set("X-End-To-End", "must-arrive")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()

	for _, h := range []string{"X-Custom-Hop", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Connection"} {
		if v := appGot.Header.Get(h); v != "" {
			t.Fatalf("hop-by-hop header %s survived to the upstream with value %q", h, v)
		}
	}
	if got := appGot.Header.Get("X-End-To-End"); got != "must-arrive" {
		t.Fatalf("end-to-end header X-End-To-End = %q, want %q", got, "must-arrive")
	}
}

func TestProxyRewritesForwardedHeadersAndDiscardsClientClaims(t *testing.T) {
	t.Parallel()
	app, appGot := recordingUpstream(t, "app")
	web, _ := recordingUpstream(t, "web")
	base, _ := newTestProxy(t, app.URL, web.URL)

	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/ping", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	// A browser talking straight to this proxy has no upstream hop, so
	// every X-Forwarded-* value here is a claim the client invented.
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("X-Forwarded-Proto", "https")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()

	if got := appGot.Header.Get("X-Forwarded-For"); strings.Contains(got, "203.0.113.7") {
		t.Fatalf("X-Forwarded-For = %q; the client's forged hop was appended instead of discarded", got)
	}
	if got := appGot.Header.Get("X-Forwarded-For"); got == "" {
		t.Fatal("X-Forwarded-For was not set by the proxy")
	}
	if got, want := appGot.Header.Get("X-Forwarded-Host"), strings.TrimPrefix(base, "http://"); got != want {
		t.Fatalf("X-Forwarded-Host = %q, want the real inbound host %q", got, want)
	}
	if got := appGot.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Fatalf("X-Forwarded-Proto = %q, want %q (the dev origin is plain HTTP)", got, "http")
	}
}

func TestProxyPassesSetCookieThroughUnchanged(t *testing.T) {
	t.Parallel()
	// The exact bytes matter: the __Host- prefix requires Secure and
	// Path=/ and forbids Domain, so a proxy that rewrote any of them
	// would make the development loop disagree with production about
	// whether a cookie is even accepted.
	const cookie = "__Host-session=opaque-value; Path=/; Secure; HttpOnly; SameSite=Lax"
	appSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", cookie)
		w.Header().Add("Set-Cookie", "second=value; Path=/api")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(appSrv.Close)
	web, _ := recordingUpstream(t, "web")
	base, _ := newTestProxy(t, appSrv.URL, web.URL)

	// A client that does not parse cookies, so the raw header survives.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(base + "/api/v1/session")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	got := resp.Header.Values("Set-Cookie")
	if len(got) != 2 {
		t.Fatalf("got %d Set-Cookie headers, want 2: %q", len(got), got)
	}
	if got[0] != cookie {
		t.Fatalf("Set-Cookie = %q, want %q", got[0], cookie)
	}
	if got[1] != "second=value; Path=/api" {
		t.Fatalf("second Set-Cookie = %q, want it unchanged", got[1])
	}
}

func TestProxyReportsBadGatewayWhenUpstreamIsDown(t *testing.T) {
	t.Parallel()
	web, _ := recordingUpstream(t, "web")
	// A port nothing listens on: bind one, record it, release it.
	dead := reserveClosedPort(t)
	base, reported := newTestProxy(t, "http://"+dead, web.URL)

	resp, err := http.Get(base + "/api/v1/ping") //nolint:bodyclose // closed below
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q so a stale 502 is not cached", got, "no-store")
	}
	text := string(body)
	for _, want := range []string{"502 Bad Gateway", `"app"`, "/api/v1/ping", "rebuilding"} {
		if !strings.Contains(text, want) {
			t.Fatalf("bad-gateway body does not mention %q:\n%s", want, text)
		}
	}
	if len(*reported) != 1 {
		t.Fatalf("OnError called %d times, want exactly 1: %v", len(*reported), *reported)
	}
	if !strings.HasPrefix((*reported)[0], "app: GET /api/v1/ping:") {
		t.Fatalf("reported reason %q does not name the upstream and request", (*reported)[0])
	}
}

func TestProxyBadGatewayNamesTheViteSideWhenViteIsDown(t *testing.T) {
	t.Parallel()
	app, _ := recordingUpstream(t, "app")
	dead := reserveClosedPort(t)
	base, _ := newTestProxy(t, app.URL, "http://"+dead)

	resp, err := http.Get(base + "/") //nolint:bodyclose // closed below
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
	for _, want := range []string{`"web"`, "Vite", "pnpm --dir frontend install"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("bad-gateway body does not mention %q:\n%s", want, body)
		}
	}
}

func TestNewProxyRejectsUnusableUpstreams(t *testing.T) {
	t.Parallel()
	good, _ := url.Parse("http://127.0.0.1:1")
	tests := []struct {
		name     string
		app, web *url.URL
		wantErr  string
	}{
		{"missing app", nil, good, "app upstream URL is required"},
		{"missing web", good, nil, "web upstream URL is required"},
		{"non-http scheme", mustURL(t, "ftp://127.0.0.1:1"), good, "must be http or https"},
		{"no host", mustURL(t, "http:///path"), good, "has no host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewProxy(ProxyConfig{App: tc.app, Web: tc.web})
			if err == nil {
				t.Fatal("NewProxy accepted an unusable upstream")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestProxyPassesWebsocketUpgradeThrough is the test that matters most for
// hot module reload: Vite's HMR socket must ride the same origin as the
// document, which means it must survive this proxy's hop. It speaks the
// upgrade handshake by hand over a raw connection rather than pulling in a
// websocket library, because the framework has no runtime dependencies.
func TestProxyPassesWebsocketUpgradeThrough(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "not an upgrade", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "not hijackable", http.StatusInternalServerError)
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Protocol: vite-hmr\r\n\r\n")
		if err := brw.Flush(); err != nil {
			return
		}
		// Echo one line, proving the tunnel carries bytes both ways.
		line, err := brw.ReadString('\n')
		if err != nil {
			return
		}
		_, _ = brw.WriteString("echo:" + line)
		_ = brw.Flush()
	}))
	t.Cleanup(upstream.Close)

	app, _ := recordingUpstream(t, "app")
	base, _ := newTestProxy(t, app.URL, upstream.URL)

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(base, "http://"), 5*time.Second)
	if err != nil {
		t.Fatalf("dialing the proxy: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("setting deadline: %v", err)
	}

	host := strings.TrimPrefix(base, "http://")
	_, err = io.WriteString(conn, "GET /?token=hmr HTTP/1.1\r\n"+
		"Host: "+host+"\r\n"+
		"Upgrade: websocket\r\n"+
		"Connection: Upgrade\r\n"+
		"Sec-WebSocket-Version: 13\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Protocol: vite-hmr\r\n\r\n")
	if err != nil {
		t.Fatalf("writing the upgrade request: %v", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("reading the upgrade response: %v", err)
	}
	// A 101 response has no body to drain, but closing it keeps the
	// bodyclose linter honest about every other path through this file.
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101; the proxy did not pass the upgrade through", resp.StatusCode)
	}
	if got := resp.Header.Get("Upgrade"); !strings.EqualFold(got, "websocket") {
		t.Fatalf("Upgrade = %q, want websocket", got)
	}
	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != "vite-hmr" {
		t.Fatalf("Sec-WebSocket-Protocol = %q, want vite-hmr", got)
	}

	if _, err := io.WriteString(conn, "ping\n"); err != nil {
		t.Fatalf("writing through the tunnel: %v", err)
	}
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading through the tunnel: %v", err)
	}
	if line != "echo:ping\n" {
		t.Fatalf("tunnel echoed %q, want %q", line, "echo:ping\n")
	}
}

// reserveClosedPort returns a host:port that was just bound and released,
// so a dial to it fails immediately rather than hanging.
func reserveClosedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing the reserved port: %v", err)
	}
	return addr
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %q: %v", raw, err)
	}
	return u
}
