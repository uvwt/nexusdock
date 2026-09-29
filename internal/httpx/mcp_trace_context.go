package httpx

import (
	"net/http"

	"go.opentelemetry.io/otel/propagation"
)

var mcpTraceContext = propagation.TraceContext{}

// withMCPTraceContext only extracts W3C Trace Context on the public MCP transport.
// Management and browser routes keep their existing request model unchanged.
func withMCPTraceContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := mcpTraceContext.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
