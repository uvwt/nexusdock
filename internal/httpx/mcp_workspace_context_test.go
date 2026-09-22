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
	}
}

func TestWorkspaceContextRoutesToSelectedNodeWithoutForwardingNodeID(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := canonicalWorkspaceContextDescriptor(t)
	node := pairHTTPTestNode(t, store, "device_workspace_context", "DockMini", "2.0.0", descriptor)
	hub := agentdock.NewHub(store)

	connected := make(chan struct{})
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, node.ID); err != nil {
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
			Capabilities: []string{descriptor.Name}, Tools: []protocol.ToolDescriptor{descriptor}, UIResources: []protocol.UIResourceCapability{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var ready protocol.Message
	if err := socket.ReadJSON(&ready); err != nil || ready.Type != protocol.MessageNodeReady {
		t.Fatalf("ready=%#v err=%v", ready, err)
	}
	<-connected

	wantStructured := map[string]any{
		"workdir": "/repo/service", "workspace_root": "/repo",
		"instructions": []any{}, "workspace_skills": []any{}, "warnings": []any{},
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolWorkspaceContext, wantStructured)
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

	server := &Server{agentDock: store, agentDockHub: hub}
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

func TestWorkspaceContextRejectsNodeWithDriftedCanonicalContract(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := canonicalWorkspaceContextDescriptor(t)
	descriptor.InputSchema = map[string]any{
		"type": "object", "properties": map[string]any{"legacy": map[string]any{"type": "boolean"}},
		"required": []string{}, "additionalProperties": false,
	}
	node := pairHTTPTestNode(t, store, "device_workspace_context_drift", "DockOld", "1.0.0", descriptor)
	server := &Server{agentDock: store, agentDockHub: agentdock.NewHub(store)}

	result, err := server.callNodeTool(t.Context(), mcpcontract.ToolWorkspaceContext, map[string]any{"node_id": node.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("drifted canonical contract was accepted: %#v", result.StructuredContent)
	}
	structured, _ := result.StructuredContent.(map[string]any)
	message, _ := structured["error"].(string)
	if !strings.Contains(message, "canonical contract") {
		t.Fatalf("drift error is not actionable: %#v", structured)
	}
}

type workspaceContextInvokeError struct {
	tool      string
	arguments map[string]any
}

func (e *workspaceContextInvokeError) Error() string {
	return "unexpected workspace_context invoke: tool=" + e.tool
}
