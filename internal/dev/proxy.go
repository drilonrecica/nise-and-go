package dev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// forwardedHeaders are the X-Forwarded-* headers the proxy rewrites. They
// are deleted from the inbound request before new values are written,
// because this proxy is the first hop in front of the browser: anything a
// client sent under these names is a claim about a hop that does not exist,
// and httputil.ProxyRequest.SetXForwarded would otherwise append the real
// client address to whatever chain the client invented.
var forwardedHeaders = []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"}

// ProxyConfig is everything NewProxy needs. Every field is explicit; the
// proxy reads no environment variable and consults no global.
type ProxyConfig struct {
	// App is the base URL of the Go application child, e.g.
	// http://127.0.0.1:8081. Required.
	App *url.URL
	// Web is the base URL of the Vite development server, e.g.
	// http://127.0.0.1:5173. Required.
	Web *url.URL
	// AppPrefixes overrides DefaultAppPrefixes when non-empty.
	AppPrefixes []string
	// OnError, when non-nil, is called once per failed forward with the
	// upstream that could not be reached and a one-line reason. It is how
	// a proxy failure reaches the developer's terminal; the proxy itself
	// prints nothing.
	OnError func(up Upstream, reason string)
}

// Proxy is the single origin a developer's browser talks to. It forwards
// each request to the Go child or to Vite according to its Router, and it
// is the only component in the dev loop that a browser ever connects to —
// which is what makes the dev origin, the cookie origin, and the origin the
// Go application believes it is serving one and the same thing.
//
// Three properties are deliberate and are covered by tests:
//
//   - Set-Cookie is never touched. Whatever the Go child sets, the browser
//     receives byte for byte, including the Path, Secure, SameSite, and
//     __Host- prefix the application chose.
//   - The inbound Host header is forwarded unchanged. The Go child
//     therefore sees the same Host, Origin, Referer, and Sec-Fetch-* values
//     a production deployment would see, so an origin-based request-forgery
//     check needs no development exemption.
//   - Hop-by-hop headers are stripped and a websocket upgrade is passed
//     through, so Vite's hot-module-reload socket rides the same origin as
//     the document.
type Proxy struct {
	router Router
	app    *httputil.ReverseProxy
	web    *httputil.ReverseProxy
	appURL *url.URL
	webURL *url.URL
}

// NewProxy builds a Proxy. It fails rather than defaulting when either
// upstream URL is missing or is not an absolute http/https URL: a proxy
// silently pointed at the wrong place is worse than one that refuses to
// start.
func NewProxy(cfg ProxyConfig) (*Proxy, error) {
	if err := validUpstreamURL("app", cfg.App); err != nil {
		return nil, err
	}
	if err := validUpstreamURL("web", cfg.Web); err != nil {
		return nil, err
	}
	p := &Proxy{
		router: NewRouter(cfg.AppPrefixes),
		appURL: cfg.App,
		webURL: cfg.Web,
	}
	p.app = p.newReverseProxy(UpstreamApp, cfg.App, cfg.OnError)
	p.web = p.newReverseProxy(UpstreamWeb, cfg.Web, cfg.OnError)
	return p, nil
}

// Router returns the routing rule this proxy applies, for a caller that
// wants to print it at startup.
func (p *Proxy) Router() Router { return p.router }

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.router.Upstream(r.URL.Path) == UpstreamApp {
		p.app.ServeHTTP(w, r)
		return
	}
	p.web.ServeHTTP(w, r)
}

// newReverseProxy builds the httputil.ReverseProxy for one upstream.
//
// FlushInterval is -1 (flush immediately, never buffer). A development
// proxy carries Vite's hot-module-reload traffic and, later, server-sent
// events from the application; buffering either one turns "instant" into
// "eventually" with no error anywhere to explain it.
func (p *Proxy) newReverseProxy(up Upstream, target *url.URL, onError func(Upstream, string)) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// SetURL clears Out.Host, which would make the outbound
			// request carry the upstream's loopback host:port. Restore
			// the browser's own Host instead: the Go child must see the
			// Host a production deployment would see, because that is
			// what an Origin/Host request-forgery comparison is made
			// against. Vite accepts a loopback Host unconditionally, so
			// forwarding it costs nothing on that side either.
			pr.Out.Host = pr.In.Host
			for _, h := range forwardedHeaders {
				pr.Out.Header.Del(h)
			}
			pr.SetXForwarded()
		},
		FlushInterval: -1,
		ErrorHandler:  p.errorHandler(up, target, onError),
	}
}

// errorHandler renders the page a developer actually sees when an upstream
// is down, and reports the failure once to onError.
//
// A client that hung up mid-request is not an upstream failure and is not
// reported: a browser navigating away cancels in-flight requests as a
// matter of course, and a dev loop that logs a line every time would train
// its reader to ignore the log.
func (p *Proxy) errorHandler(up Upstream, target *url.URL, onError func(Upstream, string)) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
			return
		}
		if onError != nil {
			onError(up, fmt.Sprintf("%s %s: %v", r.Method, r.URL.Path, err))
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// A bad gateway from a restarting child must never be cached, or
		// the browser will keep showing it after the child is back.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(badGatewayBody(up, target, r)))
	}
}

// badGatewayBody is the body of the 502 page. It names which upstream is
// unreachable, what normally causes that, and what to do — the same
// cause/recovery shape docs/cli-output.md requires of a CLI error, because
// this page is read in exactly the same situation.
//
// It contains no error string from the transport: a dial error carries a
// loopback address and nothing a developer can act on that the two lines
// below do not already say, and the full reason is written to the terminal
// through OnError.
func badGatewayBody(up Upstream, target *url.URL, r *http.Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "502 Bad Gateway — nise dev could not reach the %q upstream at %s\n\n", up, target)
	fmt.Fprintf(&b, "request: %s %s\n\n", r.Method, r.URL.Path)
	switch up {
	case UpstreamApp:
		b.WriteString("The Go application is not listening. It is normally rebuilding after a\n")
		b.WriteString("source change, in which case this resolves in a second or two — reload.\n")
		b.WriteString("If it does not, look for a build error or a startup error in the [app]\n")
		b.WriteString("lines in the terminal running nise dev.\n")
	case UpstreamWeb:
		b.WriteString("The Vite development server is not listening. Look for a [web] error in\n")
		b.WriteString("the terminal running nise dev; a missing frontend/node_modules is the\n")
		b.WriteString("usual cause, fixed by \"pnpm --dir frontend install\".\n")
	}
	return b.String()
}

// validUpstreamURL rejects an upstream URL that cannot be dialed.
func validUpstreamURL(name string, u *url.URL) error {
	if u == nil {
		return fmt.Errorf("dev: the %s upstream URL is required", name)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("dev: the %s upstream URL must be http or https, found %q", name, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("dev: the %s upstream URL has no host: %q", name, u.String())
	}
	return nil
}
