package logging

import (
	"context"
	"log/slog"
)

// contextKey is an unexported type so keys this package puts in a
// context.Context cannot collide with a key defined by another package.
type contextKey int

const (
	requestIDContextKey contextKey = iota
	correlationIDContextKey
	loggerContextKey
)

// WithRequestID returns a copy of ctx carrying id as the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDContextKey, id)
}

// RequestID returns the request ID carried by ctx and whether one was
// present. The request ID identifies a single request to this process; it
// does not follow that request across a process or job boundary — see
// CorrelationID for that.
func RequestID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDContextKey).(string)
	return id, ok
}

// WithCorrelationID returns a copy of ctx carrying id as the correlation ID.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDContextKey, id)
}

// CorrelationID returns the correlation ID carried by ctx and whether one was
// present. The correlation ID follows a logical operation across process,
// request, and job boundaries — for example a browser request that enqueues
// a background job whose logs should still be traceable back to the request
// that created it.
func CorrelationID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(correlationIDContextKey).(string)
	return id, ok
}

// WithLogger returns a copy of ctx carrying logger as the request-scoped
// logger, typically one produced by Middleware with the request and
// correlation IDs already attached as attributes.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey, logger)
}

// FromContext returns the logger carried by ctx. If none was stored — for
// example because Middleware was never applied to this request — it returns
// slog.Default(). This package never calls slog.SetDefault itself; whatever
// slog.Default() returns is whatever the application, or the standard
// library's own zero-value default, established.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerContextKey).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.Default()
}
