package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEmbedTextsPreservesWorkflowEndpointRule(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float64{1, 0}}}})
	}))
	defer server.Close()

	vectors, err := embedTexts(t.Context(), AIConfig{
		Endpoint: server.URL + "/custom",
		Model:    "test-model",
		Timeout:  time.Second,
	}, []string{"workflow"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/custom/v1/embeddings" {
		t.Fatalf("workflow embedding path=%q", path)
	}
	if len(vectors) != 1 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
}

func TestEmbedTextsPreservesWorkflowStatusErrorText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream detail should stay hidden", http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := embedTexts(t.Context(), AIConfig{
		Endpoint: server.URL,
		Model:    "test-model",
		Timeout:  time.Second,
	}, []string{"workflow"})
	if err == nil || err.Error() != "embedding endpoint returned 429 Too Many Requests" {
		t.Fatalf("workflow embedding error=%v", err)
	}
}

func TestReindexRejectsDimensionMismatch(t *testing.T) {
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"index": 0, "embedding": []float64{1, 0}},
			{"index": 1, "embedding": []float64{0, 1, 0}},
		}})
	}))
	defer embedding.Close()

	registry := NewRegistry(t.TempDir())
	ai := AIConfig{Enabled: true, Endpoint: embedding.URL, Model: "test-model", Timeout: time.Second}
	for _, id := range []string{"development.first", "development.second"} {
		template := testTemplate(id, "1.0.0")
		template.Status = StatusActive
		template.Hash = templateHash(template)
		if err := writeTemplateJSON(registry.templatePath("published", id, template.Version), template); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := registry.ReindexVectors(context.Background(), ai); err == nil || !strings.Contains(err.Error(), "dimension mismatch") {
		t.Fatalf("dimension mismatch was not rejected: %v", err)
	}
	if _, err := os.Stat(registry.vectorIndexPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid workflow vector index was written: %v", err)
	}
}

