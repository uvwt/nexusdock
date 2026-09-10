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

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/auth"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/recall"
)

func newNodeTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := core.OpenSQLite(context.Background(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	store, err := agentdock.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{agentDock: store, agentDockHub: agentdock.NewHub(store), auth: auth.NewService(db)}
}

func TestPairingIssuesDeviceTokenWithoutAgentDockToken(t *testing.T) {
	server := newNodeTestServer(t)
	pairing, err := server.agentDock.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/nodes/pair", strings.NewReader(`{"code":"`+pairing.Code+`","device_id":"device_12345678","name":"DockMini"}`))
	response := httptest.NewRecorder()
	server.agentDockNodePair(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Node        agentdock.Node `json:"node"`
		DeviceToken string         `json:"device_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Node.ID == "" || result.DeviceToken == "" || strings.Contains(response.Body.String(), "endpoint") {
		t.Fatalf("unexpected pairing response: %s", response.Body.String())
	}
	principal, err := server.auth.Authenticate(t.Context(), result.DeviceToken)
	if err != nil || principal.Actor.Type != core.ActorDevice || principal.Actor.ID != result.Node.ID {
		t.Fatalf("device principal=%#v err=%v", principal, err)
	}
}

func TestNodeListReportsHubOnlineState(t *testing.T) {
	server := newNodeTestServer(t)
	pairing, _ := server.agentDock.CreatePairingCode(t.Context())
	if _, err := server.agentDock.Pair(t.Context(), agentdock.PairInput{Code: pairing.Code, DeviceID: "device_12345678", Name: "DockMini"}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.agentDockNodeList(response, httptest.NewRequest(http.MethodGet, "/v1/runtime/nodes", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"online":false`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNodeConnectDistinguishesInvalidTokenFromAuthBackendFailure(t *testing.T) {
	t.Run("invalid token", func(t *testing.T) {
		server := newNodeTestServer(t)
		request := httptest.NewRequest(http.MethodGet, "/v1/nodes/connect", nil)
		request.Header.Set("Authorization", "Bearer invalid-device-token")
		response := httptest.NewRecorder()

		server.agentDockNodeConnect(response, request)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"INVALID_DEVICE_TOKEN"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("authentication backend failure", func(t *testing.T) {
		db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
		if err != nil {
			t.Fatal(err)
		}
		authService := auth.NewService(db)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		server := &Server{auth: authService, agentDockHub: agentdock.NewHub(nil)}
		request := httptest.NewRequest(http.MethodGet, "/v1/nodes/connect", nil)
		request.Header.Set("Authorization", "Bearer any-device-token")
		response := httptest.NewRecorder()

		server.agentDockNodeConnect(response, request)
		if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"AGENTDOCK_DEVICE_AUTH_FAILED"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestDeviceTokenAccessesOnlyExplicitDeviceRoutes(t *testing.T) {
	server := newNodeTestServer(t)
	pairing, _ := server.agentDock.CreatePairingCode(t.Context())
	node, err := server.agentDock.Pair(t.Context(), agentdock.PairInput{Code: pairing.Code, DeviceID: "device_12345678", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := server.auth.IssueToken(t.Context(), core.Actor{Type: core.ActorDevice, ID: node.ID}, "device_token", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/recall", nil)
	request.Header.Set("Authorization", "Bearer "+issued.Token)
	response := httptest.NewRecorder()
	server.withDeviceOrAPIAccess(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("device route status=%d body=%s", response.Code, response.Body.String())
	}

	// 管理 API 仍使用普通 API/管理员认证，Device Token 不会被 withAPIAccess 接受。
	adminResponse := httptest.NewRecorder()
	server.withAPIAccess(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })(adminResponse, request)
	if adminResponse.Code != http.StatusUnauthorized {
		t.Fatalf("admin route status=%d, want 401", adminResponse.Code)
	}
}

func TestDeviceTokenRecallPreviewUsesRealStoreWithoutPersistence(t *testing.T) {
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
	node, err := server.agentDock.Pair(t.Context(), agentdock.PairInput{Code: pairing.Code, DeviceID: "device_preview_12345678", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := server.auth.IssueToken(t.Context(), core.Actor{Type: core.ActorDevice, ID: node.ID}, "device_token", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	handler := server.Handler()
	body := `{"path":"recall/docs/inbox/http-preview.md","content":"# Preview\n\nno persistence"}`
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/v1/recall/preview", strings.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized preview status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/recall/preview", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+issued.Token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("device preview status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["dry_run"] != true || payload["path"] != "recall/docs/inbox/http-preview.md" {
		t.Fatalf("preview payload=%#v", payload)
	}
	if _, err := store.Read("recall/docs/inbox/http-preview.md"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview persisted Recall entry: %v", err)
	}
}

func TestNodeDisablePromotesConvergedToolContract(t *testing.T) {
	server := newNodeTestServer(t)
	server.mcpServer = mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	server.mcpTools = make(map[string]publishedNodeTool)
	oldDescriptor, newDescriptor := nodeLifecycleTestDescriptors()
	oldNode := pairHTTPTestNode(t, server.agentDock, "device_disable_old", "DockMini", "1.8.3", oldDescriptor)
	newNode := pairHTTPTestNode(t, server.agentDock, "device_disable_new", "DockAir", "1.9.0", newDescriptor)
	server.registerNodeTools(oldNode, agentdock.Hello{Tools: []agentdock.ToolDescriptor{oldDescriptor}})
	server.registerNodeTools(newNode, agentdock.Hello{Tools: []agentdock.ToolDescriptor{newDescriptor}})

	request := httptest.NewRequest(http.MethodPatch, "/v1/runtime/nodes/"+oldNode.ID, strings.NewReader(`{"enabled":false}`))
	request.SetPathValue("nodeID", oldNode.ID)
	response := httptest.NewRecorder()
	server.agentDockNodeUpdate(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	newHash, _ := toolContractHash(newDescriptor)
	published, _ := server.publishedNodeTool("exec_command")
	if published.ContractHash != newHash || len(published.AcceptedSemanticHashes) != 1 || published.AcceptedSemanticHashes[0] != newHash {
		t.Fatalf("disable did not promote remaining contract: %#v", published)
	}
}

func TestNodeEnableReconcileKeepsCanonicalToolsCentral(t *testing.T) {
	server := newNodeTestServer(t)
	server.mcpTools = make(map[string]publishedNodeTool)
	server.mcpResources = make(map[string]struct{})

	input, ok := mcpcontract.InputSchema(mcpcontract.ToolAgentDockContext)
	if !ok {
		t.Fatal("agentdock_context input contract is missing")
	}
	descriptor := agentdock.ToolDescriptor{
		Name:        mcpcontract.ToolAgentDockContext,
		InputSchema: input,
	}
	node := pairHTTPTestNode(t, server.agentDock, "device_canonical_reconcile", "DockMini", "1.9.0", descriptor)
	server.initializeMCPGateway()

	// 即使 enabled 没有变化，管理端 PATCH 仍会触发一次完整工具契约重算。
	request := httptest.NewRequest(http.MethodPatch, "/v1/runtime/nodes/"+node.ID, strings.NewReader(`{"enabled":true}`))
	request.SetPathValue("nodeID", node.ID)
	response := httptest.NewRecorder()
	server.agentDockNodeUpdate(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	contracts, err := server.agentDock.ListPublishedToolContracts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 0 {
		t.Fatalf("node reconcile published canonical contracts: %#v", contracts)
	}

	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "nexusdock-test", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	tools, err := clientSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != mcpcontract.ToolAgentDockContext {
			continue
		}
		properties := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		if _, hasNodeID := properties["node_id"]; hasNodeID {
			t.Fatalf("node reconcile replaced central agentdock_context: %#v", tool.InputSchema)
		}
		return
	}
	t.Fatal("agentdock_context is missing")
}

func TestNodeDeletePromotesConvergedToolContract(t *testing.T) {
	server := newNodeTestServer(t)
	server.mcpServer = mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	server.mcpTools = make(map[string]publishedNodeTool)
	oldDescriptor, newDescriptor := nodeLifecycleTestDescriptors()
	oldNode := pairHTTPTestNode(t, server.agentDock, "device_delete_old", "DockMini", "1.8.3", oldDescriptor)
	newNode := pairHTTPTestNode(t, server.agentDock, "device_delete_new", "DockAir", "1.9.0", newDescriptor)
	server.registerNodeTools(oldNode, agentdock.Hello{Tools: []agentdock.ToolDescriptor{oldDescriptor}})
	server.registerNodeTools(newNode, agentdock.Hello{Tools: []agentdock.ToolDescriptor{newDescriptor}})

	request := httptest.NewRequest(http.MethodDelete, "/v1/runtime/nodes/"+oldNode.ID, nil)
	request.SetPathValue("nodeID", oldNode.ID)
	response := httptest.NewRecorder()
	server.agentDockNodeDelete(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	newHash, _ := toolContractHash(newDescriptor)
	published, _ := server.publishedNodeTool("exec_command")
	if published.ContractHash != newHash || len(published.AcceptedSemanticHashes) != 1 || published.AcceptedSemanticHashes[0] != newHash {
		t.Fatalf("delete did not promote remaining contract: %#v", published)
	}
}

func TestNodeDeleteRetiresLastPublishedTool(t *testing.T) {
	server := newNodeTestServer(t)
	server.mcpServer = mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	server.mcpTools = make(map[string]publishedNodeTool)
	descriptor := agentdock.ToolDescriptor{
		Name:        "browser_act",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	node := pairHTTPTestNode(t, server.agentDock, "device_delete_last", "DockMini", "1.9.0", descriptor)
	server.registerNodeTools(node, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})

	request := httptest.NewRequest(http.MethodDelete, "/v1/runtime/nodes/"+node.ID, nil)
	request.SetPathValue("nodeID", node.ID)
	response := httptest.NewRecorder()
	server.agentDockNodeDelete(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, ok := server.publishedNodeTool("browser_act"); ok {
		t.Fatal("last deleted provider should retire browser_act")
	}
	contracts, err := server.agentDock.ListPublishedToolContracts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 0 {
		t.Fatalf("published contracts after last provider delete = %#v", contracts)
	}
}

func nodeLifecycleTestDescriptors() (agentdock.ToolDescriptor, agentdock.ToolDescriptor) {
	oldDescriptor := agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "integer"}},
		},
	}
	newDescriptor := agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "number"}},
		},
	}
	return oldDescriptor, newDescriptor
}
