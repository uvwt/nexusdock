package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTracingDefaultsToNoopAndPreservesRemoteParent(t *testing.T) {
	tracing := NewTracing(nil)

	rootCtx, rootSpan := tracing.StartAgentDockInvoke(context.Background(), "read_file")
	rootTraceID, rootSpanID := TraceIdentifiers(context.Background(), rootCtx)
	rootSpan.End()
	if rootTraceID != "" || rootSpanID != "" {
		t.Fatalf("default no-op root identifiers = %q / %q", rootTraceID, rootSpanID)
	}

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	state, err := trace.ParseTraceState("rojo=00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled, TraceState: state, Remote: true,
	})
	parentCtx := trace.ContextWithRemoteSpanContext(context.Background(), parent)
	activeCtx, span := tracing.StartAgentDockInvoke(parentCtx, "read_file")
	defer span.End()

	active := trace.SpanContextFromContext(activeCtx)
	if !active.Equal(parent) {
		t.Fatalf("default no-op span context = %#v, want remote parent %#v", active, parent)
	}
	gotTraceID, gotSpanID := TraceIdentifiers(parentCtx, activeCtx)
	if gotTraceID != traceID.String() || gotSpanID != "" {
		t.Fatalf("correlation identifiers = %q / %q", gotTraceID, gotSpanID)
	}
}

func TestTracingPreservesUnsampledRemoteParent(t *testing.T) {
	tracing := NewTracing(nil)
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	parent := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, Remote: true})
	parentCtx := trace.ContextWithRemoteSpanContext(context.Background(), parent)
	activeCtx, span := tracing.StartAgentDockInvoke(parentCtx, "read_file")
	defer span.End()

	active := trace.SpanContextFromContext(activeCtx)
	if !active.Equal(parent) || active.IsSampled() {
		t.Fatalf("unsampled parent was not preserved: %#v", active)
	}
	gotTraceID, gotSpanID := TraceIdentifiers(parentCtx, activeCtx)
	if gotTraceID != traceID.String() || gotSpanID != "" {
		t.Fatalf("unsampled correlation identifiers = %q / %q", gotTraceID, gotSpanID)
	}
}
