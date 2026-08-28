package logging_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/logging"
)

// TestMiddlewareRejectsNilLogger covers a zero-value defect this fix wave
// closed. A nil *slog.Logger used to be accepted and then dereferenced on
// the first request served, so a wiring mistake surfaced as a bare
// nil-pointer panic inside the middleware whose job is to make failures
// legible. It now fails at construction, and does not silently substitute
// slog.Default(), which would leave the process running and logging
// somewhere nobody configured.
func TestMiddlewareRejectsNilLogger(t *testing.T) {
	defer func() {
		p := recover()
		if p == nil {
			t.Fatal("expected a panic for a nil *slog.Logger")
		}
		msg, ok := p.(string)
		if !ok {
			t.Fatalf("panic value is %T, want a diagnostic string: %v", p, p)
		}
		for _, want := range []string{"runtime/logging", "Middleware", "slog.New"} {
			if !strings.Contains(msg, want) {
				t.Errorf("panic message %q does not mention %q", msg, want)
			}
		}
	}()
	logging.Middleware(nil, logging.MiddlewareOptions{})
}
