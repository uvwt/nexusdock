package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTracingCreatesRootAndContinuesRemoteParent(t *testing.T) {
	tracing := NewTracing()
	t.Cleanup(func() { _ = tracing.Shutdown(context.Background()) })

	rootCtx, rootSpan := tracing.StartAgentDockInvoke(context.Background(), "read_file")
	rootTraceID, rootSpanID := TraceIdentifiers(rootCtx)
	rootSpan.End()
	if len(rootTraceID) != 32 || len(rootSpanID) != 16 {
		t.Fatalf("root identifiers = %q / %q", rootTraceID, rootSpanID)
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
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), parent)
	childCtx, childSpan := tracing.StartAgentDockInvoke(ctx, "read_file")
	child := trace.SpanContextFromContext(childCtx)
	childSpan.End()
	if child.TraceID() != parent.TraceID() || child.SpanID() == parent.SpanID() {
		t.Fatalf("child ids = %s/%s parent=%s/%s", child.TraceID(), child.SpanID(), parent.TraceID(), parent.SpanID())
	}
	if !child.IsSampled() || child.TraceState().String() != parent.TraceState().String() {
		t.Fatalf("child sampling/state = sampled:%v state:%q", child.IsSampled(), child.TraceState())
	}
}

func TestTracingPreservesUnsampledRemoteParent(t *testing.T) {
	tracing := NewTracing()
	t.Cleanup(func() { _ = tracing.Shutdown(context.Background()) })
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	parent := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, Remote: true})
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), parent)
	childCtx, childSpan := tracing.StartAgentDockInvoke(ctx, "read_file")
	defer childSpan.End()
	child := trace.SpanContextFromContext(childCtx)
	if child.TraceID() != parent.TraceID() || child.IsSampled() {
		t.Fatalf("unsampled parent was not preserved: %#v", child)
	}
}
