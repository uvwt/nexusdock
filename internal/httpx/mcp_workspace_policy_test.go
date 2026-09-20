package httpx

import (
	"fmt"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/workspace"
)

func TestCallNodeToolRejectsWorkspaceBoundaryBeforeInvoke(t *testing.T) {
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
	descriptor := agentdock.ToolDescriptor{
		Name: "mcp_tool_call",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":      map[string]any{"type": "string"},
				"arguments": map[string]any{"type": "object"},
			},
		},
	}
	target := pairHTTPTestNode(t, nodes, "device_workspace_policy", "DockWin", "0.8.3", descriptor)
	if _, err := workspaces.Create(t.Context(), workspace.CreateInput{
		ID: "servocylmotion", Name: "ServoCylMotion", NodeID: target.ID,
		ProjectRoot: `D:\website\ServoCylMotion`, Domain: "servocylmotion.com",
		AllowedMCP: []string{"elementor-servocylmotion"},
	}); err != nil {
		t.Fatal(err)
	}
	server := newGatewayTestServer(t, nodes)
	server.workspaces = workspaces
	server.registerNodeTools(target, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})

	result, err := server.callNodeTool(t.Context(), "mcp_tool_call", map[string]any{
		"node_id": target.ID, "workspace_id": "servocylmotion",
		"name":      "novamira-actulift-com:update_page",
		"arguments": map[string]any{"url": "https://actulift.com/test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("cross-workspace call was not denied: %#v", result)
	}
	details, _ := result.StructuredContent.(map[string]any)
	if !strings.Contains(fmt.Sprint(details["error"]), "Workspace mcp policy denied") {
		t.Fatalf("unexpected policy error: %#v", details)
	}
}

func TestCallNodeToolRejectsWorkspaceBoundToDifferentNode(t *testing.T) {
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
	descriptor := agentdock.ToolDescriptor{
		Name: "read_file",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
	}
	first := pairHTTPTestNode(t, nodes, "device_workspace_first", "DockA", "0.8.3", descriptor)
	second := pairHTTPTestNode(t, nodes, "device_workspace_second", "DockB", "0.8.3", descriptor)
	if _, err := workspaces.Create(t.Context(), workspace.CreateInput{
		ID: "servocylmotion", Name: "ServoCylMotion", NodeID: first.ID,
		ProjectRoot: `D:\website\ServoCylMotion`,
	}); err != nil {
		t.Fatal(err)
	}
	server := newGatewayTestServer(t, nodes)
	server.workspaces = workspaces
	server.registerNodeTools(first, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})
	server.registerNodeTools(second, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})

	result, err := server.callNodeTool(t.Context(), "read_file", map[string]any{
		"node_id": second.ID, "workspace_id": "servocylmotion",
		"path": `D:\website\ServoCylMotion\DESIGN.md`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("cross-node workspace call was not denied: %#v", result)
	}
	details, _ := result.StructuredContent.(map[string]any)
	if !strings.Contains(fmt.Sprint(details["error"]), "Workspace node policy denied") {
		t.Fatalf("unexpected node policy error: %#v", details)
	}
}
