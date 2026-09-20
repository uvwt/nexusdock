package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/recall"
)

func TestRuntimeNodeStatusReportsHealthLogicalCapabilitiesAndLimits(t *testing.T) {
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
	readDescriptor := agentdock.ToolDescriptor{
		Name:        "read_file",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
	}
	target := pairHTTPTestNode(t, nodes, "device_runtime_status", "DockStatus", "0.8.3", readDescriptor)
	execDescriptor := agentdock.ToolDescriptor{
		Name:        "exec_command",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"cmd": map[string]any{"type": "string"}}},
	}
	target, err = nodes.UpdateHello(t.Context(), target.ID, agentdock.Hello{
		DeviceID: target.DeviceID, Version: "0.8.3", ProtocolVersion: agentdock.ConnectionProtocolVersion,
		OS: "windows", Arch: "amd64",
		Capabilities: []string{"read_file", "exec_command"},
		Tools:        []agentdock.ToolDescriptor{readDescriptor, execDescriptor},
		UIResources:  []agentdock.UIResourceCapability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	recallStore, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(config.Config{
		ToolConcurrencyGlobal: 7, ToolConcurrencyPerNode: 2,
		ToolConcurrencyPerWorkspace: 1, ToolQueueTimeoutSeconds: 9,
	}, recallStore, slog.Default(), WithSystemDatabase(db), WithAgentDockNodes(nodes, agentdock.NewHub(nodes)))

	req := httptest.NewRequest(http.MethodGet, "/v1/runtime/nodes/"+target.ID+"/status", nil)
	req.SetPathValue("nodeID", target.ID)
	res := httptest.NewRecorder()
	server.runtimeNodeStatus(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	health := body["health"].(map[string]any)
	if health["state"] != "offline" || health["compatible"] != true {
		t.Fatalf("health=%#v", health)
	}
	encoded := res.Body.String()
	for _, expected := range []string{
		`"capability":"filesystem.read"`,
		`"capability":"shell.exec"`,
		`"global":7`,
		`"per_node":2`,
		`"per_workspace":1`,
		`"queue_timeout_seconds":9`,
		`"explicit_node_required":true`,
		`"automatic_node_selection":false`,
	} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("status body missing %s: %s", expected, encoded)
		}
	}
	workspaceCapabilities, _ := body["workspace_capabilities"].([]any)
	for _, item := range workspaceCapabilities {
		capability := item.(map[string]any)["capability"]
		if capability == "shell.exec" {
			t.Fatalf("workspace capabilities exposed unsafe shell: %#v", workspaceCapabilities)
		}
	}
}
