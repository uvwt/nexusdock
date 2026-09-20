package httpx

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/privatenotes"
	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/workspace"
)

func newTestHandler(t *testing.T, cfg config.Config, options ...ServerOption) http.Handler {
	t.Helper()
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	nodes, err := agentdock.NewStore(db)
	if err != nil {
		t.Fatalf("New AgentDock node store: %v", err)
	}
	workspaces, err := workspace.NewStore(db)
	if err != nil {
		t.Fatalf("New Runtime Workspace store: %v", err)
	}
	store, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	privateNotes, err := privatenotes.New(filepath.Join(store.Root(), "private-notes"))
	if err != nil {
		t.Fatalf("New private notes store: %v", err)
	}
	serverOptions := []ServerOption{WithSystemDatabase(db), WithAgentDockNodes(nodes, agentdock.NewHub(nodes)), WithRuntimeWorkspaces(workspaces), WithPrivateNotes(privateNotes), WithWorkflowRegistry(newTestWorkflowRegistry(cfg.NexusDataDir))}
	serverOptions = append(serverOptions, options...)
	handler := NewServer(cfg, store, slog.Default(), serverOptions...).Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "127.0.0.1:51234"
		handler.ServeHTTP(w, r)
	})
}

func doJSON(t *testing.T, h http.Handler, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.RemoteAddr = "127.0.0.1:51234"
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func TestHealthEndpointIsPublic(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/health", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestSecurityHeadersApplyToAllResponses(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/health", nil))

	want := map[string]string{
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	}
	for name, value := range want {
		if got := res.Header().Get(name); got != value {
			t.Fatalf("%s=%q want=%q", name, got, value)
		}
	}
	if csp := res.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("content security policy=%q", csp)
	}
	if permissions := res.Header().Get("Permissions-Policy"); !strings.Contains(permissions, "camera=()") || !strings.Contains(permissions, "microphone=()") {
		t.Fatalf("permissions policy=%q", permissions)
	}
}

func TestSensitiveResponsesDisableCaching(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	for _, path := range []string{"/", "/login", "/change-password", "/v1/auth/status"} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if cacheControl := res.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
			t.Fatalf("%s Cache-Control=%q", path, cacheControl)
		}
	}
}

func TestHSTSOnlyAppliesToHTTPS(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	plain := httptest.NewRecorder()
	h.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/health", nil))
	if value := plain.Header().Get("Strict-Transport-Security"); value != "" {
		t.Fatalf("plain HTTP unexpectedly received HSTS: %q", value)
	}

	secureRequest := httptest.NewRequest(http.MethodGet, "/health", nil)
	secureRequest.TLS = &tls.ConnectionState{}
	secure := httptest.NewRecorder()
	h.ServeHTTP(secure, secureRequest)
	if value := secure.Header().Get("Strict-Transport-Security"); value != "max-age=31536000; includeSubDomains" {
		t.Fatalf("HTTPS HSTS=%q", value)
	}
}

func TestRequestBoundaryAddsRequestIDToErrors(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	res := doJSON(t, h, http.MethodPost, "/v1/recall", `{`)
	requestID := res.Header().Get("X-Request-ID")
	if !strings.HasPrefix(requestID, "req_") {
		t.Fatalf("request ID header=%q", requestID)
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["request_id"] != requestID {
		t.Fatalf("error request_id=%v header=%q body=%s", body["request_id"], requestID, res.Body.String())
	}
}

func TestRequestBoundaryRecoversPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server := &Server{logger: logger}
	h := server.requestBoundary(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if requestID := requestIDFromContext(r.Context()); !strings.HasPrefix(requestID, "req_") {
			t.Fatalf("request context ID=%q", requestID)
		}
		panic("injected panic")
	}))
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("panic status=%d body=%s", res.Code, res.Body.String())
	}
	requestID := res.Header().Get("X-Request-ID")
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	detail, ok := body["error"].(map[string]any)
	if !ok || detail["code"] != "INTERNAL_ERROR" || body["request_id"] != requestID {
		t.Fatalf("panic body=%#v request_id=%q", body, requestID)
	}
}

