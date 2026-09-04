package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/recall"
)

func TestAgentDockContextIsCentralToolWithoutNodeID(t *testing.T) {
	for _, tool := range nexusToolDefinitions() {
		if tool.Name != agentDockContextToolName {
			continue
		}
		input := tool.InputSchema.(map[string]any)
		properties := input["properties"].(map[string]any)
		if _, exists := properties["node_id"]; exists {
			t.Fatalf("central context input must not expose node_id: %#v", input)
		}
		if tool.Title != "AgentDock fleet context" || !strings.Contains(tool.Description, "Nexus-owned shared") {
			t.Fatalf("central context presentation = title %q description %q", tool.Title, tool.Description)
		}
		output := tool.OutputSchema.(map[string]any)
		outputProperties := output["properties"].(map[string]any)
		for _, field := range []string{"nodes", "shared"} {
			if _, exists := outputProperties[field]; !exists {
				t.Fatalf("fleet output schema missing %s: %#v", field, output)
			}
		}
		nodesSchema := outputProperties["nodes"].(map[string]any)
		nodeSchema := nodesSchema["items"].(map[string]any)
		nodeProperties := nodeSchema["properties"].(map[string]any)
		if _, exists := nodeProperties["capabilities"]; !exists {
			t.Fatalf("fleet node schema missing capabilities: %#v", nodeSchema)
		}
		return
	}
	t.Fatal("central agentdock_context tool is missing")
}

