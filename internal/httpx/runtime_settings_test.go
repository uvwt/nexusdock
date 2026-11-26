package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/settings"
)

func newRuntimeSettingsHTTPServer(t *testing.T) *Server {
	t.Helper()
	dataDir := t.TempDir()
	db, err := core.OpenSQLite(t.Context(), filepath.Join(dataDir, "nexus.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	store, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{NexusDataDir: dataDir, RecallRepoDir: store.Root()}
	runtimeSettings, err := settings.NewStore(db, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(cfg, store, slog.Default(), WithSystemDatabase(db), WithRuntimeSettings(runtimeSettings))
}

func loopbackHTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "127.0.0.1:51234"
		next.ServeHTTP(w, r)
	})
}

func TestRuntimeAISettingsAPIProtectsSecretsAndAppliesEmbeddingConfiguration(t *testing.T) {
	const embeddingToken = "embedding-settings-secret"
	var embeddingAuthorization string
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embeddingAuthorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float64{1, 0}}}})
	}))
	defer embedding.Close()

	server := newRuntimeSettingsHTTPServer(t)
	handler := loopbackHTTPHandler(server.Handler())

	body := map[string]any{
		"embedding": map[string]any{
			"enabled": true, "endpoint": embedding.URL, "model": "test-embedding", "timeout_seconds": 15,
			"api_key": map[string]any{"action": "replace", "value": embeddingToken},
		},
	}
	payload, _ := json.Marshal(body)
	update := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/settings/ai", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(update, req)
	if update.Code != http.StatusOK {
		t.Fatalf("settings update status=%d body=%s", update.Code, update.Body.String())
	}
	if strings.Contains(update.Body.String(), embeddingToken) {
		t.Fatal("settings update response leaked embedding API key")
	}

	read := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/settings/ai", nil)
	handler.ServeHTTP(read, req)
	if read.Code != http.StatusOK || strings.Contains(read.Body.String(), embeddingToken) {
		t.Fatalf("settings read response invalid or leaked secret: status=%d body=%s", read.Code, read.Body.String())
	}
	var response struct {
		Settings settings.View `json:"settings"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Settings.Embedding.Enabled || !response.Settings.Embedding.APIKeyConfigured || response.Settings.Embedding.Model != "test-embedding" {
		t.Fatalf("unexpected settings response: %#v", response.Settings)
	}

	status := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/embeddings/status", nil)
	handler.ServeHTTP(status, req)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"reachable":true`) {
		t.Fatalf("embedding status=%d body=%s", status.Code, status.Body.String())
	}
	if embeddingAuthorization != "Bearer "+embeddingToken {
		t.Fatalf("embedding authorization=%q", embeddingAuthorization)
	}
}

func TestRuntimeAIConnectionTestsUseSavedSecrets(t *testing.T) {
	const (
		embeddingToken = "embedding-test-secret"
	)
	var embeddingAuthorization string
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embeddingAuthorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float64{1, 0}}}})
	}))
	defer embedding.Close()

	server := newRuntimeSettingsHTTPServer(t)
	handler := loopbackHTTPHandler(server.Handler())
	body := map[string]any{
		"embedding": map[string]any{
			"enabled": true, "endpoint": embedding.URL, "model": "embed-test", "timeout_seconds": 5,
			"api_key": map[string]any{"action": "replace", "value": embeddingToken},
		},
	}
	payload, _ := json.Marshal(body)
	update := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/settings/ai", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(update, req)
	if update.Code != http.StatusOK {
		t.Fatalf("settings update status=%d body=%s", update.Code, update.Body.String())
	}

	response := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/settings/ai/test/embedding", nil)
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("embedding test status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), embeddingToken) {
		t.Fatalf("embedding test response leaked a secret: %s", response.Body.String())
	}
	var result runtimeAITestResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Target != "embedding" || result.Message == "" {
		t.Fatalf("unexpected embedding test result: %#v", result)
	}
	if embeddingAuthorization != "Bearer "+embeddingToken {
		t.Fatalf("embedding authorization=%q", embeddingAuthorization)
	}
}

func TestRuntimeAIOptionsPopulateDedicatedState(t *testing.T) {
	store, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.DefaultRuntimeAIConfig()
	cfg.EmbeddingEnabled = true
	cfg.EmbeddingEndpoint = "https://embedding.example.test"
	cfg.EmbeddingModel = "dedicated-state-model"
	embedding := recall.NewEmbeddingService(store, recall.EmbeddingConfig{
		Enabled: true, Endpoint: cfg.EmbeddingEndpoint, Model: cfg.EmbeddingModel,
	})

	server := NewServer(
		config.Config{},
		store,
		slog.Default(),
		WithRuntimeAIConfig(cfg),
		WithEmbeddingService(embedding),
		WithMCPAppsEnabled(false),
	)

	if got := server.runtimeAI.currentConfig(); got.EmbeddingModel != cfg.EmbeddingModel || got.EmbeddingEndpoint != cfg.EmbeddingEndpoint {
		t.Fatalf("runtime AI config not populated: %#v", got)
	}
	if got := server.runtimeAI.currentEmbedding(); got != embedding {
		t.Fatal("runtime AI embedding instance not populated")
	}
	if server.mcpAppsEnabled() {
		t.Fatal("MCP Apps state changed independently from configured false value")
	}
}
