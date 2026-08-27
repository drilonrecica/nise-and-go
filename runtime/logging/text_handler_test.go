package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewTextHandler_ExactOutput_NoColor(t *testing.T) {
	var buf bytes.Buffer
	h := NewTextHandler(&buf, TextOptions{})

	rec := fixedRecord(slog.LevelInfo, "user signed in", slog.String("user_id", "u_123"))
	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := "12:30:45.000 INFO  user signed in user_id=u_123\n"
	if got := buf.String(); got != want {
		t.Fatalf("text output mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestNewTextHandler_LevelAlignment(t *testing.T) {
	tests := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO "},
		{slog.LevelWarn, "WARN "},
		{slog.LevelError, "ERROR"},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		h := NewTextHandler(&buf, TextOptions{HandlerOptions: HandlerOptions{Level: slog.LevelDebug}})
		if err := h.Handle(t.Context(), fixedRecord(tt.level, "m")); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		got := buf.String()
		wantSegment := " " + tt.want + " m"
		if !strings.Contains(got, wantSegment) {
			t.Errorf("level %v: output %q does not contain %q", tt.level, got, wantSegment)
		}
	}
}

func TestNewTextHandler_Color(t *testing.T) {
	var buf bytes.Buffer
	h := NewTextHandler(&buf, TextOptions{Color: true})

	if err := h.Handle(t.Context(), fixedRecord(slog.LevelError, "boom")); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := "12:30:45.000 \x1b[31mERROR\x1b[0m boom\n"
	if got := buf.String(); got != want {
		t.Fatalf("colored output mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestNewTextHandler_NoColorByDefault(t *testing.T) {
	var buf bytes.Buffer
	h := NewTextHandler(&buf, TextOptions{})
	if err := h.Handle(t.Context(), fixedRecord(slog.LevelError, "boom")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("output contains ANSI escape with Color unset: %q", buf.String())
	}
}

func TestNewTextHandler_AttributeOrderIsDeterministic(t *testing.T) {
	var buf bytes.Buffer
	base := NewTextHandler(&buf, TextOptions{})
	logger := slog.New(base).With(
		slog.String("z_first", "1"),
		slog.String("a_second", "2"),
	)

	rec := fixedRecord(slog.LevelInfo, "m", slog.String("m_third", "3"), slog.String("b_fourth", "4"))
	// Emulate what slog.Logger.Handler().Handle would receive: With-derived
	// handler, then the call-site attrs.
	h := logger.Handler()
	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := "12:30:45.000 INFO  m z_first=1 a_second=2 m_third=3 b_fourth=4\n"
	if got := buf.String(); got != want {
		t.Fatalf("attribute order mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestNewTextHandler_QuotesValuesThatNeedIt(t *testing.T) {
	var buf bytes.Buffer
	h := NewTextHandler(&buf, TextOptions{})

	rec := fixedRecord(slog.LevelInfo, "m",
		slog.String("plain", "value"),
		slog.String("spaced", "has space"),
		slog.String("empty", ""),
	)
	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := `12:30:45.000 INFO  m plain=value spaced="has space" empty=""` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("quoting mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestNewTextHandler_GroupsAreDotJoined(t *testing.T) {
	var buf bytes.Buffer
	base := NewTextHandler(&buf, TextOptions{})
	logger := slog.New(base).WithGroup("http").With(slog.String("method", "GET"))

	rec := fixedRecord(slog.LevelInfo, "request", slog.Group("resp", slog.Int("status", 200)))
	if err := logger.Handler().Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := "12:30:45.000 INFO  request http.method=GET http.resp.status=200\n"
	if got := buf.String(); got != want {
		t.Fatalf("group nesting mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestNewTextHandler_RedactsDenyListedKey(t *testing.T) {
	var buf bytes.Buffer
	h := NewTextHandler(&buf, TextOptions{})

	rec := fixedRecord(slog.LevelInfo, "m", slog.String("password", "hunter2"))
	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := "12:30:45.000 INFO  m password=[REDACTED]\n"
	if got := buf.String(); got != want {
		t.Fatalf("redaction mismatch:\n got:  %q\n want: %q", got, want)
	}
}
