package workspace

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/uvwt/nexusdock/internal/core"
)

func newWorkspaceStore(t *testing.T) *Store {
	t.Helper()
	db, err := core.OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "nexus.db"), 1)
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
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStoreCRUDNormalizesWorkspace(t *testing.T) {
	store := newWorkspaceStore(t)
	created, err := store.Create(t.Context(), CreateInput{
		ID: "ServoCylMotion", Name: " ServoCylMotion ", NodeID: "node_test",
		ProjectRoot:       `D:\website\ServoCylMotion`,
		Domain:            "https://ServoCylMotion.com/",
		AllowedMCP:        []string{"elementor-servocylmotion", "elementor-servocylmotion", "novamira-servocylmotion-c"},
		ContextRoots:      []string{`D:\website\ServoCylMotion`, `D:\website\ServoCylMotion`},
		DesignAuthorities: []string{"DESIGN.md", "website_link.md"},
		RouteAuthority:    " website_link.md ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "servocylmotion" || created.Domain != "servocylmotion.com" {
		t.Fatalf("normalized workspace = %#v", created)
	}
	if !reflect.DeepEqual(created.AllowedMCP, []string{"elementor-servocylmotion", "novamira-servocylmotion-c"}) {
		t.Fatalf("allowed mcp = %#v", created.AllowedMCP)
	}

	name := "Servo Cyl Motion"
	allowed := []string{"novamira-servocylmotion-c"}
	updated, err := store.Update(t.Context(), created.ID, UpdateInput{Name: &name, AllowedMCP: &allowed})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || !reflect.DeepEqual(updated.AllowedMCP, allowed) {
		t.Fatalf("updated = %#v", updated)
	}
	items, err := store.List(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("list=%#v err=%v", items, err)
	}
	if err := store.Delete(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted err=%v", err)
	}
}

func TestStoreRejectsInvalidDomainAndDuplicateID(t *testing.T) {
	store := newWorkspaceStore(t)
	base := CreateInput{ID: "demo-site", Name: "Demo", NodeID: "node_test", ProjectRoot: `D:\demo`}
	if _, err := store.Create(t.Context(), CreateInput{
		ID: "bad-domain", Name: "Bad", NodeID: "node_test", ProjectRoot: `D:\bad`, Domain: "https://example.com/path",
	}); err == nil {
		t.Fatal("domain with path was accepted")
	}
	if _, err := store.Create(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(t.Context(), base); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate err=%v", err)
	}
}
