package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/recall"
)

func newEvolutionLifecycleTestServer(t *testing.T) (*Server, string, *http.Cookie) {
	t.Helper()
	server := newNodeTestServer(t)
	store, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server.store = store

	pairing, err := server.agentDock.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := server.agentDock.Pair(t.Context(), agentdock.PairInput{Code: pairing.Code, DeviceID: "device_evolution_12345678", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := server.auth.IssueToken(t.Context(), core.Actor{Type: core.ActorDevice, ID: node.ID}, "device_token", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := server.auth.InitializeAdmin(t.Context(), "owner", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	login, err := server.auth.Login(t.Context(), "owner", "correct horse battery staple", "127.0.0.0/24", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookieName, Value: login.Token}
	return server, issued.Token, cookie
}

func TestEvolutionLifecycleAPIUsesDeviceTokenAndCAS(t *testing.T) {
	server, deviceToken, _ := newEvolutionLifecycleTestServer(t)
	h := server.Handler()
	payload := map[string]any{
		"operation_id": "op_0123456789abcdef", "expected_revision": 0, "policy_version": "v1", "next_state": "provisional",
		"record": map[string]any{"evolution_id": "evo_0123456789abcdef", "title": "x", "statement": "wait for readiness", "type": "runbook", "scope": "project", "project": "agentdock", "status": "provisional", "policy_version": "v1"},
	}
	body, _ := json.Marshal(payload)

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/internal/recall/lifecycle/transition", bytes.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/internal/recall/lifecycle/transition", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+deviceToken)
	request.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	h.ServeHTTP(created, request)
	if created.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}

	queryBody := []byte(`{"evolution_id":"evo_0123456789abcdef"}`)
	query := httptest.NewRequest(http.MethodPost, "/internal/recall/lifecycle/query", bytes.NewReader(queryBody))
	query.Header.Set("Authorization", "Bearer "+deviceToken)
	query.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, query)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"revision":1`)) {
		t.Fatalf("query status = %d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}
