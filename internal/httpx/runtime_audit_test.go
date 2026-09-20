package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/audit"
	"github.com/uvwt/nexusdock/internal/core"
)

func TestRuntimeAuditListFiltersWorkspaceRiskAndResult(t *testing.T) {
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	service := audit.NewService(db)
	for _, event := range []audit.Event{
		{
			Actor:  core.Actor{Type: core.ActorUser, ID: "user-1"},
			Action: "agentdock.tool.invoke", ObjectType: "agentdock_tool", ObjectID: "node-1:file_edit",
			Result: "failed", Risk: "high", Metadata: map[string]any{"workspace_id": "site-a", "node_id": "node-1"},
		},
		{
			Actor:  core.Actor{Type: core.ActorUser, ID: "user-1"},
			Action: "agentdock.tool.invoke", ObjectType: "agentdock_tool", ObjectID: "node-2:read_file",
			Result: "succeeded", Risk: "low", Metadata: map[string]any{"workspace_id": "site-b", "node_id": "node-2"},
		},
	} {
		if _, err := service.Record(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{auditService: service}
	req := httptest.NewRequest(http.MethodGet, "/v1/runtime/audit?workspace_id=site-a&risk=high&result=failed&limit=10", nil)
	res := httptest.NewRecorder()
	server.runtimeAuditList(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !containsAll(body, `"count":1`, `"workspace_id":"site-a"`, `"risk":"high"`, `"result":"failed"`) {
		t.Fatalf("unexpected body=%s", body)
	}
	if containsAll(body, `"workspace_id":"site-b"`) {
		t.Fatalf("filter leaked unrelated event: %s", body)
	}
}

func TestRuntimeAuditListRejectsInvalidRisk(t *testing.T) {
	server := &Server{auditService: &auditStub{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/runtime/audit?risk=critical", nil)
	res := httptest.NewRecorder()
	server.runtimeAuditList(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

type auditStub struct{}

func (*auditStub) Record(_ context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
func (*auditStub) List(_ context.Context, _ int) ([]audit.Event, error) { return nil, nil }

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
