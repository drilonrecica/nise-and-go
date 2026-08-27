package observability

import "context"

// Tracer starts spans of traced work. It names exactly the four operations
// the blueprint's optional tracing seam requires (blueprint §15: "Optional
// tracing through a narrow interface; no mandatory collector") — start a
// span, end it, record an error on it, and propagate it through a
// [context.Context] — and nothing else. This package imports no tracing
// SDK, so this interface names none in its signature either.
//
// [NoopTracer] is the zero-cost default every generated application gets
// unless it explicitly wires something else. To use a real tracer (for
// example OpenTelemetry), an application implements Tracer itself in its
// own package — importing whatever SDK it chooses there, not here — and
// passes that implementation to whatever application code accepts a
// Tracer. See docs/metrics.md for a worked example.
type Tracer interface {
	// Start begins a span named name as a child of any span already
	// carried by ctx, and returns a context carrying the new span
	// alongside the span itself. Call End on the returned Span exactly
	// once, typically via defer immediately after Start.
	Start(ctx context.Context, name string) (context.Context, Span)
}

// Span is a single unit of traced work returned by [Tracer.Start].
type Span interface {
	// End marks the span finished. Call it exactly once.
	End()
	// RecordError attaches err to the span. A nil err is a no-op.
	RecordError(err error)
}

// NoopTracer is the zero-cost default [Tracer]: [NoopTracer.Start] and the
// [Span] it returns do nothing, and neither allocates. Its zero value is
// ready to use. See trace_test.go for the [testing.AllocsPerRun]
// measurement proving the unused path allocates nothing.
type NoopTracer struct{}

var _ Tracer = NoopTracer{}

// Start returns ctx unchanged alongside a [Span] whose End and RecordError
// do nothing.
func (NoopTracer) Start(ctx context.Context, name string) (context.Context, Span) {
	return ctx, noopSpan{}
}

// noopSpan is the empty-struct [Span] [NoopTracer.Start] returns. A
// zero-size value converted to an interface does not heap-allocate in Go,
// which is what makes NoopTracer genuinely free rather than merely cheap.
type noopSpan struct{}

var _ Span = noopSpan{}

func (noopSpan) End()              {}
func (noopSpan) RecordError(error) {}
