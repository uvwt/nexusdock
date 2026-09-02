package httpx

import (
	"reflect"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/config"
)

func TestNexusMCPAppsDisabledRemovesCentralAndNodePresentationOnly(t *testing.T) {
	for _, tool := range nexusToolDefinitionsWithApps(false) {
		if tool.Meta["ui"] != nil {
			t.Fatalf("central tool %s still exposes Apps UI metadata: %#v", tool.Name, tool.Meta)
		}
	}
	if meta := centralToolResultMetaWithApps("workflow_template_manage", map[string]any{"action": "match"}, false); meta != nil {
		t.Fatalf("workflow match still exposes Apps UI result metadata: %#v", meta)
	}

	descriptor := agentdock.ToolDescriptor{
		Name:        "file_edit",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Meta: map[string]any{
			"ui":                     map[string]any{"resourceUri": protocol.FileChangeUIResourceURI},
			"file_arg_rewrite_paths": []string{"file"},
		},
	}
	published := nodeMCPToolWithApps(descriptor, false)
	if published.Meta["ui"] != nil {
		t.Fatalf("node tool still exposes Apps UI metadata: %#v", published.Meta)
	}
	if !reflect.DeepEqual(published.Meta["file_arg_rewrite_paths"], []string{"file"}) {
		t.Fatalf("node tool lost execution metadata: %#v", published.Meta)
	}
	if descriptor.Meta["ui"] == nil {
		t.Fatal("presentation filtering mutated the persisted node descriptor")
	}
}

func TestNexusMCPAppsDisabledStripsProxiedResultUIOnly(t *testing.T) {
	server := &Server{cfg: config.Config{MCPAppsEnabled: false}}
	result, err := server.gatewayToolResult("demo", map[string]any{
		"isError":           false,
		"structuredContent": map[string]any{"value": "ok"},
		"content":           []map[string]any{{"type": "text", "text": "ok"}},
		"_meta": map[string]any{
			"ui":       map[string]any{"resourceUri": protocol.FileChangeUIResourceURI},
			"trace_id": "trace-1",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["ui"] != nil {
		t.Fatalf("proxied result still exposes Apps UI metadata: %#v", result.Meta)
	}
	if result.Meta["trace_id"] != "trace-1" {
		t.Fatalf("proxied result lost unrelated metadata: %#v", result.Meta)
	}
}

func TestNexusMCPAppsToggleUpdatesActiveSessionToolPresentation(t *testing.T) {
	server := &Server{
		cfg:          config.Config{MCPAppsEnabled: true},
		mcpTools:     make(map[string]publishedNodeTool),
		mcpResources: make(map[string]struct{}),
	}
	server.initializeMCPGateway()

	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "nexus-mcp-apps-toggle-test", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	contextTool := findSessionTool(t, clientSession, "agentdock_context")
	if contextTool.Meta["ui"] == nil {
		t.Fatalf("agentdock_context missing Apps UI metadata before toggle: %#v", contextTool.Meta)
	}

	server.setMCPAppsEnabled(false)
	contextTool = findSessionTool(t, clientSession, "agentdock_context")
	if contextTool.Meta["ui"] != nil {
		t.Fatalf("active session still sees Apps UI metadata after disable: %#v", contextTool.Meta)
	}

	server.setMCPAppsEnabled(true)
	contextTool = findSessionTool(t, clientSession, "agentdock_context")
	if contextTool.Meta["ui"] == nil {
		t.Fatalf("active session did not recover Apps UI metadata after re-enable: %#v", contextTool.Meta)
	}
}

func findSessionTool(t *testing.T, session *mcpsdk.ClientSession, name string) *mcpsdk.Tool {
	t.Helper()
	for tool, err := range session.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %s not found", name)
	return nil
}