func TestFleetSharedContextComesDirectlyFromNexus(t *testing.T) {
	dataDir := t.TempDir()
	store, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(recall.WriteRequest{Path: "profile.md", Content: "# Profile\n\nNexus-owned memory.\n", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: config.Config{NexusDataDir: dataDir}, store: store}
	template := testWorkflowTemplate("central-context", "1.0.0")
	if _, err := server.publishWorkflowTemplateValue(template); err != nil {
		t.Fatal(err)
	}

	shared := server.buildFleetAgentDockSharedContext()
	if len(shared.WorkflowTemplates) != 1 || shared.WorkflowTemplates[0].Name != template.ID {
		t.Fatalf("workflow templates = %#v", shared.WorkflowTemplates)
	}
	if shared.Recall == nil || !shared.Recall.Enabled || len(shared.Recall.Items) == 0 || shared.Recall.Items[0].Name != "profile.md" {
		t.Fatalf("recall = %#v", shared.Recall)
	}
	if len(shared.Rules) == 0 || !strings.Contains(strings.Join(shared.Rules, "\n"), "workflow_template_manage") {
		t.Fatalf("shared rules = %#v", shared.Rules)
	}

	capabilities := deviceNodeCapabilities([]string{"exec_command", agentDockContextToolName, "workflow_template_manage", "recall_search", "exec_command"})
	if !containsString(capabilities, "exec_command") || containsString(capabilities, agentDockContextToolName) || containsString(capabilities, "workflow_template_manage") || containsString(capabilities, "recall_search") {
		t.Fatalf("device capabilities = %#v", capabilities)
	}
}

func TestCallFleetAgentDockContextAggregatesOnlineAndOfflineNodes(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := fleetContextTestDescriptor()
	online := pairHTTPTestNode(t, store, "device_context_online", "DockMini", "2.0.0", descriptor)
	var err error
	online, err = store.UpdateHello(t.Context(), online.ID, agentdock.Hello{
		DeviceID: online.DeviceID, Version: online.Version, ProtocolVersion: agentdock.ConnectionProtocolVersion,
		OS: "darwin", Arch: "arm64", Capabilities: []string{descriptor.Name}, Tools: []agentdock.ToolDescriptor{descriptor}, UIResources: []agentdock.UIResourceCapability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	offline := pairHTTPTestNode(t, store, "device_context_offline", "DockWin", "2.0.0", descriptor)
	disabled := pairHTTPTestNode(t, store, "device_context_disabled", "DockAir", "2.0.0", descriptor)
	enabled := false
	if _, err := store.Update(t.Context(), disabled.ID, agentdock.UpdateInput{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}

	hub := agentdock.NewHub(store)
	// 故意注入与 Hello 冲突的 runtime，证明 Nexus 节点事实不会被 provider context 覆盖。
	connectFleetContextTestNode(t, hub, online, descriptor, map[string]any{
		"runtime": map[string]any{
			"version": "9.9.9", "os": "linux", "arch": "amd64",
			"agentdock_home": "/wrong", "agentdock_default_dir": "/wrong", "default_cwd": ".", "path_model": "host",
		},
		"skills": []any{map[string]any{"name": "desktop", "description": "Desktop", "file": "skill://desktop/SKILL.md"}},
		"common_skills": map[string]any{
			"root": "/Users/xx/.agents/skills", "total": 1, "truncated": false,
			"items": []any{map[string]any{"name": "personal-dev-guard", "description": "Development guard", "file": "/Users/xx/.agents/skills/personal-dev-guard/SKILL.md"}},
		},
		"dynamic_mcp":        []any{map[string]any{"name": "github", "description": "GitHub"}},
		"workflow_templates": []any{map[string]any{"name": "deploy", "description": "Deploy"}},
		"recall":             map[string]any{"enabled": true, "items": []any{map[string]any{"name": "profile.md", "description": "Profile"}}},
		"rules":              []any{"rule-a", "rule-b"},
	})
	recallStore, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg: config.Config{NexusDataDir: t.TempDir()}, store: recallStore,
		agentDock: store, agentDockHub: hub,
	}

	result, err := server.callFleetAgentDockContext(t.Context())
	if err != nil {
		t.Fatalf("fleet context result=%#v err=%v", result, err)
	}
	assertCentralToolResultMatchesOutputSchema(t, "agentdock_context", result)
	var fleet fleetAgentDockContext
	if err := decodeMap(result, &fleet); err != nil {
		t.Fatal(err)
	}
	if len(fleet.Nodes) != 2 {
		t.Fatalf("enabled fleet nodes = %#v", fleet.Nodes)
	}
	if fleet.Nodes[0].Name != "DockMini" || !fleet.Nodes[0].Online || containsString(fleet.Nodes[0].Capabilities, descriptor.Name) || fleet.Nodes[0].Context == nil || len(fleet.Nodes[0].Context.Skills) != 1 {
		t.Fatalf("online node context = %#v", fleet.Nodes[0])
	}
	if fleet.Nodes[0].Context.CommonSkills == nil || fleet.Nodes[0].Context.CommonSkills.Total != 1 || len(fleet.Nodes[0].Context.CommonSkills.Items) != 1 || fleet.Nodes[0].Context.CommonSkills.Items[0].Name != "personal-dev-guard" {
		t.Fatalf("common Skill context was not forwarded: %#v", fleet.Nodes[0].Context.CommonSkills)
	}
	if fleet.Nodes[0].Version != "2.0.0" || fleet.Nodes[0].OS != "darwin" || fleet.Nodes[0].Arch != "arm64" {
		t.Fatalf("fleet node facts must come from Bridge Hello, got %#v", fleet.Nodes[0])
	}
	if fleet.Nodes[1].Name != offline.Name || fleet.Nodes[1].Online || containsString(fleet.Nodes[1].Capabilities, descriptor.Name) || fleet.Nodes[1].Error != agentdock.ErrNodeOffline.Error() || fleet.Nodes[1].Context != nil {
		t.Fatalf("offline node context = %#v", fleet.Nodes[1])
	}
	if fleet.Shared.Recall == nil || !fleet.Shared.Recall.Enabled || len(fleet.Shared.Rules) == 0 {
		t.Fatalf("Nexus-owned shared context = %#v", fleet.Shared)
	}
}

func TestFleetContextKeepsNexusSharedContextWhenAllNodesAreOffline(t *testing.T) {
	nodeStore := newHTTPTestAgentDockStore(t)
	descriptor := fleetContextTestDescriptor()
	pairHTTPTestNode(t, nodeStore, "device_context_offline_only", "DockWin", "2.0.0", descriptor)

	recallStore, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recallStore.Write(recall.WriteRequest{Path: "profile.md", Content: "# Profile\n\nShared memory survives offline nodes.\n", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg: config.Config{NexusDataDir: t.TempDir()}, store: recallStore,
		agentDock: nodeStore, agentDockHub: agentdock.NewHub(nodeStore),
	}
	if _, err := server.publishWorkflowTemplateValue(testWorkflowTemplate("offline.shared", "1.0.0")); err != nil {
		t.Fatal(err)
	}

	result, err := server.callFleetAgentDockContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var fleet fleetAgentDockContext
	if err := decodeMap(result, &fleet); err != nil {
		t.Fatal(err)
	}
	if len(fleet.Nodes) != 1 || fleet.Nodes[0].Online || fleet.Nodes[0].Error != agentdock.ErrNodeOffline.Error() {
		t.Fatalf("offline nodes = %#v", fleet.Nodes)
	}
	if len(fleet.Shared.WorkflowTemplates) != 1 || fleet.Shared.WorkflowTemplates[0].Name != "offline.shared" {
		t.Fatalf("shared workflows = %#v", fleet.Shared.WorkflowTemplates)
	}
	if fleet.Shared.Recall == nil || !fleet.Shared.Recall.Enabled || len(fleet.Shared.Recall.Items) == 0 {
		t.Fatalf("shared Recall = %#v", fleet.Shared.Recall)
	}
}

func TestLocalAgentDockContextRemovesSharedRecallRoutingRule(t *testing.T) {
	sharedRecallRule := nexusSharedAgentDockRules[len(nexusSharedAgentDockRules)-1]
	localOnlyRule := "local-only-rule"
	local := localAgentDockContext(agentDockContext{Rules: []string{sharedRecallRule, localOnlyRule}})
	if len(local.Rules) != 1 || local.Rules[0] != localOnlyRule {
		t.Fatalf("shared recall routing rule should be deduplicated from node context: %#v", local.Rules)
	}
}

func TestDecodeAgentDockContextRejectsLegacyMarkdownResult(t *testing.T) {
	_, err := decodeAgentDockContextResult(map[string]any{
		"isError":           false,
		"structuredContent": map[string]any{"context": "# AgentDock Context"},
	})
	if err == nil || !strings.Contains(err.Error(), "结构化契约") {
		t.Fatalf("legacy context should be rejected, got %v", err)
	}
}

func TestDecodeAgentDockContextAcceptsOlderNodeWithoutCommonSkills(t *testing.T) {
	decoded, err := decodeAgentDockContextResult(map[string]any{
		"isError": false,
		"structuredContent": map[string]any{
			"skills": []any{}, "dynamic_mcp": []any{}, "workflow_templates": []any{}, "rules": []any{},
		},
	})
	if err != nil {
		t.Fatalf("older node context should remain compatible: %v", err)
	}
	if decoded.CommonSkills != nil {
		t.Fatalf("older node should decode without synthetic common Skills: %#v", decoded.CommonSkills)
	}
}

func fleetContextTestDescriptor() agentdock.ToolDescriptor {
	inputSchema, ok := mcpcontract.InputSchema(agentDockContextToolName)
	if !ok {
		panic("agentdock_context input schema is missing")
	}
	return agentdock.ToolDescriptor{
		Name: agentDockContextToolName, Title: "AgentDock context", Description: "Return structured AgentDock bootstrap context.",
		InputSchema: inputSchema, OutputSchema: mcpcontract.LocalAgentDockContextOutputSchema(),
	}
}

func connectFleetContextTestNode(t *testing.T, hub *agentdock.Hub, node agentdock.Node, descriptor agentdock.ToolDescriptor, structured map[string]any) {
	t.Helper()
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, node.ID); err != nil {
			t.Errorf("accept context node: %v", err)
			return
		}
		close(connected)
	}))
	t.Cleanup(server.Close)

	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if err := socket.WriteJSON(map[string]any{
		"type": protocol.MessageNodeHello, "protocol_version": agentdock.ConnectionProtocolVersion,
		"hello": agentdock.Hello{
			DeviceID: node.DeviceID, Version: node.Version, ProtocolVersion: agentdock.ConnectionProtocolVersion,
			OS: node.OS, Arch: node.Arch, Capabilities: []string{descriptor.Name}, Tools: []agentdock.ToolDescriptor{descriptor}, UIResources: []agentdock.UIResourceCapability{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := socket.ReadJSON(&ready); err != nil || ready["type"] != protocol.MessageNodeReady {
		t.Fatalf("ready=%#v err=%v", ready, err)
	}
	<-connected

	go func() {
		var invoke struct {
			Type      string          `json:"type"`
			RequestID string          `json:"request_id"`
			Operation string          `json:"operation"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := socket.ReadJSON(&invoke); err != nil {
			return
		}
		if invoke.Operation != protocol.OperationContextLocal {
			t.Errorf("fleet context operation = %q", invoke.Operation)
			return
		}
		var arguments map[string]any
		if len(invoke.Arguments) > 0 {
			if err := json.Unmarshal(invoke.Arguments, &arguments); err != nil {
				t.Errorf("decode context arguments: %v", err)
				return
			}
		}
		if len(arguments) != 0 {
			t.Errorf("bridge-private context operation received public tool arguments: %#v", arguments)
		}
		_ = socket.WriteJSON(map[string]any{
			"type": protocol.MessageToolResult, "request_id": invoke.RequestID,
			"result": map[string]any{
				"isError": false, "structuredContent": structured,
				"content": []map[string]any{{"type": "text", "text": "context"}},
			},
		})
	}()
}

func TestFleetContextReturnsPartialResultWhenNodeContextTimesOut(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := fleetContextTestDescriptor()
	node := pairHTTPTestNode(t, store, "device_context_timeout", "DockSlow", "2.0.0", descriptor)
	hub := agentdock.NewHub(store)
	connectStalledFleetContextTestNode(t, hub, node, descriptor)
	recallStore, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg: config.Config{NexusDataDir: t.TempDir()}, store: recallStore,
		agentDock: store, agentDockHub: hub,
	}

	started := time.Now()
	result, err := server.callFleetAgentDockContextWithTimeout(t.Context(), 30*time.Millisecond)
	if err != nil {
		t.Fatalf("fleet context err=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("fleet context timeout took %v", elapsed)
	}
	var fleet fleetAgentDockContext
	if err := decodeMap(result, &fleet); err != nil {
		t.Fatal(err)
	}
	if len(fleet.Nodes) != 1 || !fleet.Nodes[0].Online || fleet.Nodes[0].Error != "context timeout" || fleet.Nodes[0].Context != nil {
		t.Fatalf("timeout node=%#v", fleet.Nodes)
	}
	if fleet.Shared.Rules == nil {
		t.Fatalf("partial result lost Nexus-owned shared context: %#v", fleet.Shared)
	}
}

func connectStalledFleetContextTestNode(t *testing.T, hub *agentdock.Hub, node agentdock.Node, descriptor agentdock.ToolDescriptor) {
	t.Helper()
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, node.ID); err != nil {
			t.Errorf("accept stalled context node: %v", err)
			return
		}
		close(connected)
	}))
	t.Cleanup(server.Close)

	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if err := socket.WriteJSON(map[string]any{
		"type": protocol.MessageNodeHello, "protocol_version": agentdock.ConnectionProtocolVersion,
		"hello": agentdock.Hello{
			DeviceID: node.DeviceID, Version: node.Version, ProtocolVersion: agentdock.ConnectionProtocolVersion,
			OS: node.OS, Arch: node.Arch, Capabilities: []string{descriptor.Name}, Tools: []agentdock.ToolDescriptor{descriptor}, UIResources: []agentdock.UIResourceCapability{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := socket.ReadJSON(&ready); err != nil || ready["type"] != protocol.MessageNodeReady {
		t.Fatalf("ready=%#v err=%v", ready, err)
	}
	<-connected

	go func() {
		var invoke map[string]any
		if err := socket.ReadJSON(&invoke); err != nil {
			return
		}
		if invoke["operation"] != protocol.OperationContextLocal {
			t.Errorf("stalled context operation=%#v", invoke)
			return
		}
		var cancel map[string]any
		if err := socket.ReadJSON(&cancel); err == nil && cancel["type"] != protocol.MessageToolCancel {
			t.Errorf("expected tool.cancel after timeout, got %#v", cancel)
		}
	}()
}
