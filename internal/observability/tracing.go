package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const instrumentationName = "github.com/uvwt/nexusdock"

// Tracing 只持有 OpenTelemetry API 层的 Tracer。
// 默认使用 no-op 实现，不创建 SDK Provider，也不拥有采样、导出或 Shutdown 生命周期。
type Tracing struct {
	tracer trace.Tracer
}

func NewTracing(tracer trace.Tracer) *Tracing {
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer(instrumentationName)
	}
	return &Tracing{tracer: tracer}
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

// TraceIdentifiers 返回当前 TraceID 与“本次调用实际创建的”SpanID。
// 默认 no-op tracer 会原样保留上游 SpanContext；这种情况下只关联 TraceID，
// 不把上游 SpanID 冒充成本地 client span。
func TraceIdentifiers(parentCtx, activeCtx context.Context) (string, string) {
	if activeCtx == nil {
		return "", ""
	}
	active := trace.SpanContextFromContext(activeCtx)
	if !active.IsValid() {
		return "", ""
	}

	traceID := active.TraceID().String()
	if parentCtx == nil {
		return traceID, active.SpanID().String()
	}
	parent := trace.SpanContextFromContext(parentCtx)
	if parent.IsValid() &&
		active.TraceID() == parent.TraceID() &&
		active.SpanID() == parent.SpanID() {
		return traceID, ""
	}
	return traceID, active.SpanID().String()
}
