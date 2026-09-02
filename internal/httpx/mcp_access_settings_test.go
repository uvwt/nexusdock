package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/auth"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/settings"
)

func TestMCPAccessTokenSettingsReadAndReset(t *testing.T) {
	server, _ := newOAuthHTTPTestServer(t)
	handler := server.Handler()
	oldToken := server.mcpToken.Token()

	readReq := httptest.NewRequest(http.MethodGet, "https://nexus.example/v1/settings/mcp-token", nil)
	readReq.Header.Set("Authorization", "Bearer ops-secret")
	readRes := httptest.NewRecorder()
	handler.ServeHTTP(readRes, readReq)
	if readRes.Code != http.StatusOK {
		t.Fatalf("read token status=%d body=%s", readRes.Code, readRes.Body.String())
	}
	if got := readRes.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("read token Cache-Control=%q want=no-store", got)
	}
	var readBody mcpAccessTokenResponse
	if err := json.Unmarshal(readRes.Body.Bytes(), &readBody); err != nil {
		t.Fatal(err)
	}
	if !readBody.OK || readBody.Token != oldToken {
		t.Fatalf("read token mismatch: %#v", readBody)
	}

	resetReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/v1/settings/mcp-token/reset", nil)
	resetReq.Header.Set("Authorization", "Bearer ops-secret")
	resetRes := httptest.NewRecorder()
	handler.ServeHTTP(resetRes, resetReq)
	if resetRes.Code != http.StatusOK {
		t.Fatalf("reset token status=%d body=%s", resetRes.Code, resetRes.Body.String())
	}
	if got := resetRes.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("reset token Cache-Control=%q want=no-store", got)
	}
	var resetBody mcpAccessTokenResponse
	if err := json.Unmarshal(resetRes.Body.Bytes(), &resetBody); err != nil {
		t.Fatal(err)
	}
	if !resetBody.OK || resetBody.Token == "" || resetBody.Token == oldToken {
		t.Fatalf("reset did not rotate token: %#v", resetBody)
	}

	called := false
	wrapped := server.withMCPAccess(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	oldReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/mcp", nil)
	oldReq.Header.Set("Authorization", "Bearer "+oldToken)
	oldRes := httptest.NewRecorder()
	wrapped(oldRes, oldReq)
	if called || oldRes.Code != http.StatusUnauthorized {
		t.Fatalf("old token remained valid: called=%v status=%d", called, oldRes.Code)
	}

	newReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/mcp", nil)
	newReq.Header.Set("Authorization", "Bearer "+resetBody.Token)
	newRes := httptest.NewRecorder()
	wrapped(newRes, newReq)
	if !called || newRes.Code != http.StatusNoContent {
		t.Fatalf("new token rejected: called=%v status=%d", called, newRes.Code)
	}
}

func TestMCPSettingsAPIUpdatesGatewayAndPersists(t *testing.T) {
	db, err := core.OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "nexus.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	store, err := settings.NewMCPStore(db, true)
	if err != nil {
		t.Fatal(err)
	}
	tokenStore, err := auth.NewMCPTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg: config.Config{MCPAppsEnabled: true}, mcpSettings: store, mcpToken: tokenStore,
	}

	getRecorder := httptest.NewRecorder()
	server.getMCPSettings(getRecorder, httptest.NewRequest(http.MethodGet, "/v1/settings/mcp", nil))
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET settings status = %d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	var initial mcpSettingsResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if !initial.MCPAppsEnabled || initial.Persisted {
		t.Fatalf("unexpected initial settings: %#v", initial)
	}

	putRecorder := httptest.NewRecorder()
	server.updateMCPSettings(putRecorder, httptest.NewRequest(http.MethodPut, "/v1/settings/mcp", strings.NewReader(`{"mcp_apps_enabled":false}`)))
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("PUT settings status = %d body=%s", putRecorder.Code, putRecorder.Body.String())
	}
	if server.mcpAppsEnabled() {
		t.Fatal("gateway MCP Apps UI remained enabled after settings update")
	}
	enabled, view, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if enabled || view.MCPAppsEnabled || !view.Persisted {
		t.Fatalf("updated settings were not persisted: enabled=%v view=%#v", enabled, view)
	}
}
