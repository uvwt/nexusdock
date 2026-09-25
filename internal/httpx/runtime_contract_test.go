package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/core"
	"log/slog"
)

// runtimeContractTestServer 启动一台假的 AgentDock 节点并接入真实 Hub，
// 让 Runtime 视图走完整的“HTTP handler → Bridge 调用 → DTO 解析 → UI JSON”链路。
// canned 按上游 path 提供响应 JSON，用来模拟真实节点、旧节点与契约漂移。
func runtimeContractTestServer(t *testing.T, canned map[string]string) (*Server, *http.ServeMux, string) {
	t.Helper()
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := agentdock.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), agentdock.PairInput{Code: pairing.Code, DeviceID: "device_runtime_contract", Name: "契约测试节点"})
	if err != nil {
		t.Fatal(err)
	}
	hub := agentdock.NewHub(store)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, node.ID, ""); err != nil {
			t.Errorf("accept node: %v", err)
		}
	}))
	t.Cleanup(bridge.Close)

	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(bridge.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if err := socket.WriteJSON(map[string]any{
		"type": protocol.MessageNodeHello, "protocol_version": agentdock.ConnectionProtocolVersion,
		"hello": map[string]any{
			"device_id": node.DeviceID, "protocol_version": agentdock.ConnectionProtocolVersion,
			"capabilities": []string{}, "bridge_capabilities": []string{}, "tools": []any{}, "ui_resources": []any{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := socket.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}

	// 节点侧循环：对每条 runtime.request 用 canned 响应回结果。
	go func() {
		for {
			var invoke map[string]any
			if err := socket.ReadJSON(&invoke); err != nil {
				return
			}
			arguments, _ := invoke["arguments"].(map[string]any)
			path, _ := arguments["path"].(string)
			result, ok := canned[path]
			if !ok {
				result = `{"ok": false}`
			}
			if err := socket.WriteJSON(map[string]any{
				"type": protocol.MessageToolResult, "request_id": invoke["request_id"], "result": json.RawMessage(result),
			}); err != nil {
				return
			}
		}
	}()

	server := &Server{agentDock: store, agentDockHub: hub, logger: slog.Default()}
	mux := http.NewServeMux()
	// Runtime 视图是受保护路由；这里用直通中间件聚焦契约行为本身。
	server.registerRuntimeRoutes(mux, func(next http.HandlerFunc) http.HandlerFunc { return next })
	return server, mux, node.ID
}

func runtimeContractRequest(t *testing.T, mux *http.ServeMux, method, target string) map[string]any {
	t.Helper()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(method, target, nil))
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是 JSON（status=%d）: %s", response.Code, response.Body.String())
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	return payload
}

func TestRuntimeTasksThroughBridgeReturnsTypedView(t *testing.T) {
	_, mux, nodeID := runtimeContractTestServer(t, map[string]string{
		"/internal/runtime/tasks": `{"ok": true, "source": "agentdock-api", "action": "list", "count": 1, "tasks": [{
			"id": "task_01", "title": "修复登录超时", "goal": "登录不再超时", "status": "active",
			"phase": "execute", "review_status": "not_started", "completed_step_count": 1, "step_count": 3,
			"current_step": {"id": "s2", "title": "复现问题", "status": "in_progress"},
			"updated_at": "2026-09-13T08:00:00Z", "created_at": "2026-09-13T07:00:00Z", "event_count": 4}]}`,
	})
	payload := runtimeContractRequest(t, mux, http.MethodGet, "/v1/runtime/nodes/"+nodeID+"/tasks")
	if payload["ok"] != true || payload["total"] != float64(1) {
		t.Fatalf("任务列表响应错误: %v", payload)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", payload["items"])
	}
	task, _ := items[0].(map[string]any)
	// file_name 是 UI 侧派生字段，等于任务 ID；计数字段来自类型化 DTO。
	if task["file_name"] != "task_01" || task["completed_step_count"] != float64(1) || task["step_count"] != float64(3) {
		t.Fatalf("任务摘要字段错误: %v", task)
	}
	currentStep, _ := task["current_step"].(map[string]any)
	if currentStep["title"] != "复现问题" || currentStep["status"] != "in_progress" {
		t.Fatalf("current_step 错误: %v", currentStep)
	}
}

func TestRuntimeTasksThroughBridgeRejectsContractDrift(t *testing.T) {
	// 上游把 tasks 数组改成了对象：必须显式报错并带字段路径，而不是渲染成空列表。
	_, mux, nodeID := runtimeContractTestServer(t, map[string]string{
		"/internal/runtime/tasks": `{"ok": true, "tasks": {"id": "task_01"}}`,
	})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/runtime/nodes/"+nodeID+"/tasks", nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"code":"AGENTDOCK_RUNTIME_BAD_RESPONSE"`) {
		t.Fatalf("缺少契约错误码: %s", body)
	}
	if !strings.Contains(body, "tasks") || !strings.Contains(body, nodeID) {
		t.Fatalf("错误缺少字段路径与节点上下文: %s", body)
	}
}

func TestRuntimeMCPServersThroughBridgeReturnsTypedView(t *testing.T) {
	_, mux, nodeID := runtimeContractTestServer(t, map[string]string{
		"/internal/runtime/mcp": `{"action": "list", "ok": true, "source": "agentdock-api", "count": 2, "servers": [
			{"name": "github", "description": "GitHub MCP", "transport": "streamable_http",
			 "source_type": "standalone", "enabled": true, "status": "ready", "tool_count": 12},
			{"name": "plugin.context7.context7", "description": "Context7 Plugin MCP", "transport": "streamable_http",
			 "source_type": "plugin", "plugin_name": "context7", "enabled": true, "status": "ready", "tool_count": 2}]}`,
	})
	payload := runtimeContractRequest(t, mux, http.MethodGet, "/v1/runtime/nodes/"+nodeID+"/mcp")
	if payload["ok"] != true || payload["count"] != float64(2) {
		t.Fatalf("MCP 列表响应错误: %v", payload)
	}
	servers, _ := payload["servers"].([]any)
	standalone, _ := servers[0].(map[string]any)
	pluginMCP, _ := servers[1].(map[string]any)
	if standalone["name"] != "github" || standalone["source_type"] != "standalone" || standalone["enabled"] != true || standalone["tool_count"] != float64(12) {
		t.Fatalf("standalone MCP 摘要字段错误: %v", standalone)
	}
	if pluginMCP["name"] != "plugin.context7.context7" || pluginMCP["source_type"] != "plugin" || pluginMCP["plugin_name"] != "context7" {
		t.Fatalf("Plugin MCP 摘要字段错误: %v", pluginMCP)
	}
}

func TestRuntimeTaskDetailThroughBridgeDerivesProgress(t *testing.T) {
	_, mux, nodeID := runtimeContractTestServer(t, map[string]string{
		"/internal/runtime/tasks/task_01": `{"ok": true, "source": "agentdock-api", "action": "get", "task": {
			"id": "task_01", "title": "修复登录超时", "goal": "登录不再超时", "status": "active", "phase": "execute",
			"steps": [
				{"id": "s1", "title": "复现问题", "phase": "execute", "status": "completed", "updated_at": "2026-09-13T07:30:00Z"},
				{"id": "s2", "title": "修复配置", "phase": "execute", "status": "in_progress", "updated_at": "2026-09-13T08:00:00Z"}],
			"conditions": [], "events": [],
			"created_at": "2026-09-13T07:00:00Z", "updated_at": "2026-09-13T08:00:00Z"}}`,
	})
	payload := runtimeContractRequest(t, mux, http.MethodGet, "/v1/runtime/nodes/"+nodeID+"/tasks/task_01.json")
	task, _ := payload["task"].(map[string]any)
	if task == nil {
		t.Fatalf("task 缺失: %v", payload)
	}
	// 详情摘要的进度由完整步骤推导：1 个完成、当前步骤是进行中的 s2；review 回退 not_started。
	if task["completed_step_count"] != float64(1) || task["step_count"] != float64(2) || task["review_status"] != "not_started" {
		t.Fatalf("详情进度推导错误: %v", task)
	}
	if task["file_name"] != "task_01" || task["path"] != "agentdock-runtime-api" {
		t.Fatalf("详情派生字段错误: %v", task)
	}
	steps, _ := task["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("steps = %v", task["steps"])
	}
	if _, hasAttempts := task["attempts"]; hasAttempts {
		t.Fatalf("attempts 已随上游契约移除，不应再出现: %v", task)
	}
}

func TestRuntimeSkillsThroughBridgeExposeCurrentContentIdentity(t *testing.T) {
	_, mux, nodeID := runtimeContractTestServer(t, map[string]string{
		"/internal/runtime/skills": `{"action":"list","count":1,"source":"agentdock-api","skills":[{
			"skill":"demo","name":"demo","description":"Demo Skill",
			"skill_ref":"skill://managed/demo","source_type":"managed",
			"content_digest":"sha256:abc123","file_count":2}]}`,
	})
	payload := runtimeContractRequest(t, mux, http.MethodGet, "/v1/runtime/nodes/"+nodeID+"/skills")
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("Skill 列表响应错误: %v", payload)
	}
	item, _ := items[0].(map[string]any)
	if item["skill_ref"] != "skill://managed/demo" || item["source_type"] != "managed" ||
		item["content_digest"] != "sha256:abc123" {
		t.Fatalf("Skill 当前内容身份字段错误: %v", item)
	}
	for _, legacy := range []string{"active_version", "versions", "channels"} {
		if _, ok := item[legacy]; ok {
			t.Fatalf("Skill UI API 不应保留旧版本字段 %q: %v", legacy, item)
		}
	}
}

