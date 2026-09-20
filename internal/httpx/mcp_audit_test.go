package httpx

import (
	"context"
	"testing"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/audit"
	"github.com/uvwt/nexusdock/internal/core"
)

func TestCallNodeToolRecordsAppendOnlyAuditEvent(t *testing.T) {
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
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
	descriptor := agentdock.ToolDescriptor{
		Name: "read_file",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
	}
	target := pairHTTPTestNode(t, store, "device_audit_trace", "DockAudit", "0.8.3", descriptor)
	auditService := audit.NewService(db)
	server := newGatewayTestServer(t, store)
	server.auditService = auditService
	server.registerNodeTools(target, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})

	ctx := withMCPActorContext(context.Background(), core.Actor{Type: core.ActorUser, ID: "user-audit"})
	result, err := server.callNodeTool(ctx, "read_file", map[string]any{
		"node_id": target.ID,
		"path":    `D:\website\ServoCylMotion\DESIGN.md`,
		"content": "must-not-be-audited",
		"token":   "must-not-be-audited",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("offline node should return MCP error: %#v", result)
	}

	events, err := auditService.List(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events=%#v", events)
	}
	event := events[0]
	if event.Actor.Type != core.ActorUser || event.Actor.ID != "user-audit" {
		t.Fatalf("actor=%#v", event.Actor)
	}
	if event.Action != "agentdock.tool.invoke" || event.ObjectType != "agentdock_tool" || event.Result != "failed" || event.Risk != "low" {
		t.Fatalf("event=%#v", event)
	}
	if event.Metadata["path"] != `D:\website\ServoCylMotion\DESIGN.md` {
		t.Fatalf("metadata=%#v", event.Metadata)
	}
	for _, value := range event.Metadata {
		if value == "must-not-be-audited" {
			t.Fatalf("sensitive value leaked to audit metadata: %#v", event.Metadata)
		}
	}
}
