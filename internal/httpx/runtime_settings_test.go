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
	"time"

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
		"stage3": map[string]any{
			"enabled": false, "endpoint": "", "model": "", "timeout_seconds": 60, "interval_minutes": 360,
			"api_key": map[string]any{"action": "keep"},
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

func TestWorkflowEmbeddingUsesRuntimeAPIKey(t *testing.T) {
	const token = "workflow-embedding-secret"
	var authorization string
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float64{1, 0}}}})
	}))
	defer embedding.Close()

	server := &Server{}
	vectors, err := server.embedWorkflowTemplateTexts(t.Context(), settings.RuntimeAIConfig{
		EmbeddingEndpoint: embedding.URL,
		EmbeddingModel:    "test-embedding",
		EmbeddingAPIKey:   token,
		EmbeddingTimeout:  time.Second,
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

func TestRuntimeAIConnectionTestsUseSavedSecrets(t *testing.T) {
	const (
		stage3Token    = "stage3-test-secret"
		embeddingToken = "embedding-test-secret"
	)
	var stage3Authorization, embeddingAuthorization, stage3Path string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stage3Authorization = r.Header.Get("Authorization")
		stage3Path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "OK"}}}})
	}))
	defer model.Close()
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
		"stage3": map[string]any{
			"enabled": true, "endpoint": model.URL + "/v1", "model": "chat-test", "timeout_seconds": 5, "interval_minutes": 360,
			"api_key": map[string]any{"action": "replace", "value": stage3Token},
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

	for _, test := range []struct {
		path   string
		target string
	}{
		{path: "/v1/settings/ai/test/stage3", target: "stage3"},
		{path: "/v1/settings/ai/test/embedding", target: "embedding"},
	} {
		response := httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, test.path, nil)
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("%s test status=%d body=%s", test.target, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), stage3Token) || strings.Contains(response.Body.String(), embeddingToken) {
			t.Fatalf("%s test response leaked a secret: %s", test.target, response.Body.String())
		}
		var result runtimeAITestResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !result.OK || result.Target != test.target || result.Message == "" {
			t.Fatalf("unexpected %s test result: %#v", test.target, result)
		}
	}
	if stage3Authorization != "Bearer "+stage3Token {
		t.Fatalf("stage3 authorization=%q", stage3Authorization)
	}
	if stage3Path != "/v1/chat/completions" {
		t.Fatalf("stage3 request path=%q", stage3Path)
	}
	if embeddingAuthorization != "Bearer "+embeddingToken {
		t.Fatalf("embedding authorization=%q", embeddingAuthorization)
	}
}
