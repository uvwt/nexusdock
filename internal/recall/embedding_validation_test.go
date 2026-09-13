package recall

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseEmbeddingResponseRespectsOpenAIIndexes(t *testing.T) {
	vectors, err := parseEmbeddingResponse([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("indexed vectors were not restored to request order: %#v", vectors)
	}
}

func TestParseEmbeddingResponseRejectsInvalidIndexes(t *testing.T) {
	tests := map[string]string{
		"mixed":     `{"data":[{"index":0,"embedding":[1,0]},{"embedding":[0,1]}]}`,
		"duplicate": `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`,
		"fraction":  `{"data":[{"index":0.5,"embedding":[1,0]}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseEmbeddingResponse([]byte(body)); err == nil {
				t.Fatal("invalid indexed embedding response was accepted")
			}
		})
	}
}

func TestEmbeddingReindexRejectsDimensionMismatch(t *testing.T) {
	store := newTestStore(t)
	for _, title := range []string{"First card", "Second card"} {
		if _, err := store.WriteCard(CardRequest{
			Title: title, Content: "Reusable content for vector validation.", Type: CardRunbook,
			Scope: ScopeProject, Project: "agentdock", Status: StatusInbox, Confirmed: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"index": 0, "embedding": []float64{1, 0}},
			{"index": 1, "embedding": []float64{0, 1, 0}},
		}})
	}))
	defer server.Close()

	indexPath := filepath.Join(store.Root(), ".recall", "embedding-index.json")
	svc := NewEmbeddingService(store, EmbeddingConfig{Enabled: true, Endpoint: server.URL})
	if _, err := svc.Reindex(context.Background(), EmbeddingReindexRequest{Prefix: "recall/managed/cards"}); err == nil || !strings.Contains(err.Error(), "dimension mismatch") {
		t.Fatalf("dimension mismatch was not rejected: %v", err)
	}
	if _, err := os.Stat(indexPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid index was written: %v", err)
	}
}

func TestEmbeddingSearchRejectsStaleModelBeforeQuery(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	svc := NewEmbeddingService(newTestStore(t), EmbeddingConfig{
		Enabled: true, Endpoint: server.URL, Model: "new-model",
	})
	index := embeddingIndex{Model: "old-model", Dimension: 2, UpdatedAt: time.Now().UTC(), Documents: map[string]embeddingDocument{
		"recall/managed/cards/demo.md": {
			Path: "recall/managed/cards/demo.md", Text: "demo", Vector: []float64{1, 0}, UpdatedAt: time.Now().UTC(),
		},
	}}
	if err := svc.writeIndex(index); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Search(context.Background(), EmbeddingSearchRequest{Query: "demo"}); err == nil || !strings.Contains(err.Error(), "does not match configured model") {
		t.Fatalf("stale model was not rejected: %v", err)
	}
	if requests != 0 {
		t.Fatalf("stale index triggered %d unnecessary embedding requests", requests)
	}
}

func TestEmbeddingSearchRejectsQueryDimensionMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 0, 0}}})
	}))
	defer server.Close()

	svc := NewEmbeddingService(newTestStore(t), EmbeddingConfig{
		Enabled: true, Endpoint: server.URL, Model: "test-model",
	})
	index := embeddingIndex{Model: "test-model", Dimension: 2, UpdatedAt: time.Now().UTC(), Documents: map[string]embeddingDocument{
		"recall/managed/cards/demo.md": {
			Path: "recall/managed/cards/demo.md", Text: "demo", Vector: []float64{1, 0}, UpdatedAt: time.Now().UTC(),
		},
	}}
	if err := svc.writeIndex(index); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Search(context.Background(), EmbeddingSearchRequest{Query: "demo"}); err == nil || !strings.Contains(err.Error(), "query dimension") {
		t.Fatalf("query dimension mismatch was not rejected: %v", err)
	}
}

func TestEmbeddingIndexValidationRejectsInconsistentDocuments(t *testing.T) {
	index := embeddingIndex{Model: "test-model", Dimension: 2, Documents: map[string]embeddingDocument{
		"a.md": {Path: "different.md", Vector: []float64{1}},
	}}
	if err := validateEmbeddingIndex(index); err == nil {
		t.Fatal("inconsistent embedding index was accepted")
	}
}

func TestCosineRejectsDimensionMismatch(t *testing.T) {
	if score := cosine([]float64{1, 0}, []float64{1}); score != 0 {
		t.Fatalf("mismatched vectors produced score %f", score)
	}
}
