package settings

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/uvwt/nexusdock/internal/core"
)

func TestMCPSettingsDefaultAndPersistence(t *testing.T) {
	db, err := core.OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "nexus.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}

	store, err := NewMCPStore(db, true)
	if err != nil {
		t.Fatal(err)
	}
	enabled, view, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !enabled || !view.MCPAppsEnabled || view.Persisted {
		t.Fatalf("unexpected default MCP settings: enabled=%v view=%#v", enabled, view)
	}

	store.now = func() time.Time { return time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC) }
	view, err = store.Update(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if view.MCPAppsEnabled || !view.Persisted || view.UpdatedAt == "" {
		t.Fatalf("unexpected persisted MCP settings: %#v", view)
	}

	enabled, view, err = store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if enabled || view.MCPAppsEnabled || !view.Persisted {
		t.Fatalf("persisted MCP settings were not restored: enabled=%v view=%#v", enabled, view)
	}
}
