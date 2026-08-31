package forwarded

import (
	"context"
	"net/http"
	"net/netip"
)

// contextKey is unexported, so nothing outside this package can place an edge
// view in a request. A client address any package could write would be exactly
// the forgeable value this package exists to prevent.
type contextKey struct{}

// Middleware resolves the edge view once per request and stores it in the
// context.
//
// Resolving once matters for more than cost: a later handler that re-parsed the
// headers under a different assumption would produce a second, disagreeing
// answer to "who is asking", and the two would be used by different controls.
func (p Policy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, p.Resolve(r))))
	})
}

// FromContext returns the resolved edge view, and whether the middleware ran.
func FromContext(ctx context.Context) (Request, bool) {
	resolved, ok := ctx.Value(contextKey{}).(Request)
	return resolved, ok
}

// ClientIP returns the address to attribute a request to, or the invalid zero
// address when the middleware did not run.
//
// A caller that rate-limits or audits by address must treat the invalid case as
// a wiring failure rather than as a shared bucket every request falls into.
func ClientIP(ctx context.Context) netip.Addr {
	resolved, ok := FromContext(ctx)
	if !ok {
		return netip.Addr{}
	}
	return resolved.ClientIP
}
