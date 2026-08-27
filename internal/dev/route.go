package dev

import (
	"sort"
	"strings"
)

// Upstream names one of the two processes the development proxy forwards
// to. The value is what appears in a proxy log line and in the body of a
// bad-gateway page, so it is short and stable.
type Upstream string

const (
	// UpstreamApp is the Go application child process.
	UpstreamApp Upstream = "app"
	// UpstreamWeb is the Vite development server.
	UpstreamWeb Upstream = "web"
)

// DefaultAppPrefixes are the request-path prefixes routed to the Go child
// rather than to Vite. They are exactly the subtrees the generated
// application's own router mounts ahead of its catch-all — see the
// generated internal/platform/httpapi/router.go — so the split here mirrors
// that file rather than inventing a second, divergent map of the URL space:
//
//   - /api      the JSON API, mounted under /api/v1
//   - /healthz  the startup, liveness, and readiness probes
//   - /internal the operator subtree (metrics, detailed readiness report)
//
// Everything else — the document, the client bundle, Vite's own /@vite/,
// /@fs/, and /node_modules/ paths, and the hot-module-reload websocket —
// belongs to Vite.
var DefaultAppPrefixes = []string{"/api", "/healthz", "/internal"}

// Router decides which upstream a request path belongs to. It is a value,
// not a mutable registry: build one with NewRouter and it never changes.
type Router struct {
	// prefixes is normalized (leading slash, no trailing slash, no empty
	// entry, deduplicated) and sorted longest-first so that the most
	// specific prefix wins when two overlap.
	prefixes []string
}

// NewRouter returns a Router that sends every path under one of prefixes to
// the Go child and every other path to Vite. A nil or empty prefixes slice
// selects DefaultAppPrefixes; pass a non-empty slice to override it
// entirely rather than to add to it, so what a project routes to Go is
// always readable in one place.
//
// A prefix matches on path segment boundaries only: "/api" matches "/api"
// and "/api/v1/widgets" but never "/apiary". Matching on a raw string
// prefix instead would silently hand a Vite-owned route to the Go child the
// first time somebody named a page "/apiary", and the symptom — one page
// 404s, everything else works — is a bad afternoon.
func NewRouter(prefixes []string) Router {
	if len(prefixes) == 0 {
		prefixes = DefaultAppPrefixes
	}
	seen := make(map[string]bool, len(prefixes))
	normalized := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		p = normalizePrefix(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		normalized = append(normalized, p)
	}
	// Longest first, then lexicographic, so the result is deterministic
	// for a given input set regardless of the order it arrived in.
	sort.Slice(normalized, func(i, j int) bool {
		if len(normalized[i]) != len(normalized[j]) {
			return len(normalized[i]) > len(normalized[j])
		}
		return normalized[i] < normalized[j]
	})
	if len(normalized) == 0 {
		// Every supplied prefix normalized away (all empty, or all "/").
		// Falling back to the defaults is the safe direction: the
		// alternative is a router that sends the API, the probes, and the
		// operator subtree to Vite, which fails as a wall of 404s from the
		// wrong process rather than as anything a reader can diagnose.
		return NewRouter(nil)
	}
	return Router{prefixes: normalized}
}

// Prefixes returns the normalized prefixes routed to the Go child, longest
// first. The returned slice is a copy.
func (r Router) Prefixes() []string {
	out := make([]string, len(r.prefixes))
	copy(out, r.prefixes)
	return out
}

// Upstream reports which process should serve path. An empty or relative
// path is treated as "/" and goes to Vite, which is the safe direction: a
// request nise cannot classify reaches the dev server that owns the
// document, not the process that holds the session cookie.
func (r Router) Upstream(path string) Upstream {
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	for _, p := range r.prefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return UpstreamApp
		}
	}
	return UpstreamWeb
}

// normalizePrefix trims whitespace, adds a leading slash, and removes
// trailing slashes so that "/api/", "api", and " /api " are one prefix.
func normalizePrefix(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p[0] != '/' {
		p = "/" + p
	}
	for len(p) > 1 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	if p == "/" {
		// A "/" prefix would route the entire URL space to the Go child
		// and leave Vite unreachable, which is never what somebody means.
		return ""
	}
	return p
}
