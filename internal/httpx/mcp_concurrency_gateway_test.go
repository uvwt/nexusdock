package httpx

import (
	"strings"
	"testing"
	"time"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/runtimecontrol"
)

func TestCallNodeToolHonorsRuntimeQueueBeforeAgentDockInvoke(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := agentdock.ToolDescriptor{
		Name: "read_file",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
	}
	target := pairHTTPTestNode(t, store, "device_queue_gate", "DockQueue", "0.8.3", descriptor)
	server := newGatewayTestServer(t, store)
	server.toolGate = runtimecontrol.New(runtimecontrol.Limits{
		Global: 1, PerNode: 1, PerWorkspace: 1, QueueTimeout: 30 * time.Millisecond,
	})
	server.registerNodeTools(target, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})

	release, _, err := server.toolGate.Acquire(t.Context(), runtimecontrol.Request{NodeID: "occupied"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	result, err := server.callNodeTool(t.Context(), "read_file", map[string]any{
		"node_id": target.ID,
		"path":    "/tmp/file.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("queue saturation should return MCP error: %#v", result)
	}
	details, _ := result.StructuredContent.(map[string]any)
	if !strings.Contains(stringValue(details["error"]), runtimecontrol.ErrQueueTimeout.Error()) {
		t.Fatalf("queue error=%#v", details)
	}
}
