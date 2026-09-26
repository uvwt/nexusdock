package e2e_test

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/httpx"
	"github.com/uvwt/nexusdock/internal/recall"
)

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	root := t.TempDir()
	store, err := recall.NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	handler := httpx.NewServer(config.Config{RecallRepoDir: root}, store, slog.Default()).Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "127.0.0.1:51234"
		handler.ServeHTTP(w, r)
	})
}

func TestHealthAndEmbeddedNexusUI(t *testing.T) {
	handler := newHandler(t)

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"ok":true`) {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}

	ui := httptest.NewRecorder()
	handler.ServeHTTP(ui, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if ui.Code != http.StatusOK {
		t.Fatalf("ui status=%d body=%s", ui.Code, ui.Body.String())
	}
	if !strings.Contains(ui.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("ui index does not contain application mount point: %s", ui.Body.String())
	}
	if !strings.Contains(ui.Body.String(), `<title>NexusDock</title>`) {
		t.Fatalf("ui index still exposes legacy title: %s", ui.Body.String())
	}
}

func TestRecallAPIWorks(t *testing.T) {
	handler := newHandler(t)

	create := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/recall", strings.NewReader(`{"path":"recall/docs/inbox/e2e.md","content":"# E2E\n\nRecall API is canonical."}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(create, request)
	if create.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/v1/recall/recall/docs/inbox/e2e.md", nil))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "Recall API is canonical") {
		t.Fatalf("read status=%d body=%s", read.Code, read.Body.String())
	}
}

func TestBuiltFrontendContainsNexusSectionsAndResponsiveRules(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "httpx", "web_dist")
	var javascript strings.Builder
	var styles strings.Builder
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		switch filepath.Ext(path) {
		case ".js":
			javascript.Write(data)
		case ".css":
			styles.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk frontend dist: %v", err)
	}
	for _, label := range []string{"AgentDock", "Nexus", "总览", "Recall", "Runtime", "MCP 服务", "添加 MCP", "隔离环境", "设置", "系统概览", "数据库", "拒绝跨源 API 请求", "INVALID_JSON"} {
		if !strings.Contains(javascript.String(), label) {
			t.Errorf("frontend bundle missing section label %q", label)
		}
	}
	for _, rule := range []string{"nexus-sidebar.is-open", "grid-template-columns:1fr", "nexus-mobile-menu", "nexus-scrim", "settings-grid", "mcp-layout", "mcp-env-form"} {
		if !strings.Contains(styles.String(), rule) {
			t.Errorf("frontend stylesheet missing responsive rule %q", rule)
		}
	}
}

func TestPublicAPIStatusAndErrorEnvelope(t *testing.T) {
	handler := newHandler(t)

	health := decodeJSON(t, handler, http.MethodGet, "/health", "")
	if health.Code != http.StatusOK || health.Body["ok"] != true || health.Body["service"] != "nexusdock" {
		t.Fatalf("health status=%d body=%#v", health.Code, health.Body)
	}
	auth := decodeJSON(t, handler, http.MethodGet, "/v1/auth/status", "")
	if auth.Code != http.StatusOK || auth.Body["ok"] != true || auth.Body["initialized"] != false {
		t.Fatalf("auth status=%d body=%#v", auth.Code, auth.Body)
	}
	cards := decodeJSON(t, handler, http.MethodGet, "/v1/recall/cards", "")
	if cards.Code != http.StatusOK || cards.Body["ok"] != true || cards.Body["count"] != float64(0) || cards.Body["prefix"] != "recall/managed/cards" {
		t.Fatalf("recall cards status=%d body=%#v", cards.Code, cards.Body)
	}
	embedding := decodeJSON(t, handler, http.MethodGet, "/v1/embeddings/status", "")
	if embedding.Code != http.StatusOK || embedding.Body["ok"] != true || embedding.Body["enabled"] != false || embedding.Body["configured"] != false {
		t.Fatalf("embedding status=%d body=%#v", embedding.Code, embedding.Body)
	}
	create := decodeJSON(t, handler, http.MethodPost, "/v1/recall", `{"path":"recall/docs/inbox/client.md","content":"# Client contract\n"}`)
	if create.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%#v", create.Code, create.Body)
	}
	read := decodeJSON(t, handler, http.MethodGet, "/v1/recall/recall/docs/inbox/client.md", "")
	recallDoc, _ := read.Body["recall"].(map[string]any)
	content, _ := recallDoc["content"].(string)
	if read.Code != http.StatusOK || read.Body["ok"] != true || recallDoc["path"] != "recall/docs/inbox/client.md" || !strings.Contains(content, "Client contract") {
		t.Fatalf("recall result status=%d body=%#v", read.Code, read.Body)
	}

	missing := decodeJSON(t, handler, http.MethodGet, "/v1/recall/recall/docs/missing.md", "")
	requestID, _ := missing.Body["request_id"].(string)
	code, _ := missing.Body["code"].(string)
	if detail, ok := missing.Body["error"].(map[string]any); ok && code == "" {
		code, _ = detail["code"].(string)
	}
	if missing.Code != http.StatusNotFound || code != "READ_FAILED" || !strings.HasPrefix(requestID, "req_") {
		t.Fatalf("missing recall status=%d body=%#v", missing.Code, missing.Body)
	}
}

type jsonResponse struct {
	Code int
	Body map[string]any
}

func decodeJSON(t *testing.T, handler http.Handler, method, path, rawBody string) jsonResponse {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(rawBody))
	if rawBody != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: decode %s: %v", method, path, recorder.Body.String(), err)
	}
	return jsonResponse{Code: recorder.Code, Body: body}
}
