package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/nexusdock/internal/agentdock"
)

func canonicalWorkspaceContextDescriptor(t *testing.T) agentdock.ToolDescriptor {
	t.Helper()
	input, ok := mcpcontract.InputSchema(mcpcontract.ToolWorkspaceContext)
	if !ok {
		t.Fatal("workspace_context direct input contract missing")
	}
	output, ok := mcpcontract.OutputSchema(mcpcontract.ToolWorkspaceContext)
	if !ok {
		t.Fatal("workspace_context output contract missing")
	}
	return agentdock.ToolDescriptor{
		Name:         mcpcontract.ToolWorkspaceContext,
		Title:        "Workspace context",
		InputSchema:  input,
		OutputSchema: output,
		Meta: map[string]any{
			"ui": map[string]any{"resourceUri": protocol.WorkspaceUIResourceURI},
		},
	}
}

func TestWorkspaceContextRoutesToSelectedNodeWithoutForwardingNodeID(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := canonicalWorkspaceContextDescriptor(t)
	node := pairHTTPTestNode(t, store, "device_workspace_context", "DockMini", "2.0.0", descriptor)
	server := newGatewayTestServer(t, store)
	server.mcpAppsEnabledState = true
	server.agentDockHub.SetHelloHandler(server.registerNodeTools)
	hub := server.agentDockHub

	connected := make(chan struct{})
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, node.ID, ""); err != nil {
			t.Errorf("accept workspace node: %v", err)
			return
		}
		close(connected)
	}))
	t.Cleanup(bridge.Close)

	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(bridge.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if err := socket.WriteJSON(protocol.Message{
		Type: protocol.MessageNodeHello, ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Hello: &protocol.Hello{
			DeviceID: node.DeviceID, Version: node.Version, ProtocolVersion: agentdock.ConnectionProtocolVersion,
			Capabilities: []string{descriptor.Name},
			Tools:        []protocol.ToolDescriptor{descriptor},
			UIResources: []protocol.UIResourceCapability{{
				URI: protocol.WorkspaceUIResourceURI, Contract: protocol.WorkspaceUIContract, MIMEType: protocol.MCPAppMIMEType,
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var ready protocol.Message
	if err := socket.ReadJSON(&ready); err != nil || ready.Type != protocol.MessageNodeReady {
		t.Fatalf("ready=%#v err=%v", ready, err)
	}
	<-connected

	published, ok := server.publishedToolBridge.Published(mcpcontract.ToolWorkspaceContext)
	if !ok {
		t.Fatal("workspace_context was not published through the node tool bridge")
	}
	publishedTool := nodeMCPTool(published.Descriptor)
	properties := publishedTool.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := properties["node_id"]; !ok {
		t.Fatalf("workspace_context published input missing node_id: %#v", publishedTool.InputSchema)
	}
	if _, ok := properties["workdir"]; !ok {
		t.Fatalf("workspace_context published input lost AgentDock workdir: %#v", publishedTool.InputSchema)
	}
	ui, ok := publishedTool.Meta["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != protocol.WorkspaceUIResourceURI {
		t.Fatalf("workspace_context Apps UI binding was not relayed: %#v", publishedTool.Meta)
	}
	resources, err := server.publishedMCPAppResourceURIs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resources[protocol.WorkspaceUIResourceURI]; !ok {
		t.Fatalf("Workspace MCP App resource was not published: %#v", resources)
	}
	publishedHash, err := agentdock.ToolContractHash(published.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	canonicalHash, err := agentdock.ToolContractHash(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if publishedHash != canonicalHash {
		t.Fatalf("workspace_context published contract drifted: got=%s want=%s", publishedHash, canonicalHash)
	}

	wantStructured := map[string]any{
		"workdir": "/repo/service", "workspace_root": "/repo",
		"instructions": []any{},
		"workspace_skills": []any{map[string]any{
			"name": "demo", "description": "Workspace demo",
			"file":        "skill://workspace/ws-test/demo/SKILL.md",
			"skill_ref":   "skill://workspace/ws-test/demo",
			"source_type": "workspace",
		}},
		"warnings": []any{},
	}
	serveDone := make(chan error, 1)
	go func() {
		var invoke protocol.Message
		if err := socket.ReadJSON(&invoke); err != nil {
			serveDone <- err
			return
		}
		if invoke.Operation != protocol.OperationToolCall {
			serveDone <- &unexpectedOperationError{got: invoke.Operation}
			return
		}
		var call struct {
			Tool      string         `json:"tool"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(invoke.Arguments, &call); err != nil {
			serveDone <- err
			return
		}
		if call.Tool != mcpcontract.ToolWorkspaceContext || !reflect.DeepEqual(call.Arguments, map[string]any{"workdir": "/repo/service"}) {
			serveDone <- &workspaceContextInvokeError{tool: call.Tool, arguments: call.Arguments}
			return
		}
		result, err := json.Marshal(map[string]any{
			"isError": false, "structuredContent": wantStructured,
			"content": []map[string]any{{"type": "text", "text": "workspace context"}},
		})
		if err != nil {
			serveDone <- err
			return
		}
		serveDone <- socket.WriteJSON(protocol.Message{Type: protocol.MessageToolResult, RequestID: invoke.RequestID, Result: result})
	}()

	result, err := server.callNodeTool(t.Context(), mcpcontract.ToolWorkspaceContext, map[string]any{
		"node_id": node.ID, "workdir": "/repo/service",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !reflect.DeepEqual(result.StructuredContent, wantStructured) {
		t.Fatalf("workspace proxy result = %#v", result)
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceContextRejectsNodeOutsidePublishedContract(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	current := canonicalWorkspaceContextDescriptor(t)
	currentNode := pairHTTPTestNode(t, store, "device_workspace_context_current", "DockMini", "2.0.0", current)

	drifted := canonicalWorkspaceContextDescriptor(t)
	drifted.InputSchema["properties"].(map[string]any)["workdir"] = map[string]any{"type": "integer"}
	driftedNode := pairHTTPTestNode(t, store, "device_workspace_context_drift", "DockOld", "1.0.0", drifted)

	server := newGatewayTestServer(t, store)
	server.registerNodeTools(currentNode, agentdock.Hello{Tools: []agentdock.ToolDescriptor{current}})
	server.registerNodeTools(driftedNode, agentdock.Hello{Tools: []agentdock.ToolDescriptor{drifted}})

	result, err := server.callNodeTool(t.Context(), mcpcontract.ToolWorkspaceContext, map[string]any{"node_id": driftedNode.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("workspace_context accepted a node outside the published contract: %#v", result.StructuredContent)
	}
	structured, _ := result.StructuredContent.(map[string]any)
	if structured["code"] != "TOOL_CONTRACT_MISMATCH" {
		t.Fatalf("workspace_context contract mismatch is not actionable: %#v", structured)
	}
}

type workspaceContextInvokeError struct {
	tool      string
	arguments map[string]any
}

func (e *workspaceContextInvokeError) Error() string {
	return "unexpected workspace_context invoke: tool=" + e.tool
}
