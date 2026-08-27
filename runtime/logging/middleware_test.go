package logging

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(NewJSONHandler(buf, HandlerOptions{}))
}

func TestMiddleware_UntrustedByDefault_GeneratesFreshIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	mw := Middleware(logger, MiddlewareOptions{})

	var gotRequestID, gotCorrelationID string
	var loggerFound bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID, _ = RequestID(r.Context())
		gotCorrelationID, _ = CorrelationID(r.Context())
		_, loggerFound = r.Context().Value(loggerContextKey).(*slog.Logger)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DefaultRequestIDHeader, "client-supplied-id")
	req.Header.Set(DefaultCorrelationIDHeader, "client-supplied-correlation")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotRequestID == "client-supplied-id" {
		t.Error("TrustInbound is false but the client-supplied request ID was used")
	}
	if gotCorrelationID == "client-supplied-correlation" {
		t.Error("TrustInbound is false but the client-supplied correlation ID was used")
	}
	if !ValidID(gotRequestID) || !ValidID(gotCorrelationID) {
		t.Errorf("generated IDs are not ValidID: request=%q correlation=%q", gotRequestID, gotCorrelationID)
	}
	if !loggerFound {
		t.Error("request-scoped logger was not stored in the context")
	}
	if got := rec.Header().Get(DefaultRequestIDHeader); got != gotRequestID {
		t.Errorf("response %s header = %q, want %q", DefaultRequestIDHeader, got, gotRequestID)
	}
	if got := rec.Header().Get(DefaultCorrelationIDHeader); got != gotCorrelationID {
		t.Errorf("response %s header = %q, want %q", DefaultCorrelationIDHeader, got, gotCorrelationID)
	}
}

func TestMiddleware_TrustInbound_AcceptsValidID(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	mw := Middleware(logger, MiddlewareOptions{TrustInbound: true})

	var gotRequestID, gotCorrelationID string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID, _ = RequestID(r.Context())
		gotCorrelationID, _ = CorrelationID(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DefaultRequestIDHeader, "upstream-req-123")
	req.Header.Set(DefaultCorrelationIDHeader, "upstream-corr-456")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotRequestID != "upstream-req-123" {
		t.Errorf("request ID = %q, want the trusted inbound value %q", gotRequestID, "upstream-req-123")
	}
	if gotCorrelationID != "upstream-corr-456" {
		t.Errorf("correlation ID = %q, want the trusted inbound value %q", gotCorrelationID, "upstream-corr-456")
	}
}

// TestMiddleware_TrustInbound_RejectsHostileValues is the end-to-end version
// of the ValidID hostile-input table: even with TrustInbound set, each of
// these inbound header values must be rejected and replaced with a freshly
// generated ID rather than reaching the context, the logger, or the response
// header.
func TestMiddleware_TrustInbound_RejectsHostileValues(t *testing.T) {
	hostile := map[string]string{
		"empty":             "",
		"newline injection": "abc\nFAKE-LOG-LINE: admin logged in",
		"ansi escape":       "abc\x1b[31mRED\x1b[0m",
		"10KB value":        strings.Repeat("a", 10*1024),
		"non-ascii":         "café-🎉",
		"embedded CR":       "abc\r\nSet-Cookie: session=stolen",
		"over max length":   strings.Repeat("a", MaxIDLength+1),
	}

	for name, value := range hostile {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := newTestLogger(&buf)
			mw := Middleware(logger, MiddlewareOptions{TrustInbound: true})

			var gotRequestID string
			handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotRequestID, _ = RequestID(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if value != "" {
				req.Header.Set(DefaultRequestIDHeader, value)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if gotRequestID == value {
				t.Errorf("hostile inbound value %q reached the context unchanged", value)
			}
			if !ValidID(gotRequestID) {
				t.Errorf("resolved request ID %q is not ValidID", gotRequestID)
			}
			headerValue := rec.Header().Get(DefaultRequestIDHeader)
			if strings.Contains(headerValue, "\n") || strings.Contains(headerValue, "\x1b") {
				t.Errorf("response header %q still carries hostile bytes", headerValue)
			}
			if headerValue != gotRequestID {
				t.Errorf("response header = %q, want the resolved ID %q", headerValue, gotRequestID)
			}
		})
	}
}

func TestMiddleware_CustomHeaderNames(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	mw := Middleware(logger, MiddlewareOptions{
		TrustInbound:        true,
		RequestIDHeader:     "X-My-Request",
		CorrelationIDHeader: "X-My-Correlation",
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-My-Request", "custom-req-1")
	req.Header.Set("X-My-Correlation", "custom-corr-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-My-Request"); got != "custom-req-1" {
		t.Errorf("X-My-Request = %q, want %q", got, "custom-req-1")
	}
	if got := rec.Header().Get("X-My-Correlation"); got != "custom-corr-1" {
		t.Errorf("X-My-Correlation = %q, want %q", got, "custom-corr-1")
	}
}

func TestMiddleware_RequestScopedLoggerCarriesIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	mw := Middleware(logger, MiddlewareOptions{TrustInbound: true})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		FromContext(r.Context()).Info("handled")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DefaultRequestIDHeader, "req-marker-1")
	req.Header.Set(DefaultCorrelationIDHeader, "corr-marker-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	out := buf.String()
	if !strings.Contains(out, `"request_id":"req-marker-1"`) {
		t.Errorf("log output missing request_id attribute: %s", out)
	}
	if !strings.Contains(out, `"correlation_id":"corr-marker-1"`) {
		t.Errorf("log output missing correlation_id attribute: %s", out)
	}
}
