package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/uvwt/nexusdock"

// Tracing owns NexusDock's process-local OpenTelemetry provider.
// No exporter is configured in this phase; the SDK provides standard IDs,
// sampling semantics, and parent-child relationships for local correlation.
type Tracing struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
}

func NewTracing() *Tracing {
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)
	return &Tracing{
		provider: provider,
		tracer:   provider.Tracer(instrumentationName),
	}
}

func (t *Tracing) StartAgentDockInvoke(ctx context.Context, tool string) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil || t.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return t.tracer.Start(
		ctx,
		"nexusdock.agentdock.invoke",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("agentdock.tool.name", tool)),
	)
}

func (t *Tracing) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return t.provider.Shutdown(ctx)
}

func TraceIdentifiers(ctx context.Context) (string, string) {
	if ctx == nil {
		return "", ""
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return "", ""
	}
	return spanContext.TraceID().String(), spanContext.SpanID().String()
}
