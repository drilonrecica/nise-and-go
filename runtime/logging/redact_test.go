package logging

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// captureHandler is a minimal slog.Handler that flattens every attribute it
// receives (following WithAttrs/WithGroup/group nesting, and resolving any
// slog.LogValuer) into a map keyed by dot-joined path, recording the value
// that ultimately reached it. Tests use it to assert on exactly what a
// handler downstream of NewRedactingHandler would see, independent of the
// JSON or text rendering this package also happens to ship.
type captureHandler struct {
	prefix string
	attrs  []slog.Attr
	got    map[string]string
}

func newCaptureHandler() *captureHandler {
	return &captureHandler{got: map[string]string{}}
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	for k, v := range flattenAttrs(h.prefix, h.attrs) {
		h.got[k] = v
	}
	var recAttrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		recAttrs = append(recAttrs, a)
		return true
	})
	for k, v := range flattenAttrs(h.prefix, recAttrs) {
		h.got[k] = v
	}
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &captureHandler{prefix: h.prefix, attrs: merged, got: h.got}
}

func (h *captureHandler) WithGroup(name string) slog.Handler {
	return &captureHandler{prefix: h.prefix + name + ".", attrs: h.attrs, got: h.got}
}

func flattenAttrs(prefix string, attrs []slog.Attr) map[string]string {
	out := map[string]string{}
	for _, a := range attrs {
		v := a.Value.Resolve()
		key := prefix + a.Key
		if v.Kind() == slog.KindGroup {
			for k, val := range flattenAttrs(key+".", v.Group()) {
				out[k] = val
			}
			continue
		}
		out[key] = v.String()
	}
	return out
}

func TestNewRedactingHandler_DefaultDenyKeys(t *testing.T) {
	for _, key := range DefaultDenyKeys() {
		t.Run(key, func(t *testing.T) {
			capture := newCaptureHandler()
			h := NewRedactingHandler(capture, RedactOptions{})
			logger := slog.New(h)
			logger.Info("event", key, "super-secret-value")

			if got := capture.got[key]; got != RedactPlaceholder {
				t.Errorf("got %s=%q, want %q", key, got, RedactPlaceholder)
			}
		})
	}
}

func TestNewRedactingHandler_CaseInsensitive(t *testing.T) {
	capture := newCaptureHandler()
	h := NewRedactingHandler(capture, RedactOptions{})
	logger := slog.New(h)
	logger.Info("event", "PaSsWoRd", "hunter2")

	if got := capture.got["PaSsWoRd"]; got != RedactPlaceholder {
		t.Errorf("got %q, want %q", got, RedactPlaceholder)
	}
}

func TestNewRedactingHandler_NonDenyListedKeyPasses(t *testing.T) {
	capture := newCaptureHandler()
	h := NewRedactingHandler(capture, RedactOptions{})
	logger := slog.New(h)
	logger.Info("event", "user_id", "u_123")

	if got := capture.got["user_id"]; got != "u_123" {
		t.Errorf("got %q, want %q (non-deny-listed key must pass through unredacted)", got, "u_123")
	}
}

func TestNewRedactingHandler_ExtraDenyKeys(t *testing.T) {
	capture := newCaptureHandler()
	h := NewRedactingHandler(capture, RedactOptions{ExtraDenyKeys: []string{"access_token"}})
	logger := slog.New(h)
	logger.Info("event", "access_token", "abc.def.ghi")

	if got := capture.got["access_token"]; got != RedactPlaceholder {
		t.Errorf("got %q, want %q", got, RedactPlaceholder)
	}
}

func TestNewRedactingHandler_CustomPlaceholder(t *testing.T) {
	capture := newCaptureHandler()
	h := NewRedactingHandler(capture, RedactOptions{Placeholder: "***"})
	logger := slog.New(h)
	logger.Info("event", "password", "hunter2")

	if got := capture.got["password"]; got != "***" {
		t.Errorf("got %q, want %q", got, "***")
	}
}

// TestNewRedactingHandler_NestedThreeGroupsDeep is named directly for the
// brief's required case: a deny-set key nested three groups deep, reached
// through repeated slog.Logger.WithGroup calls, must still be redacted.
func TestNewRedactingHandler_NestedThreeGroupsDeep(t *testing.T) {
	capture := newCaptureHandler()
	h := NewRedactingHandler(capture, RedactOptions{})
	logger := slog.New(h).WithGroup("a").WithGroup("b").WithGroup("c")
	logger.Info("event", "password", "hunter2", "safe", "value")

	if got := capture.got["a.b.c.password"]; got != RedactPlaceholder {
		t.Errorf("got a.b.c.password=%q, want %q", got, RedactPlaceholder)
	}
	if got := capture.got["a.b.c.safe"]; got != "value" {
		t.Errorf("got a.b.c.safe=%q, want %q (non-secret key at same depth must pass through)", got, "value")
	}
}

