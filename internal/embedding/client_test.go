package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseResponseRespectsOpenAIIndexes(t *testing.T) {
	vectors, err := ParseResponse([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("indexed vectors were not restored to request order: %#v", vectors)
	}
}

func TestParseResponseSupportsEmbeddingsArray(t *testing.T) {
	vectors, err := ParseResponse([]byte(`{"embeddings":[[1,0],[0,1]]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
}

func TestParseResponseRejectsInvalidIndexes(t *testing.T) {
	tests := map[string]string{
		"mixed":     `{"data":[{"index":0,"embedding":[1,0]},{"embedding":[0,1]}]}`,
		"duplicate": `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`,
		"fraction":  `{"data":[{"index":0.5,"embedding":[1,0]}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseResponse([]byte(body)); err == nil {
				t.Fatal("invalid indexed embedding response was accepted")
			}
		})
	}
}

func TestParseResponseDoesNotAcceptSingleEmbeddingShape(t *testing.T) {
	if _, err := ParseResponse([]byte(`{"embedding":[1,0]}`)); err == nil {
		t.Fatal("shared parser unexpectedly accepted Recall-only single embedding response")
	}
}

func TestClientSendsRequestHeadersAndRestoresResponseOrder(t *testing.T) {
	const token = "secret"
	var gotAuthorization string
	var gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		var request struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" || len(request.Input) != 2 {
			t.Fatalf("unexpected request: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"index": 1, "embedding": []float64{0, 1}},
			{"index": 0, "embedding": []float64{1, 0}},
		}})
	}))
	defer server.Close()

	vectors, err := NewClient(time.Second).Embed(context.Background(), Request{
		URL: server.URL, Model: "test-model", APIKey: token, Inputs: []string{"first", "second"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "Bearer "+token {
		t.Fatalf("authorization=%q", gotAuthorization)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type=%q", gotContentType)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
}

func TestClientReturnsBoundedStatusError(t *testing.T) {
	body := strings.Repeat("界", maxStatusBodyBytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, body, http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := NewClient(time.Second).Embed(context.Background(), Request{
		URL: server.URL, Model: "test-model", Inputs: []string{"text"},
	})
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error=%T %v, want StatusError", err, err)
	}
	if statusErr.Code != http.StatusTooManyRequests || statusErr.Status != "429 Too Many Requests" {
		t.Fatalf("unexpected status error: %#v", statusErr)
	}
	if len(statusErr.Body) > maxStatusBodyBytes || !strings.HasPrefix(statusErr.Error(), "embedding endpoint returned HTTP 429: ") {
		t.Fatalf("unexpected bounded status error: %q", statusErr.Error())
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer server.Close()

	_, err := NewClient(time.Second).Embed(context.Background(), Request{
		URL: server.URL, Model: "test-model", Inputs: []string{"text"},
	})
	if err == nil || !strings.Contains(err.Error(), "embedding response exceeds") {
		t.Fatalf("oversized response error=%v", err)
	}
}
