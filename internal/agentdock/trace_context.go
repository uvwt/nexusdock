package agentdock

import (
	"context"
	"strings"

	protocol "github.com/uvwt/agentdock-protocol"
	"go.opentelemetry.io/otel/propagation"
)

var bridgeTraceContext = propagation.TraceContext{}

type messageTraceCarrier struct {
	message *protocol.Message
}

func (c messageTraceCarrier) Get(key string) string {
	if c.message == nil {
		return ""
	}
	switch strings.ToLower(key) {
	case "traceparent":
		return c.message.Traceparent
	case "tracestate":
		return c.message.Tracestate
	default:
		return ""
	}
}

func (c messageTraceCarrier) Set(key, value string) {
	if c.message == nil {
		return
	}
	switch strings.ToLower(key) {
	case "traceparent":
		c.message.Traceparent = value
	case "tracestate":
		c.message.Tracestate = value
	}
}

func (c messageTraceCarrier) Keys() []string {
	return []string{"traceparent", "tracestate"}
}

func injectBridgeTraceContext(ctx context.Context, message *protocol.Message) {
	if ctx == nil || message == nil {
		return
	}
	bridgeTraceContext.Inject(ctx, messageTraceCarrier{message: message})
}