// TestNewRedactingHandler_NestedGroupValue covers a deny-set key nested
// inside a single slog.Group attribute value (rather than via WithGroup).
func TestNewRedactingHandler_NestedGroupValue(t *testing.T) {
	capture := newCaptureHandler()
	h := NewRedactingHandler(capture, RedactOptions{})
	logger := slog.New(h)
	logger.Info("event", slog.Group("db",
		slog.String("host", "db.internal"),
		slog.Group("auth", slog.String("password", "hunter2")),
	))

	if got := capture.got["db.host"]; got != "db.internal" {
		t.Errorf("got db.host=%q, want %q", got, "db.internal")
	}
	if got := capture.got["db.auth.password"]; got != RedactPlaceholder {
		t.Errorf("got db.auth.password=%q, want %q", got, RedactPlaceholder)
	}
}

// credentialValuer is a slog.LogValuer whose outer attribute key ("creds") is
// not deny-listed, but whose resolved value is a group containing a
// deny-set-shaped key ("password"). A naive top-level-only redaction
// implementation — one that only inspects the outer attribute's key — would
// let this straight through.
type credentialValuer struct {
	Username string
	Password string
}

func (c credentialValuer) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("username", c.Username),
		slog.String("password", c.Password),
	)
}

func TestNewRedactingHandler_LogValuerReturningSecretShapedKey(t *testing.T) {
	capture := newCaptureHandler()
	h := NewRedactingHandler(capture, RedactOptions{})
	logger := slog.New(h)
	logger.Info("login attempt", "creds", credentialValuer{Username: "alice", Password: "hunter2"})

	if got := capture.got["creds.username"]; got != "alice" {
		t.Errorf("got creds.username=%q, want %q", got, "alice")
	}
	if got := capture.got["creds.password"]; got != RedactPlaceholder {
		t.Errorf("got creds.password=%q, want %q (LogValuer must not bypass redaction)", got, RedactPlaceholder)
	}
}

// denyShapedValuer is a slog.LogValuer whose own attribute key IS deny-listed
// directly; this must be redacted wholesale without even needing to resolve
// the value (though resolving it is harmless).
type denyShapedValuer struct{ value string }

func (d denyShapedValuer) LogValue() slog.Value { return slog.StringValue(d.value) }

func TestNewRedactingHandler_DenyListedKeyWithLogValuerValue(t *testing.T) {
	capture := newCaptureHandler()
	h := NewRedactingHandler(capture, RedactOptions{})
	logger := slog.New(h)
	logger.Info("event", "token", denyShapedValuer{value: "abc.def.ghi"})

	if got := capture.got["token"]; got != RedactPlaceholder {
		t.Errorf("got token=%q, want %q", got, RedactPlaceholder)
	}
}

func TestRedact_PostgresURL(t *testing.T) {
	got := Redact("postgres://appuser:s3cr3t-pw@db.internal:5432/appdb?sslmode=require")
	want := "postgres://db.internal:5432/appdb?sslmode=require"
	if got != want {
		t.Errorf("Redact() = %q, want %q", got, want)
	}
}

func TestRedact_PostgresURLWithCredentialQueryParam(t *testing.T) {
	got := Redact("postgres://db.internal:5432/appdb?sslmode=require&password=oops")
	if strings.Contains(got, "oops") {
		t.Errorf("Redact() = %q, still contains the credential value", got)
	}
	if !strings.Contains(got, "sslmode=require") {
		t.Errorf("Redact() = %q, dropped a non-credential query parameter", got)
	}
}

func TestRedact_SMTPURL(t *testing.T) {
	got := Redact("smtp://mailer:hunter2@smtp.example.com:587")
	want := "smtp://smtp.example.com:587"
	if got != want {
		t.Errorf("Redact() = %q, want %q", got, want)
	}
	if strings.Contains(got, "mailer") || strings.Contains(got, "hunter2") {
		t.Errorf("Redact() = %q, userinfo leaked", got)
	}
}

func TestRedact_UnparsableInputFailsClosed(t *testing.T) {
	// A space-separated PostgreSQL DSN is not URL-shaped; Redact cannot
	// verify it holds no secret, so it must not return it verbatim.
	raw := "host=localhost user=postgres password=hunter2 dbname=app"
	got := Redact(raw)
	if strings.Contains(got, "hunter2") {
		t.Errorf("Redact() = %q, leaked the credential from an unparsable DSN", got)
	}
	if got == raw {
		t.Errorf("Redact() returned the input unchanged for an unparsable value")
	}
}

func TestDefaultDenyKeys_ReturnsIndependentCopy(t *testing.T) {
	a := DefaultDenyKeys()
	a[0] = "mutated"
	b := DefaultDenyKeys()
	if b[0] == "mutated" {
		t.Fatal("mutating the result of DefaultDenyKeys affected a later call")
	}
}
