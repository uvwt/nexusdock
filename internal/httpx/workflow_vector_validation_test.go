package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/settings"
)

func TestParseWorkflowEmbeddingResponseRespectsOpenAIIndexes(t *testing.T) {
	vectors, err := parseWorkflowEmbeddingResponse([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("indexed vectors were not restored to request order: %#v", vectors)
	}
}

func TestParseWorkflowEmbeddingResponseRejectsInvalidIndexes(t *testing.T) {
	tests := map[string]string{
		"mixed":     `{"data":[{"index":0,"embedding":[1,0]},{"embedding":[0,1]}]}`,
		"duplicate": `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`,
		"fraction":  `{"data":[{"index":0.5,"embedding":[1,0]}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseWorkflowEmbeddingResponse([]byte(body)); err == nil {
				t.Fatal("invalid indexed embedding response was accepted")
			}
		})
	}
}

func TestWorkflowTemplateReindexRejectsDimensionMismatch(t *testing.T) {
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"index": 0, "embedding": []float64{1, 0}},
			{"index": 1, "embedding": []float64{0, 1, 0}},
		}})
	}))
	defer embedding.Close()

	server := &Server{
		cfg:   config.Config{NexusDataDir: t.TempDir()},
		aiCfg: settings.RuntimeAIConfig{EmbeddingEnabled: true, EmbeddingEndpoint: embedding.URL, EmbeddingModel: "test-model", EmbeddingTimeout: time.Second},
	}
	if err := server.ensureWorkflowRegistryDirs(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"development.first", "development.second"} {
		template := testWorkflowTemplate(id, "1.0.0")
		template.Status = workflowTemplateActive
		template.Hash = workflowTemplateHash(template)
		if err := writeWorkflowTemplateJSON(server.workflowTemplatePath("published", id, template.Version), template); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := server.reindexWorkflowTemplateVectors(context.Background()); err == nil || !strings.Contains(err.Error(), "dimension mismatch") {
		t.Fatalf("dimension mismatch was not rejected: %v", err)
	}
	if _, err := os.Stat(server.workflowTemplateVectorIndexPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid workflow vector index was written: %v", err)
	}
}

func TestWorkflowVectorIndexInfoDistinguishesStaleAndInvalid(t *testing.T) {
	server := &Server{
		cfg:   config.Config{NexusDataDir: t.TempDir()},
		aiCfg: settings.RuntimeAIConfig{EmbeddingEnabled: true, EmbeddingEndpoint: "http://example.invalid", EmbeddingModel: "new-model"},
	}
	if err := server.ensureWorkflowRegistryDirs(); err != nil {
		t.Fatal(err)
	}
	stale := workflowTemplateVectorIndex{Model: "old-model", Dimension: 1, UpdatedAt: time.Now().UTC(), Documents: map[string]workflowTemplateVector{
		"development.demo@1.0.0": {
			ID: "development.demo", Version: "1.0.0", Hash: "sha256:test", Text: "demo", Vector: []float64{1}, UpdatedAt: time.Now().UTC(),
		},
	}}
	if err := writeWorkflowTemplateJSON(server.workflowTemplateVectorIndexPath(), stale); err != nil {
		t.Fatal(err)
	}
	if status, count := server.workflowTemplateVectorIndexInfo(); status != "stale" || count != 0 {
		t.Fatalf("stale index status=%q count=%d", status, count)
	}

	if err := os.WriteFile(server.workflowTemplateVectorIndexPath(), []byte(`{"model":"new-model","dimension":2,"documents":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, count := server.workflowTemplateVectorIndexInfo(); status != "invalid" || count != 0 {
		t.Fatalf("invalid index status=%q count=%d", status, count)
	}
}

func TestWorkflowVectorScoresRejectQueryDimensionMismatch(t *testing.T) {
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 0, 0}}})
	}))
	defer embedding.Close()

	server := &Server{
		cfg:   config.Config{NexusDataDir: t.TempDir()},
		aiCfg: settings.RuntimeAIConfig{EmbeddingEnabled: true, EmbeddingEndpoint: embedding.URL, EmbeddingModel: "test-model", EmbeddingTimeout: time.Second},
	}
	if err := server.ensureWorkflowRegistryDirs(); err != nil {
		t.Fatal(err)
	}
	index := workflowTemplateVectorIndex{Model: "test-model", Dimension: 2, UpdatedAt: time.Now().UTC(), Documents: map[string]workflowTemplateVector{
		"development.demo@1.0.0": {
			ID: "development.demo", Version: "1.0.0", Hash: "sha256:test", Text: "demo", Vector: []float64{1, 0}, UpdatedAt: time.Now().UTC(),
		},
	}}
	if err := writeWorkflowTemplateJSON(server.workflowTemplateVectorIndexPath(), index); err != nil {
		t.Fatal(err)
	}
	if scores := server.workflowTemplateVectorScores(context.Background(), "demo", "DockMini", "development"); scores != nil {
		t.Fatalf("dimension-mismatched query produced scores: %#v", scores)
	}
}

func TestWorkflowVectorIndexValidationRejectsInconsistentDocument(t *testing.T) {
	index := workflowTemplateVectorIndex{Model: "test-model", Dimension: 2, Documents: map[string]workflowTemplateVector{
		"development.demo@1.0.0": {ID: "development.demo", Version: "1.0.0", Vector: []float64{1}},
	}}
	if err := validateWorkflowTemplateVectorIndex(index, "test-model"); err == nil {
		t.Fatal("inconsistent workflow vector index was accepted")
	}
}
