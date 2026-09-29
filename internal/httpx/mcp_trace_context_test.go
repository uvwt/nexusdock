package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestWithMCPTraceContextExtractsRemoteParent(t *testing.T) {
	var got trace.SpanContext
	handler := withMCPTraceContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	req.Header.Set("tracestate", "rojo=00f067aa0ba902b7")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !got.IsValid() || !got.IsRemote() || !got.IsSampled() {
		t.Fatalf("span context = %#v", got)
	}
	if got.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || got.SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("ids = %s / %s", got.TraceID(), got.SpanID())
	}
	if got.TraceState().String() != "rojo=00f067aa0ba902b7" {
		t.Fatalf("tracestate = %q", got.TraceState())
	}
}

func TestWithMCPTraceContextIgnoresInvalidHeader(t *testing.T) {
	var got trace.SpanContext
	handler := withMCPTraceContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = trace.SpanContextFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("traceparent", "invalid")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if got.IsValid() {
		t.Fatalf("invalid header produced span context: %#v", got)
	}
}
