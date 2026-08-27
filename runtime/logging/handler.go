package logging

import (
	"io"
	"log/slog"
)

// HandlerOptions configures NewJSONHandler and the parts of NewTextHandler it
// shares with NewJSONHandler.
type HandlerOptions struct {
	// Level is the minimum level the handler emits. A nil Level uses
	// slog.LevelInfo. The caller always sets this explicitly, directly or by
	// leaving it nil for the documented default; this package never reads an
	// environment variable or consults a global to choose it.
	Level slog.Leveler

	// Redact configures the central redaction NewJSONHandler and
	// NewTextHandler always apply. The zero value uses DefaultDenyKeys with
	// no extensions.
	Redact RedactOptions
}

// NewJSONHandler returns a slog.Handler that writes one JSON object per
// record to w, intended for production. Every attribute passes through
// central redaction (NewRedactingHandler) before it reaches the JSON
// encoder, so redaction cannot be bypassed by an attribute added after
// construction — see NewRedactingHandler for exactly what that covers.
//
// NewJSONHandler never calls slog.SetDefault. The caller decides whether and
// how the returned handler becomes a *slog.Logger's handler, typically with
// slog.New.
func NewJSONHandler(w io.Writer, opts HandlerOptions) slog.Handler {
	level := opts.Level
	if level == nil {
		level = slog.LevelInfo
	}
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return NewRedactingHandler(base, opts.Redact)
}
