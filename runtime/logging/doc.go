// Package logging provides structured logging over the standard library's
// log/slog: a JSON handler for production, a concise human-readable handler
// for local development, request and correlation IDs propagated through
// context.Context and HTTP headers, and a central redaction handler wrapper
// that every constructor in this package applies automatically.
//
// This package imports only the standard library.
//
// # No global logger
//
// This package never constructs a *slog.Logger and never calls
// slog.SetDefault. NewJSONHandler and NewTextHandler each return a
// slog.Handler; the caller wraps it with slog.New and decides how, or
// whether, it becomes the process's default logger. The level is always
// supplied explicitly by the caller through HandlerOptions.Level — this
// package reads no environment variable and consults no global to choose it.
//
// # runtime/config is not a dependency
//
// This package does not import runtime/config, and must not: ADR 0011 fixes
// runtime/logging and runtime/config as independent packages so that either
// can be imported without the other. An application wires a runtime/config
// value into a logging constructor itself, at the call site.
//
// # Request and correlation IDs
//
// A request ID identifies one request to this process. A correlation ID
// follows a logical operation across process, request, and job boundaries —
// for example a browser request that enqueues a background job whose logs
// should still be traceable back to the request that created it. Middleware
// assigns both for every HTTP request; NewID generates one directly for a
// job or any other non-HTTP context.
//
// Accepting either ID from an inbound header is opt-in
// (MiddlewareOptions.TrustInbound) and always validated (ValidID): bounded
// length, a restricted character set. An untrusted client cannot use this
// path to inject an arbitrary or oversized value into your logs — see
// Middleware and ValidID.
//
// # Redaction
//
// NewJSONHandler and NewTextHandler both wrap their output in
// NewRedactingHandler, so central redaction is not something a caller can
// forget to apply: any attribute whose key is in the deny set (case
// insensitive, exact match — see DefaultDenyKeys) is replaced before it
// reaches the encoder, including a key nested inside slog.Group, WithGroup,
// or a value returned by a slog.LogValuer.
//
// Redaction is key-based. It has no way to detect a secret that has been
// written into a message string ("connecting with password hunter2") or
// otherwise concatenated into a value that does not itself carry a
// deny-set key — that is a call-site discipline problem this package cannot
// solve for you. Redact addresses the specific case of a connection-string
// or URL value by stripping userinfo and credential-shaped query parameters
// structurally, instead of relying on an attribute key existing at all.
//
// See docs/observability.md for the full log format contract, ID semantics
// and trust rules, and the redaction guarantee and its limits.
package logging
