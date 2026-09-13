package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvolutionLifecycleBrowserRoutesAreProtectedReadOnlyViews(t *testing.T) {
	server, deviceToken, browserCookie := newEvolutionLifecycleTestServer(t)
	h := server.Handler()
	payload := map[string]any{
		"operation_id": "op_browser_read_01234567", "expected_revision": 0, "policy_version": "v1", "next_state": "active",
		"record": map[string]any{
			"evolution_id": "evo_aaaaaaaaaaaaaaaa", "title": "前端可读进化记录", "statement": "发布前执行构建和真实验证",
			"type": "runbook", "scope": "project", "project": "nexusdock", "status": "active", "policy_version": "v1",
		},
	}
	body, _ := json.Marshal(payload)
	seedRequest := httptest.NewRequest(http.MethodPost, "/internal/recall/lifecycle/transition", bytes.NewReader(body))
	seedRequest.Header.Set("Authorization", "Bearer "+deviceToken)
	seedRequest.Header.Set("Content-Type", "application/json")
	seedResponse := httptest.NewRecorder()
	h.ServeHTTP(seedResponse, seedRequest)
	if seedResponse.Code != http.StatusOK {
		t.Fatalf("seed status = %d body=%s", seedResponse.Code, seedResponse.Body.String())
	}

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/evolution/lifecycle", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/evolution/lifecycle", nil)
	listRequest.AddCookie(browserCookie)
	listResponse := httptest.NewRecorder()
	h.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	if !bytes.Contains(listResponse.Body.Bytes(), []byte(`"evolution_id":"evo_aaaaaaaaaaaaaaaa"`)) ||
		!bytes.Contains(listResponse.Body.Bytes(), []byte(`"evidence_count":0`)) {
		t.Fatalf("list body=%s", listResponse.Body.String())
	}
	if bytes.Contains(listResponse.Body.Bytes(), []byte(`"applied_operations"`)) {
		t.Fatalf("list unexpectedly exposes operation metadata: %s", listResponse.Body.String())
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/v1/evolution/lifecycle/evo_aaaaaaaaaaaaaaaa", nil)
	detailRequest.AddCookie(browserCookie)
	detailResponse := httptest.NewRecorder()
	h.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK || !bytes.Contains(detailResponse.Body.Bytes(), []byte(`"statement":"发布前执行构建和真实验证"`)) {
		t.Fatalf("detail status = %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	if bytes.Contains(detailResponse.Body.Bytes(), []byte(`"applied_operations"`)) {
		t.Fatalf("detail unexpectedly exposes operation metadata: %s", detailResponse.Body.String())
	}

	missingRequest := httptest.NewRequest(http.MethodGet, "/v1/evolution/lifecycle/evo_bbbbbbbbbbbbbbbb", nil)
	missingRequest.AddCookie(browserCookie)
	missingResponse := httptest.NewRecorder()
	h.ServeHTTP(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d body=%s", missingResponse.Code, missingResponse.Body.String())
	}
}
