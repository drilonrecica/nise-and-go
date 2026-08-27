package logging

import (
	"bytes"
	"log/slog"
	"testing"
	"time"
)

// fixedRecord builds a slog.Record with a fixed time so JSON output is
// byte-for-byte comparable across runs.
func fixedRecord(level slog.Level, msg string, attrs ...slog.Attr) slog.Record {
	when := time.Date(2026, time.August, 27, 12, 30, 45, 0, time.UTC)
	r := slog.NewRecord(when, level, msg, 0)
	r.AddAttrs(attrs...)
	return r
}

func TestNewJSONHandler_ExactOutput(t *testing.T) {
	var buf bytes.Buffer
	h := NewJSONHandler(&buf, HandlerOptions{Level: slog.LevelInfo})

	rec := fixedRecord(slog.LevelInfo, "user signed in",
		slog.String("user_id", "u_123"),
		slog.String("password", "hunter2"),
	)
	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := `{"time":"2026-08-27T12:30:45Z","level":"INFO","msg":"user signed in","user_id":"u_123","password":"[REDACTED]"}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("JSON output mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestNewJSONHandler_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	h := NewJSONHandler(&buf, HandlerOptions{Level: slog.LevelWarn})

	if h.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("Enabled(Info) = true, want false when configured level is Warn")
	}
	if !h.Enabled(t.Context(), slog.LevelWarn) {
		t.Error("Enabled(Warn) = false, want true when configured level is Warn")
	}
	if !h.Enabled(t.Context(), slog.LevelError) {
		t.Error("Enabled(Error) = false, want true when configured level is Warn")
	}
}

func TestNewJSONHandler_DefaultLevelIsInfo(t *testing.T) {
	var buf bytes.Buffer
	h := NewJSONHandler(&buf, HandlerOptions{})

	if h.Enabled(t.Context(), slog.LevelDebug) {
		t.Error("Enabled(Debug) = true, want false with zero-value HandlerOptions (default level Info)")
	}
	if !h.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("Enabled(Info) = false, want true with zero-value HandlerOptions (default level Info)")
	}
}

func TestNewJSONHandler_ExtraDenyKeys(t *testing.T) {
	var buf bytes.Buffer
	h := NewJSONHandler(&buf, HandlerOptions{
		Redact: RedactOptions{ExtraDenyKeys: []string{"access_token"}},
	})

	rec := fixedRecord(slog.LevelInfo, "issued token", slog.String("access_token", "abc.def.ghi"))
	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := `{"time":"2026-08-27T12:30:45Z","level":"INFO","msg":"issued token","access_token":"[REDACTED]"}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("JSON output mismatch:\n got:  %q\n want: %q", got, want)
	}
}
