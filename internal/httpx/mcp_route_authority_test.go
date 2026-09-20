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
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/workspace"
)

func TestWorkspaceRouteAuthorityRejectsUnknownRouteBeforeTargetInvoke(t *testing.T) {
	server, socket, target := newRouteAuthorityGateway(t)
	defer socket.Close()

	done := make(chan error, 1)
	go func() {
		var invoke protocol.Message
		if err := socket.ReadJSON(&invoke); err != nil {
			done <- err
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(invoke.Arguments, &payload); err != nil {
			done <- err
			return
		}
		if payload["tool"] != "read_file" {
			done <- &testMessageError{message: "first invoke was not read_file"}
			return
		}
		done <- socket.WriteJSON(protocol.Message{
			Type: protocol.MessageToolResult, RequestID: invoke.RequestID,
			Result: []byte(`{"isError":false,"structuredContent":{"content":"https://servocylmotion.com/products/\nhttps://servocylmotion.com/products/electric-cylinders/\n"}}`),
		})
	}()

	result, err := server.callNodeTool(t.Context(), "mcp_tool_call", map[string]any{
		"node_id": target.ID, "workspace_id": "servocylmotion",
		"name":      "elementor-servocylmotion:update_page",
		"arguments": map[string]any{"page_url": "https://servocylmotion.com/invented-route/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("unknown route was not denied: %#v", result)
	}
	details, _ := result.StructuredContent.(map[string]any)
	if !strings.Contains(stringValue(details["error"]), "Workspace route_authority policy denied") {
		t.Fatalf("unexpected route authority error: %#v", details)
	}

	_ = socket.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var unexpected protocol.Message
	if err := socket.ReadJSON(&unexpected); err == nil {
		t.Fatalf("target tool was invoked after route denial: %#v", unexpected)
	}
}

func TestWorkspaceRouteAuthorityAllowsApprovedRouteAndStripsWorkspaceMetadata(t *testing.T) {
	server, socket, target := newRouteAuthorityGateway(t)
	defer socket.Close()

	done := make(chan error, 1)
	go func() {
		var authorityInvoke protocol.Message
		if err := socket.ReadJSON(&authorityInvoke); err != nil {
			done <- err
			return
		}
		if err := socket.WriteJSON(protocol.Message{
			Type: protocol.MessageToolResult, RequestID: authorityInvoke.RequestID,
			Result: []byte(`{"isError":false,"structuredContent":{"content":"https://servocylmotion.com/products/\n"}}`),
		}); err != nil {
			done <- err
			return
		}

		var targetInvoke protocol.Message
		if err := socket.ReadJSON(&targetInvoke); err != nil {
			done <- err
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(targetInvoke.Arguments, &payload); err != nil {
			done <- err
			return
		}
		if payload["tool"] != "mcp_tool_call" {
			done <- &testMessageError{message: "second invoke was not target mcp_tool_call"}
			return
		}
		args, _ := payload["arguments"].(map[string]any)
		if _, exists := args["workspace_id"]; exists {
			done <- &testMessageError{message: "workspace_id leaked to AgentDock"}
			return
		}
		if _, exists := args["node_id"]; exists {
			done <- &testMessageError{message: "node_id leaked to AgentDock"}
			return
		}
		done <- socket.WriteJSON(protocol.Message{
			Type: protocol.MessageToolResult, RequestID: targetInvoke.RequestID,
			Result: []byte(`{"isError":false,"structuredContent":{"ok":true},"content":[{"type":"text","text":"ok"}]}`),
		})
	}()

	result, err := server.callNodeTool(t.Context(), "mcp_tool_call", map[string]any{
		"node_id": target.ID, "workspace_id": "servocylmotion",
		"name":      "elementor-servocylmotion:update_page",
		"arguments": map[string]any{"page_url": "https://servocylmotion.com/products/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("approved route failed: %#v", result.StructuredContent)
	}
}

type testMessageError struct{ message string }

func (e *testMessageError) Error() string { return e.message }

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func newRouteAuthorityGateway(t *testing.T) (*Server, *websocket.Conn, agentdock.Node) {
	t.Helper()
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	nodes, err := agentdock.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := workspace.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	targetDescriptor := agentdock.ToolDescriptor{
		Name: "mcp_tool_call",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":      map[string]any{"type": "string"},
				"arguments": map[string]any{"type": "object"},
			},
		},
	}
	readDescriptor := agentdock.ToolDescriptor{
		Name:        "read_file",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
	}
	target := pairHTTPTestNode(t, nodes, "device_route_authority", "DockWin", "0.8.3", targetDescriptor)
	if _, err := workspaces.Create(t.Context(), workspace.CreateInput{
		ID: "servocylmotion", Name: "ServoCylMotion", NodeID: target.ID,
		ProjectRoot: `D:\website\ServoCylMotion`, Domain: "servocylmotion.com",
		AllowedMCP: []string{"elementor-servocylmotion"}, RouteAuthority: "website_link.md",
	}); err != nil {
		t.Fatal(err)
	}

	server := newGatewayTestServer(t, nodes)
	server.workspaces = workspaces
	server.registerNodeTools(target, agentdock.Hello{Tools: []agentdock.ToolDescriptor{targetDescriptor}})
	hub := server.agentDockHub
	connected := make(chan struct{})
	websocketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, target.ID); err != nil {
			t.Errorf("accept route authority node: %v", err)
			return
		}
		close(connected)
	}))
	t.Cleanup(websocketServer.Close)

	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(websocketServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.WriteJSON(protocol.Message{
		Type: protocol.MessageNodeHello, ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Hello: &protocol.Hello{
			DeviceID: target.DeviceID, Version: "0.8.3", ProtocolVersion: agentdock.ConnectionProtocolVersion,
			OS: "windows", Arch: "amd64",
			Capabilities: []string{"mcp_tool_call", "read_file"},
			Tools:        []protocol.ToolDescriptor{targetDescriptor, readDescriptor},
			UIResources:  []protocol.UIResourceCapability{},
		},
	}); err != nil {
		socket.Close()
		t.Fatal(err)
	}
	var ready protocol.Message
	if err := socket.ReadJSON(&ready); err != nil {
		socket.Close()
		t.Fatal(err)
	}
	if ready.Type != protocol.MessageNodeReady {
		socket.Close()
		t.Fatalf("ready=%#v", ready)
	}
	<-connected
	updated, err := nodes.Get(t.Context(), target.ID)
	if err != nil {
		socket.Close()
		t.Fatal(err)
	}
	return server, socket, updated
}