func TestV1LocalhostAPIAccessWithoutWebAuthentication(t *testing.T) {
	h := newTestHandler(t, config.Config{})

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/recall", nil)
	req.RemoteAddr = "127.0.0.1:51234"
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("loopback API access status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestUIAssetMissDoesNotFallbackToIndex(t *testing.T) {
	h := newTestHandler(t, config.Config{})

	missingAsset := httptest.NewRecorder()
	h.ServeHTTP(missingAsset, httptest.NewRequest(http.MethodGet, "/ui/assets/missing.js", nil))
	if missingAsset.Code != http.StatusNotFound {
		t.Fatalf("missing asset status=%d body=%s", missingAsset.Code, missingAsset.Body.String())
	}

	spaRoute := httptest.NewRecorder()
	h.ServeHTTP(spaRoute, httptest.NewRequest(http.MethodGet, "/ui/devices/unknown", nil))
	if spaRoute.Code != http.StatusOK {
		t.Fatalf("spa route status=%d body=%s", spaRoute.Code, spaRoute.Body.String())
	}
	if !strings.Contains(spaRoute.Body.String(), `id="root"`) {
		t.Fatalf("spa route did not serve index.html: %s", spaRoute.Body.String())
	}
}

func TestWriteMoveDeleteConfirmationAndErrorShape(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	res := doJSON(t, h, http.MethodPost, "/v1/recall", `{"path":"profile.md","content":"# Profile"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("write without confirmation status=%d body=%s", res.Code, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("error response is not json: %v", err)
	}
	if payload["ok"] != false || payload["error"] == nil {
		t.Fatalf("unexpected error shape: %#v", payload)
	}

	res = doJSON(t, h, http.MethodPost, "/v1/recall", `{"path":"profile.md","content":"# Profile","confirmed":true}`)
	if res.Code != http.StatusOK {
		t.Fatalf("confirmed write status=%d body=%s", res.Code, res.Body.String())
	}
	res = doJSON(t, h, http.MethodPost, "/v1/recall/move", `{"from_path":"profile.md","to_path":"recall/docs/projects/demo/project.md"}`)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "confirmed") {
		t.Fatalf("move without confirmation status=%d body=%s", res.Code, res.Body.String())
	}
	res = doJSON(t, h, http.MethodDelete, "/v1/recall/profile.md", "")
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "confirmed") {
		t.Fatalf("delete without confirmation status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestRecallContextIndexEndpointReturnsCompactMixedCategories(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	writes := []map[string]any{
		{"path": "profile.md", "content": "# Profile\n\n## Git 协作\n\n" + strings.Repeat("长期偏好", 1200), "scope": "profile", "status": "active", "confirmed": true},
		{"path": "recall/docs/projects/agentdock/project.md", "content": "# AgentDock\n\n## 当前架构\n\n项目事实。", "scope": "project", "project": "agentdock", "status": "active", "confirmed": true},
		{"path": "recall/docs/projects/agentdock/runbooks/deploy.md", "content": "# Deploy\n\n部署步骤必须全文读取。", "scope": "project", "project": "agentdock", "status": "active", "confirmed": true},
	}
	for _, write := range writes {
		body, err := json.Marshal(write)
		if err != nil {
			t.Fatal(err)
		}
		res := doJSON(t, h, http.MethodPost, "/v1/recall", string(body))
		if res.Code != http.StatusOK {
			t.Fatalf("write status=%d body=%s", res.Code, res.Body.String())
		}
	}
	card, _ := json.Marshal(map[string]any{
		"title": "Global preference", "content": "Use compact context for startup routing, then read exact Recall only when the task needs it.", "type": "preference", "scope": "global",
		"project": "global", "status": "active", "confidence": "high", "evidence": "verified by context-index integration test", "confirmed": true,
	})
	if res := doJSON(t, h, http.MethodPost, "/v1/recall/cards", string(card)); res.Code != http.StatusOK {
		t.Fatalf("card write status=%d body=%s", res.Code, res.Body.String())
	}

	res := doJSON(t, h, http.MethodPost, "/v1/recall/context-index", `{"project":"agentdock","max_bytes":3000}`)
	if res.Code != http.StatusOK {
		t.Fatalf("context index status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		OK           bool                `json:"ok"`
		ContextIndex recall.ContextIndex `json:"context_index"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.ContextIndex.TotalBytes > 3000 {
		t.Fatalf("context index response=%#v", payload)
	}
	kinds := map[string]bool{}
	for _, item := range payload.ContextIndex.Items {
		kinds[item.Kind] = true
	}
	for _, kind := range []string{"profile", "project", "runbook", "card"} {
		if !kinds[kind] {
			t.Fatalf("context index missing %s: %#v", kind, payload.ContextIndex.Items)
		}
	}
}

func TestJSONEndpointsRejectTrailingValues(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	res := doJSON(t, h, http.MethodPost, "/v1/recall/search", `{"query":"first"} {"query":"second"}`)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "INVALID_JSON") {
		t.Fatalf("trailing JSON status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestJSONEndpointsRejectOversizedBodies(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	body := `{"query":"` + strings.Repeat("x", maxJSONRequestBytes) + `"}`
	res := doJSON(t, h, http.MethodPost, "/v1/recall/search", body)
	if res.Code != http.StatusRequestEntityTooLarge || !strings.Contains(res.Body.String(), "REQUEST_TOO_LARGE") {
		t.Fatalf("oversized JSON status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestCardEndpointsPlanWriteAndSearch(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	captureBody := `{"title":"Deploy check","content":"Deployment must verify the final service endpoint instead of only source files.","type":"project_trap","scope":"project","project":"chatdock","status":"inbox","confidence":"high"}`
	res := doJSON(t, h, http.MethodPost, "/v1/recall/cards/capture", captureBody)
	if res.Code != http.StatusOK {
		t.Fatalf("capture status=%d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "capture_plan") || !strings.Contains(res.Body.String(), "auto_write") {
		t.Fatalf("capture response missing plan: %s", res.Body.String())
	}

	writeBody := `{"title":"Deploy check","content":"Deployment must verify the final service endpoint instead of only source files.","type":"project_trap","scope":"project","project":"chatdock","status":"inbox","confidence":"high","confirmed":true}`
	res = doJSON(t, h, http.MethodPost, "/v1/recall/cards", writeBody)
	if res.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "recall/managed/cards/chatdock/inbox/project_trap/") {
		t.Fatalf("write response missing card path: %s", res.Body.String())
	}

	res = doJSON(t, h, http.MethodGet, "/v1/recall/cards", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"title":"Deploy check"`) || !strings.Contains(res.Body.String(), `"card_type":"project_trap"`) {
		t.Fatalf("list response missing card summary: status=%d body=%s", res.Code, res.Body.String())
	}

	res = doJSON(t, h, http.MethodPost, "/v1/recall/cards/search", `{"query":"service endpoint","max_results":5}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Deploy check") {
		t.Fatalf("search status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestEmbeddingStatusIsGracefullyDisabled(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/embeddings/status", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("embedding status code=%d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"enabled":false`) {
		t.Fatalf("embedding status should be disabled: %s", res.Body.String())
	}
}

func TestRuntimeRoutesRequireExplicitNodeID(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	for _, path := range []string{
		"/v1/runtime/tasks",
		"/v1/runtime/skills",
		"/v1/runtime/mcp",
		"/v1/runtime/overview",
		"/v1/runtime/workflow-templates",
	} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("legacy singleton route %s status = %d, want 404", path, response.Code)
		}
	}

	for _, path := range []string{
		"/v1/runtime/nodes/dockmini/tasks",
		"/v1/runtime/nodes/dockmini/skills",
		"/v1/runtime/nodes/dockmini/mcp",
		"/v1/runtime/nodes/dockmini/overview",
	} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "AGENTDOCK_NODE_NOT_FOUND") {
			t.Fatalf("node route %s was not registered: status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestRetiredRoutesAreNotRegistered(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	for _, path := range []string{"/v1/schedules", "/v1/backup/status"} {
		t.Run(path, func(t *testing.T) {
			res := httptest.NewRecorder()
			h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
			if res.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", res.Code)
			}
		})
	}
}

func TestRecallRemoteSyncRoutesAreRemoved(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/sync/status"},
		{http.MethodPost, "/v1/sync/pull"},
		{http.MethodPost, "/v1/sync/push"},
		{http.MethodPost, "/v1/sync/now"},
	} {
		res := doJSON(t, h, tc.method, tc.path, `{}`)
		if res.Code != http.StatusNotFound && res.Code != http.StatusMethodNotAllowed {
			t.Fatalf("legacy route %s %s status=%d body=%s", tc.method, tc.path, res.Code, res.Body.String())
		}
	}
}