func TestVectorIndexInfoDistinguishesStaleAndInvalid(t *testing.T) {
	registry := NewRegistry(t.TempDir())
	ai := AIConfig{Enabled: true, Endpoint: "http://example.invalid", Model: "new-model"}
	stale := VectorIndex{Model: "old-model", Dimension: 1, UpdatedAt: time.Now().UTC(), Documents: map[string]VectorDocument{
		"development.demo@1.0.0": {
			ID: "development.demo", Version: "1.0.0", Hash: "sha256:test", Text: "demo", Vector: []float64{1}, UpdatedAt: time.Now().UTC(),
		},
	}}
	if err := writeTemplateJSON(registry.vectorIndexPath(), stale); err != nil {
		t.Fatal(err)
	}
	if status, count := registry.VectorIndexInfo(ai); status != VectorIndexStale || count != 0 {
		t.Fatalf("stale index status=%q count=%d", status, count)
	}

	if err := os.WriteFile(registry.vectorIndexPath(), []byte(`{"model":"new-model","dimension":2,"documents":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, count := registry.VectorIndexInfo(ai); status != VectorIndexInvalid || count != 0 {
		t.Fatalf("invalid index status=%q count=%d", status, count)
	}
}

func TestVectorScoresRejectQueryDimensionMismatch(t *testing.T) {
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 0, 0}}})
	}))
	defer embedding.Close()

	registry := NewRegistry(t.TempDir())
	ai := AIConfig{Enabled: true, Endpoint: embedding.URL, Model: "test-model", Timeout: time.Second}
	generation := templateGeneration(nil)
	index := VectorIndex{Model: "test-model", Generation: generation, Dimension: 2, UpdatedAt: time.Now().UTC(), Documents: map[string]VectorDocument{
		"development.demo@1.0.0": {
			ID: "development.demo", Version: "1.0.0", Hash: "sha256:test", Text: "demo", Vector: []float64{1, 0}, UpdatedAt: time.Now().UTC(),
		},
	}}
	if err := writeTemplateJSON(registry.vectorIndexPath(), index); err != nil {
		t.Fatal(err)
	}
	loaded, err := registry.loadVectorIndex(ai.Model, generation)
	if err != nil {
		t.Fatal(err)
	}
	if scores := vectorScores(context.Background(), ai, loaded, "demo", "DockMini", "development"); scores != nil {
		t.Fatalf("dimension-mismatched query produced scores: %#v", scores)
	}
}

// workflow embedding 请求必须携带运行时设置里保存的 API Key。
func TestEmbedTextsSendsRuntimeAPIKey(t *testing.T) {
	const token = "workflow-embedding-secret"
	var authorization string
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float64{1, 0}}}})
	}))
	defer embedding.Close()

	vectors, err := embedTexts(t.Context(), AIConfig{
		Endpoint: embedding.URL,
		Model:    "test-embedding",
		APIKey:   token,
		Timeout:  time.Second,
	}, []string{"workflow text"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 2 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
	if authorization != "Bearer "+token {
		t.Fatalf("workflow embedding authorization=%q", authorization)
	}
}

func TestVectorIndexValidationRejectsInconsistentDocument(t *testing.T) {
	index := VectorIndex{Model: "test-model", Dimension: 2, Documents: map[string]VectorDocument{
		"development.demo@1.0.0": {ID: "development.demo", Version: "1.0.0", Vector: []float64{1}},
	}}
	if err := validateVectorIndex(index, "test-model"); err == nil {
		t.Fatal("inconsistent workflow vector index was accepted")
	}
}

func TestVectorIndexBecomesStaleWhenActiveTemplatesChange(t *testing.T) {
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 0}}})
	}))
	defer embedding.Close()

	registry := NewRegistry(t.TempDir())
	ai := AIConfig{Enabled: true, Endpoint: embedding.URL, Model: "test-model", Timeout: time.Second}
	if _, err := registry.Publish(testTemplate("development.demo", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ReindexVectors(t.Context(), ai); err != nil {
		t.Fatalf("initial reindex: %v", err)
	}
	if status, count := registry.VectorIndexInfo(ai); status != VectorIndexReady || count != 1 {
		t.Fatalf("initial index status=%q count=%d", status, count)
	}

	if _, err := registry.Publish(testTemplate("development.demo", "2.0.0")); err != nil {
		t.Fatal(err)
	}
	if status, count := registry.VectorIndexInfo(ai); status != VectorIndexStale || count != 0 {
		t.Fatalf("changed registry should stale index: status=%q count=%d", status, count)
	}
	snapshot, err := registry.VectorIndexSnapshot(ai)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != VectorIndexStale {
		t.Fatalf("snapshot status=%q, want stale", snapshot.Status)
	}
}

func TestReindexDoesNotPublishSnapshotFromOldRegistryGeneration(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(started) })
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 0}}})
	}))
	defer embedding.Close()

	registry := NewRegistry(t.TempDir())
	ai := AIConfig{Enabled: true, Endpoint: embedding.URL, Model: "test-model", Timeout: time.Second}
	if _, err := registry.Publish(testTemplate("development.demo", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := registry.ReindexVectors(context.Background(), ai)
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("embedding request did not start")
	}
	if _, err := registry.Publish(testTemplate("development.demo", "2.0.0")); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-errCh; !errors.Is(err, errRegistryChangedDuringReindex) {
		t.Fatalf("reindex error=%v, want registry changed", err)
	}
	if _, err := os.Stat(registry.vectorIndexPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale generation index should not be written: %v", err)
	}
}

func TestLegacyVectorIndexWithoutGenerationIsStale(t *testing.T) {
	registry := NewRegistry(t.TempDir())
	ai := AIConfig{Enabled: true, Endpoint: "http://example.invalid", Model: "test-model"}
	legacy := VectorIndex{Model: ai.Model, Dimension: 1, UpdatedAt: time.Now().UTC(), Documents: map[string]VectorDocument{
		"development.demo@1.0.0": {ID: "development.demo", Version: "1.0.0", Hash: "sha256:test", Text: "demo", Vector: []float64{1}},
	}}
	if err := writeTemplateJSON(registry.vectorIndexPath(), legacy); err != nil {
		t.Fatal(err)
	}
	if status, count := registry.VectorIndexInfo(ai); status != VectorIndexStale || count != 0 {
		t.Fatalf("legacy index status=%q count=%d", status, count)
	}
}
