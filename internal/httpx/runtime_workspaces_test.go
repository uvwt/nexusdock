package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/workspace"
)

func newWorkspaceHTTPHandler(t *testing.T) http.Handler {
	t.Helper()
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO agentdock_devices(
		id, device_id, name, enabled, created_at, updated_at
	) VALUES('node_test', 'device_12345678', 'Windows', 1, '2026-09-20T00:00:00Z', '2026-09-20T00:00:00Z')`); err != nil {
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
	recallStore, err := recall.NewStore(filepath.Join(t.TempDir(), "recall"))
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(config.Config{}, recallStore, slog.Default(),
		WithSystemDatabase(db),
		WithAgentDockNodes(nodes, agentdock.NewHub(nodes)),
		WithRuntimeWorkspaces(workspaces),
	).Handler()
}

func TestRuntimeWorkspaceCRUD(t *testing.T) {
	h := newWorkspaceHTTPHandler(t)
	create := doJSON(t, h, http.MethodPost, "/v1/runtime/workspaces", `{
		"id":"servocylmotion",
		"name":"ServoCylMotion",
		"node_id":"node_test",
		"project_root":"D:\\website\\ServoCylMotion",
		"domain":"servocylmotion.com",
		"allowed_mcp":["elementor-servocylmotion","novamira-servocylmotion-c"],
		"context_roots":["D:\\website\\ServoCylMotion"],
		"design_authorities":["DESIGN.md"],
		"route_authority":"website_link.md"
	}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}

	list := doJSON(t, h, http.MethodGet, "/v1/runtime/workspaces", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var listed struct {
		Items []workspace.Workspace `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != "servocylmotion" {
		t.Fatalf("list payload=%s", list.Body.String())
	}

	update := doJSON(t, h, http.MethodPatch, "/v1/runtime/workspaces/servocylmotion", `{"name":"Servo Cyl Motion"}`)
	if update.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}

	get := doJSON(t, h, http.MethodGet, "/v1/runtime/workspaces/servocylmotion", "")
	if get.Code != http.StatusOK || !json.Valid(get.Body.Bytes()) {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}

	del := doJSON(t, h, http.MethodDelete, "/v1/runtime/workspaces/servocylmotion", "")
	if del.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", del.Code, del.Body.String())
	}
	missing := doJSON(t, h, http.MethodGet, "/v1/runtime/workspaces/servocylmotion", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestRuntimeWorkspaceRejectsUnknownNode(t *testing.T) {
	h := newWorkspaceHTTPHandler(t)
	res := doJSON(t, h, http.MethodPost, "/v1/runtime/workspaces", `{
		"id":"bad-node","name":"Bad","node_id":"node_missing","project_root":"D:\\bad"
	}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
