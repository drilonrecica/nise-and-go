package logging

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

func TestRequestID_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := RequestID(ctx); ok {
		t.Fatal("RequestID on a bare context reported ok=true")
	}

	ctx = WithRequestID(ctx, "req-abc123")
	got, ok := RequestID(ctx)
	if !ok || got != "req-abc123" {
		t.Fatalf("RequestID() = (%q, %v), want (%q, true)", got, ok, "req-abc123")
	}
}

func TestCorrelationID_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := CorrelationID(ctx); ok {
		t.Fatal("CorrelationID on a bare context reported ok=true")
	}

	ctx = WithCorrelationID(ctx, "corr-xyz789")
	got, ok := CorrelationID(ctx)
	if !ok || got != "corr-xyz789" {
		t.Fatalf("CorrelationID() = (%q, %v), want (%q, true)", got, ok, "corr-xyz789")
	}
}

func TestRequestIDAndCorrelationID_AreIndependent(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-1")
	ctx = WithCorrelationID(ctx, "corr-1")

	reqID, _ := RequestID(ctx)
	corrID, _ := CorrelationID(ctx)
	if reqID != "req-1" {
		t.Errorf("RequestID() = %q, want %q", reqID, "req-1")
	}
	if corrID != "corr-1" {
		t.Errorf("CorrelationID() = %q, want %q", corrID, "corr-1")
	}
}

func TestFromContext_ReturnsStoredLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, HandlerOptions{}))
	ctx := WithLogger(context.Background(), logger)

	got := FromContext(ctx)
	got.Info("marker")

	if buf.Len() == 0 {
		t.Fatal("FromContext did not return the logger stored by WithLogger")
	}
}

func TestFromContext_FallsBackToSlogDefaultWhenAbsent(t *testing.T) {
	got := FromContext(context.Background())
	if got == nil {
		t.Fatal("FromContext(context.Background()) = nil, want slog.Default()")
	}
}
