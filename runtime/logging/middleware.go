package logging

import (
	"log/slog"
	"net/http"
)

// DefaultRequestIDHeader is the header Middleware reads and sets for the
// request ID when MiddlewareOptions.RequestIDHeader is empty.
const DefaultRequestIDHeader = "X-Request-Id"

// DefaultCorrelationIDHeader is the header Middleware reads and sets for the
// correlation ID when MiddlewareOptions.CorrelationIDHeader is empty.
const DefaultCorrelationIDHeader = "X-Correlation-Id"

// MiddlewareOptions configures Middleware.
type MiddlewareOptions struct {
	// TrustInbound, when true, accepts a request ID and a correlation ID
	// supplied by the caller in RequestIDHeader and CorrelationIDHeader,
	// provided each value passes ValidID. When false — the zero value, and
	// the safe default — inbound values are always ignored and a fresh ID is
	// generated with NewID for every request.
	//
	// Only set this true behind an ingress that itself sanitizes or
	// generates these headers, such as a load balancer or an internal
	// service-mesh hop. Accepting them directly from a browser or another
	// untrusted client lets that client choose the identifier your logs use
	// to correlate its requests. ValidID's bounded length and restricted
	// character set are the mitigation against that client injecting an
	// arbitrary or oversized value into your logs; TrustInbound does not
	// disable that validation, it only decides whether an inbound value is
	// considered at all.
	TrustInbound bool

	// RequestIDHeader overrides DefaultRequestIDHeader.
	RequestIDHeader string

	// CorrelationIDHeader overrides DefaultCorrelationIDHeader.
	CorrelationIDHeader string
}

// Middleware returns net/http middleware that, for every request:
//
//   - resolves a request ID and a correlation ID — from the matching inbound
//     header when opts.TrustInbound is true and the header value passes
//     ValidID, otherwise freshly generated with NewID;
//   - stores both in the request's context (see RequestID and CorrelationID);
//   - attaches both to logger as attributes and stores that request-scoped
//     logger in the context (see FromContext);
//   - sets both IDs as response headers;
//
// before calling the next handler with the updated request.
//
// Middleware never logs the request or response body, or any part of
// either.
func Middleware(logger *slog.Logger, opts MiddlewareOptions) func(http.Handler) http.Handler {
	// A nil logger is never a valid argument: this middleware calls
	// logger.With on every request, so accepting nil turns a wiring mistake
	// into a nil-pointer dereference on the first request the process
	// serves, in a middleware whose whole job is to make failures legible.
	// Failing here instead puts it in the startup path, where the stack
	// names the wiring site. Substituting slog.Default() would be worse:
	// the process would run, logs would go somewhere nobody configured, and
	// the mistake would surface as missing output rather than as an error.
	if logger == nil {
		panic("runtime/logging: Middleware requires a non-nil *slog.Logger; " +
			"build one with slog.New(logging.NewJSONHandler(...)) or slog.New(logging.NewTextHandler(...)) and pass it explicitly")
	}

	requestHeader := opts.RequestIDHeader
	if requestHeader == "" {
		requestHeader = DefaultRequestIDHeader
	}
	correlationHeader := opts.CorrelationIDHeader
	if correlationHeader == "" {
		correlationHeader = DefaultCorrelationIDHeader
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := resolveID(r.Header.Get(requestHeader), opts.TrustInbound)
			correlationID := resolveID(r.Header.Get(correlationHeader), opts.TrustInbound)

			ctx := WithRequestID(r.Context(), requestID)
			ctx = WithCorrelationID(ctx, correlationID)
			ctx = WithLogger(ctx, logger.With(
				slog.String(RequestIDKey, requestID),
				slog.String(CorrelationIDKey, correlationID),
			))

			w.Header().Set(requestHeader, requestID)
			w.Header().Set(correlationHeader, correlationID)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveID returns inbound when trust is true and inbound passes ValidID,
// and a freshly generated ID otherwise.
func resolveID(inbound string, trust bool) string {
	if trust && ValidID(inbound) {
		return inbound
	}
	return NewID()
}
