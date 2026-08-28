package agentdock

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/core"
)

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := core.OpenSQLite(context.Background(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.EnsureSchema(context.Background(), db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store, db
}

func TestPairingCodeIsSingleUseAndStoresNoSecret(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	pairing, err := store.CreatePairingCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(ctx, PairInput{Code: pairing.Code, DeviceID: "device_12345678", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	if node.DeviceID != "device_12345678" || !node.Enabled {
		t.Fatalf("unexpected node: %#v", node)
	}
	if _, err := store.Pair(ctx, PairInput{Code: pairing.Code, DeviceID: "device_abcdefgh", Name: "Other"}); !errors.Is(err, ErrPairingCodeInvalid) {
		t.Fatalf("reused pairing code error = %v", err)
	}
	var legacyColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agentdock_devices') WHERE name IN ('endpoint', 'token')`).Scan(&legacyColumns); err != nil {
		t.Fatal(err)
	}
	if legacyColumns != 0 {
		t.Fatalf("agentdock_devices retains %d legacy secret/location columns", legacyColumns)
	}
}

func TestBridgeCapabilitiesPersistAcrossStoreRestart(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "nexus.db")
	db, err := core.OpenSQLite(ctx, dbPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := store.CreatePairingCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(ctx, PairInput{Code: pairing.Code, DeviceID: "device_bridge_persist", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateHello(ctx, node.ID, Hello{
		DeviceID: node.DeviceID, ProtocolVersion: ConnectionProtocolVersion,
		BridgeCapabilities: []string{protocol.ArtifactReadCapability}, UIResources: []UIResourceCapability{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := core.OpenSQLite(ctx, dbPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := core.EnsureSchema(ctx, reopened); err != nil {
		t.Fatal(err)
	}
	restartedStore, err := NewStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restartedStore.BridgeCapabilities(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{protocol.ArtifactReadCapability}) {
		t.Fatalf("bridge capabilities after restart = %#v", got)
	}
}

func TestHelloRequiresExplicitUIResources(t *testing.T) {
	store, _ := newTestStore(t)
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), PairInput{Code: pairing.Code, DeviceID: "device_ui_required", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.UpdateHello(t.Context(), node.ID, Hello{
		DeviceID: node.DeviceID, ProtocolVersion: ConnectionProtocolVersion,
		UIResources: nil,
	})
	var validation ValidationError
	if !errors.As(err, &validation) || validation.Message != "AgentDock Bridge v2 握手必须声明 ui_resources" {
		t.Fatalf("missing ui_resources error = %#v", err)
	}

	if _, err := store.UpdateHello(t.Context(), node.ID, Hello{
		DeviceID: node.DeviceID, ProtocolVersion: ConnectionProtocolVersion,
		UIResources: []UIResourceCapability{},
	}); err != nil {
		t.Fatalf("explicit empty ui_resources should be valid: %v", err)
	}
}

func TestHelloPreservesUIResourcesWithoutCatalogFiltering(t *testing.T) {
	store, _ := newTestStore(t)
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), PairInput{Code: pairing.Code, DeviceID: "device_ui_roundtrip", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	resources := []UIResourceCapability{
		{URI: protocol.ContextUIResourceURI, Contract: protocol.ContextUIContract, MIMEType: protocol.MCPAppMIMEType},
		{URI: "ui://agentdock/future-widget", Contract: "agentdock.future-widget.v7", MIMEType: "application/vnd.agentdock.widget+json"},
		{URI: protocol.WorkflowUIResourceURI, Contract: "agentdock.workflow.v99", MIMEType: "text/html;profile=future-mcp-app"},
	}
	if _, err := store.UpdateHello(t.Context(), node.ID, Hello{
		DeviceID: node.DeviceID, ProtocolVersion: ConnectionProtocolVersion, UIResources: resources,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.UIResources(t.Context(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, resources) {
		t.Fatalf("ui_resources = %#v, want exact %#v", got, resources)
	}
}

func TestHelloRejectsStructurallyInvalidUIResources(t *testing.T) {
	store, _ := newTestStore(t)
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), PairInput{Code: pairing.Code, DeviceID: "device_ui_invalid", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []UIResourceCapability{
		{URI: "https://example.test/widget", Contract: "future.v1", MIMEType: "text/html"},
		{URI: "ui://agentdock/future", Contract: "", MIMEType: "text/html"},
		{URI: "ui://agentdock/future", Contract: "future.v1", MIMEType: "not a mime"},
	}
	for _, resource := range tests {
		if _, err := store.UpdateHello(t.Context(), node.ID, Hello{
			DeviceID: node.DeviceID, ProtocolVersion: ConnectionProtocolVersion, UIResources: []UIResourceCapability{resource},
		}); err == nil {
			t.Fatalf("structurally invalid ui resource was accepted: %#v", resource)
		}
	}
	duplicate := UIResourceCapability{URI: "ui://agentdock/future", Contract: "future.v1", MIMEType: "text/html"}
	if _, err := store.UpdateHello(t.Context(), node.ID, Hello{
		DeviceID: node.DeviceID, ProtocolVersion: ConnectionProtocolVersion, UIResources: []UIResourceCapability{duplicate, duplicate},
	}); err == nil {
		t.Fatal("duplicate ui resource URI was accepted")
	}
}

func TestHelloUpdatesCapabilitiesAndDisabledNodeIsRejected(t *testing.T) {
	store, _ := newTestStore(t)
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), PairInput{Code: pairing.Code, DeviceID: "device_12345678", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateHello(t.Context(), node.ID, Hello{
		DeviceID: "device_12345678", Version: "0.8.0", ProtocolVersion: ConnectionProtocolVersion,
		OS: "linux", Arch: "amd64", Capabilities: []string{"read_file", "read_file", "exec_command"}, ToolContractHash: "sha256:test",
		BridgeCapabilities: []string{protocol.ArtifactReadCapability, protocol.ArtifactReadCapability},
		Tools:              []ToolDescriptor{{Name: "read_file", InputSchema: map[string]any{"type": "object"}}}, UIResources: []UIResourceCapability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Capabilities) != 2 || updated.LastSeenAt == nil {
		t.Fatalf("unexpected hello state: %#v", updated)
	}
	bridgeCapabilities, err := store.BridgeCapabilities(t.Context(), node.ID)
	if err != nil || !reflect.DeepEqual(bridgeCapabilities, []string{protocol.ArtifactReadCapability}) {
		t.Fatalf("bridge capabilities = %#v err=%v", bridgeCapabilities, err)
	}
	descriptors, err := store.ToolDescriptors(t.Context(), node.ID)
	if err != nil || len(descriptors) != 1 || descriptors[0].Name != "read_file" {
		t.Fatalf("tool descriptors = %#v err=%v", descriptors, err)
	}
	enabled := false
	if _, err := store.Update(t.Context(), node.ID, UpdateInput{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateHello(t.Context(), node.ID, Hello{DeviceID: node.DeviceID}); !errors.Is(err, ErrNodeDisabled) {
		t.Fatalf("disabled hello error = %v", err)
	}
}
