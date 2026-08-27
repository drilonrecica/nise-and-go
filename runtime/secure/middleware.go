package secure

import (
	"context"
	"net/http"
)

// Middleware returns net/http middleware that writes p's security headers
// onto every response it wraps.
//
// Mount it as the outermost middleware in the chain, so that a response
// produced by an inner middleware — a rejected request, a rate-limit refusal,
// a panic recovered further in — carries the headers too.
//
// A generated application mounts two of these, one per policy:
//
//	documentChain := secure.Middleware(documentPolicy)
//	apiChain := secure.Middleware(apiPolicy)
//
//	mux.Handle("/api/v1/", apiChain(apiRouter))
//	mux.Handle("/", documentChain(webui.Handler()))
//
// Middleware panics if p is nil. A nil policy is a wiring mistake that would
// otherwise serve every response with no security headers at all, and the
// project's fail-closed rule makes that a startup failure rather than a
// silent one.
//
// # The headers are written last
//
// The headers are applied immediately before the response's status line is
// written, and they overwrite whatever is already in the header map. A
// handler further in the chain that set a weaker Referrer-Policy, or deleted
// Strict-Transport-Security, does not get to keep the change: the policy has
// the last word. That is what makes [Policy]'s "no setters, only options"
// design mean something at runtime rather than only at construction.
func Middleware(p *Policy) func(http.Handler) http.Handler {
	if p == nil {
		panic("runtime/secure: Middleware called with a nil Policy")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			state := &nonceState{enabled: p.nonceEnabled}
			wrapped := &responseWriter{ResponseWriter: w, policy: p, nonce: state}

			ctx := context.WithValue(r.Context(), nonceContextKey, state)
			next.ServeHTTP(wrapped, r.WithContext(ctx))

			// A handler that wrote neither a status nor a body leaves
			// net/http to write 200 after this returns, which is still
			// early enough for the header map to matter.
			wrapped.applyPolicy()
		})
	}
}

// responseWriter applies the policy's headers at the moment the response is
// committed, which is the last point at which a nonce minted by the handler
// can still be named in the Content-Security-Policy header.
type responseWriter struct {
	http.ResponseWriter

	policy  *Policy
	nonce   *nonceState
	applied bool
}

// applyPolicy writes the policy headers once per response.
func (w *responseWriter) applyPolicy() {
	if w.applied {
		return
	}
	w.applied = true
	w.policy.apply(w.Header(), w.nonce)
}

// WriteHeader applies the policy headers before committing the status line.
func (w *responseWriter) WriteHeader(statusCode int) {
	w.applyPolicy()
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write applies the policy headers before the implicit 200 that the first
// Write would otherwise commit.
func (w *responseWriter) Write(b []byte) (int, error) {
	w.applyPolicy()
	return w.ResponseWriter.Write(b)
}

// Flush applies the policy headers and then flushes, so that a streaming
// handler commits its response with the headers already in place.
//
// The flush is delegated through [net/http.ResponseController], which finds
// the real flusher through Unwrap; a ResponseWriter that cannot flush reports
// [net/http.ErrNotSupported], which is not an error this wrapper can act on
// and which the caller could not have acted on either.
func (w *responseWriter) Flush() {
	w.applyPolicy()
	//nolint:errcheck // Flush has no error return to propagate through.
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap returns the wrapped ResponseWriter, so that
// [net/http.ResponseController] can reach capabilities this wrapper does not
// implement itself — Hijack, SetReadDeadline, SetWriteDeadline.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