func TestRuntimeSkillFileThroughBridgeRejectsWrongType(t *testing.T) {
	_, mux, nodeID := runtimeContractTestServer(t, map[string]string{
		"/internal/runtime/skills/demo/files/SKILL.md": `{"action": "file", "ok": true,
			"file": {"path": "SKILL.md", "kind": "doc", "size_bytes": "12",
			         "updated_at": "2026-09-10T00:00:00Z", "content": "hello", "truncated": false}}`,
	})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/runtime/nodes/%s/skills/agentdock-api/demo/files/SKILL.md", nodeID), nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "file.size_bytes") {
		t.Fatalf("错误缺少字段路径: %s", response.Body.String())
	}
}

func TestMCPOAuthCallbackRelaysToTargetNodeWithoutWebSession(t *testing.T) {
	server, _, nodeID := runtimeContractTestServer(t, map[string]string{
		"/internal/runtime/mcp/oauth/callback": `{"accepted": true}`,
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet,
		"/oauth/mcp/nodes/"+nodeID+"/callback?code=code-1&state=state-1&iss=https%3A%2F%2Fissuer.example.test", nil)
	request.SetPathValue("nodeID", nodeID)

	server.mcpOAuthCallback(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("callback security headers = %#v", response.Header())
	}
	if strings.Contains(response.Body.String(), "code-1") || strings.Contains(response.Body.String(), "state-1") {
		t.Fatalf("callback response echoed one-time credentials: %s", response.Body.String())
	}
}

func TestRuntimeMCPAuthorizeAlwaysUsesNexusCallback(t *testing.T) {
	server, _, nodeID := runtimeContractTestServer(t, map[string]string{
		"/internal/runtime/mcp": `{"action":"authorize","name":"cloudflare","authorization_url":"https://auth.example.test/authorize","callback_id":"nexus"}`,
	})
	mux := http.NewServeMux()
	server.registerRuntimeMCPRoutes(mux, func(next http.HandlerFunc) http.HandlerFunc { return next })
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/runtime/nodes/"+nodeID+"/mcp/cloudflare/authorize", nil)
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["callback_id"] != "nexus" || payload["authorization_url"] == "" {
		t.Fatalf("authorize payload = %#v", payload)
	}
}
