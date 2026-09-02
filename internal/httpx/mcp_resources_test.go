package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/config"
)

func TestSyncMCPAppResourcesPublishesAdvertisedUIResources(t *testing.T) {
	const domain = "https://nexus.example.test"
	store := newHTTPTestAgentDockStore(t)
	descriptor := agentdock.ToolDescriptor{
		Name: "file_edit",
		Meta: map[string]any{"ui": map[string]any{"resourceUri": protocol.ContextUIResourceURI}},
	}
	node := pairHTTPTestNode(t, store, "device_resource_catalog", "DockMini", "2.0.0", descriptor)
	if _, err := store.UpdateHello(t.Context(), node.ID, agentdock.Hello{
		DeviceID: node.DeviceID, ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Tools: []agentdock.ToolDescriptor{descriptor},
		UIResources: []agentdock.UIResourceCapability{{
			URI: protocol.FileChangeUIResourceURI, Contract: protocol.FileChangeUIContract, MIMEType: protocol.MCPAppMIMEType,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	sdk := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	server := &Server{
		cfg: config.Config{PublicURL: domain, MCPAppsEnabled: true}, agentDock: store, mcpServer: sdk,
		mcpResources: make(map[string]struct{}),
	}
	server.syncMCPAppResources()

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverDone := make(chan error, 1)
	go func() { serverDone <- sdk.Run(t.Context(), serverTransport) }()
	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "nexus-resource-test", Version: "1"},
		&mcpsdk.ClientOptions{Capabilities: &mcpsdk.ClientCapabilities{}},
	)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := <-serverDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Server.Run() error = %v", err)
		}
	})

	listed := make(map[string]*mcpsdk.Resource)
	for resource, err := range session.Resources(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		listed[resource.URI] = resource
	}
	resource := listed[protocol.FileChangeUIResourceURI]
	if resource == nil || resource.MIMEType != protocol.MCPAppMIMEType {
		t.Fatalf("listed resource = %#v", resource)
	}
	if listed[protocol.ContextUIResourceURI] != nil {
		t.Fatalf("tool _meta.ui incorrectly published a resource without ui_resources capability: %#v", listed)
	}
	ui, ok := resource.Meta["ui"].(map[string]any)
	if !ok || ui["prefersBorder"] != true || ui["domain"] != domain {
		t.Fatalf("resource ui meta = %#v", resource.Meta)
	}
}

func TestMCPAppsToggleRemovesRelayButKeepsPersistedCapabilities(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := agentdock.ToolDescriptor{Name: "file_edit", InputSchema: map[string]any{"type": "object"}}
	node := pairHTTPTestNode(t, store, "device_resource_toggle", "DockMini", "2.0.0", descriptor)
	capability := agentdock.UIResourceCapability{
		URI: protocol.FileChangeUIResourceURI, Contract: protocol.FileChangeUIContract, MIMEType: protocol.MCPAppMIMEType,
	}
	if _, err := store.UpdateHello(t.Context(), node.ID, agentdock.Hello{
		DeviceID: node.DeviceID, ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Tools: []agentdock.ToolDescriptor{descriptor}, UIResources: []agentdock.UIResourceCapability{capability},
	}); err != nil {
		t.Fatal(err)
	}

	sdk := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "toggle-test", Version: "1"}, nil)
	server := &Server{
		cfg: config.Config{MCPAppsEnabled: true}, agentDock: store, mcpServer: sdk,
		mcpTools: make(map[string]publishedNodeTool), mcpResources: make(map[string]struct{}),
	}
	server.syncMCPAppResources()
	if _, ok := server.mcpResources[capability.URI]; !ok {
		t.Fatalf("resource %s was not published before toggle", capability.URI)
	}

	server.setMCPAppsEnabled(false)
	if len(server.mcpResources) != 0 {
		t.Fatalf("resources were not removed after disabling MCP Apps UI: %#v", server.mcpResources)
	}
	persisted, err := store.UIResources(t.Context(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0] != capability {
		t.Fatalf("toggle mutated persisted node capability: %#v", persisted)
	}

	server.setMCPAppsEnabled(true)
	if _, ok := server.mcpResources[capability.URI]; !ok {
		t.Fatalf("resource %s was not restored after enabling MCP Apps UI", capability.URI)
	}
}

func TestPublishedMCPAppResourcesRecoverFromPersistedCapabilities(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := agentdock.ToolDescriptor{Name: "read_file", InputSchema: map[string]any{"type": "object"}}
	node := pairHTTPTestNode(t, store, "device_resource_restart", "DockMini", "2.0.0", descriptor)
	capabilities := []agentdock.UIResourceCapability{
		{URI: protocol.ContextUIResourceURI, Contract: protocol.ContextUIContract, MIMEType: protocol.MCPAppMIMEType},
		{URI: protocol.WorkflowUIResourceURI, Contract: protocol.WorkflowUIContract, MIMEType: protocol.MCPAppMIMEType},
	}
	if _, err := store.UpdateHello(t.Context(), node.ID, agentdock.Hello{
		DeviceID: node.DeviceID, ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Tools: []agentdock.ToolDescriptor{descriptor}, UIResources: capabilities,
	}); err != nil {
		t.Fatal(err)
	}

	// A new Server has no in-memory tool/provider state; its resource catalog must recover solely from persisted ui_resources.
	restarted := &Server{cfg: config.Config{MCPAppsEnabled: true}, agentDock: store}
	resources, err := restarted.publishedMCPAppResourceURIs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != len(capabilities) {
		t.Fatalf("restarted resource catalog = %#v", resources)
	}
	for _, capability := range capabilities {
		if _, ok := resources[capability.URI]; !ok {
			t.Fatalf("persisted capability %s missing after restart: %#v", capability.URI, resources)
		}
	}
}

func TestCentralWorkflowResultMetaIsActionScoped(t *testing.T) {
	if meta := centralToolResultMeta("workflow_template_manage", map[string]any{"action": "list"}); meta != nil {
		t.Fatalf("list unexpectedly has Workflow UI meta: %#v", meta)
	}
	meta := centralToolResultMeta("workflow_template_manage", map[string]any{"action": "match"})
	ui, ok := meta["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != protocol.WorkflowUIResourceURI {
		t.Fatalf("match Workflow UI meta = %#v", meta)
	}
}

func TestDecodeNodeMCPAppResourceReplacesNodeDomainWithNexusDomain(t *testing.T) {
	const domain = "https://nexus.example.test"
	read, err := decodeNodeMCPAppResource(protocol.TaskProgressUIResourceURI, map[string]any{
		"contents": []any{map[string]any{
			"uri": protocol.TaskProgressUIResourceURI, "mimeType": protocol.MCPAppMIMEType, "text": "<!doctype html>",
			"_meta": map[string]any{"ui": map[string]any{"domain": "https://dockmini.example.test"}},
		}},
	}, domain)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Contents) != 1 {
		t.Fatalf("contents = %#v", read.Contents)
	}
	ui, ok := read.Contents[0].Meta["ui"].(map[string]any)
	if !ok || ui["prefersBorder"] != true || ui["domain"] != domain {
		t.Fatalf("sanitized meta = %#v", read.Contents[0].Meta)
	}
}

func TestNodeProvidesUIResourceRequiresExactCapability(t *testing.T) {
	valid := agentdock.UIResourceCapability{
		URI: protocol.ContextUIResourceURI, Contract: protocol.ContextUIContract, MIMEType: protocol.MCPAppMIMEType,
	}
	if !nodeProvidesUIResource([]agentdock.UIResourceCapability{valid}, protocol.ContextUIResourceURI, protocol.ContextUIContract) {
		t.Fatal("exact ui_resources capability was rejected")
	}
	wrongContract := valid
	wrongContract.Contract = protocol.ContextUIContract + ".mismatch"
	if nodeProvidesUIResource([]agentdock.UIResourceCapability{wrongContract}, protocol.ContextUIResourceURI, protocol.ContextUIContract) {
		t.Fatal("wrong renderer contract was accepted")
	}
	wrongMIME := valid
	wrongMIME.MIMEType = "text/html"
	if nodeProvidesUIResource([]agentdock.UIResourceCapability{wrongMIME}, protocol.ContextUIResourceURI, protocol.ContextUIContract) {
		t.Fatal("wrong resource MIME type was accepted")
	}
}

func TestToolBoundUIResourceURIIsPresentationOnlyAndRejectsUnknownResources(t *testing.T) {
	known := agentdock.ToolDescriptor{Meta: map[string]any{
		"ui": map[string]any{"resourceUri": protocol.FileChangeUIResourceURI},
	}}
	if got := toolBoundUIResourceURI(known); got != protocol.FileChangeUIResourceURI {
		t.Fatalf("known resource URI = %q", got)
	}
	unknown := agentdock.ToolDescriptor{Meta: map[string]any{
		"ui": map[string]any{"resourceUri": "https://example.test/widget"},
	}}
	if got := toolBoundUIResourceURI(unknown); got != "" {
		t.Fatalf("unknown resource URI = %q", got)
	}
}

func TestMCPAppResourceSelectsOnlyExplicitCapableProvider(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	bindingOnlyDescriptor := agentdock.ToolDescriptor{
		Name: "agentdock_context", InputSchema: map[string]any{"type": "object"},
		Meta: map[string]any{"ui": map[string]any{"resourceUri": protocol.ContextUIResourceURI}},
	}
	capableDescriptor := agentdock.ToolDescriptor{Name: "agentdock_context", InputSchema: map[string]any{"type": "object"}}
	withoutResource := pairHTTPTestNode(t, store, "device_resource_none", "A Without Resource", "2.0.0", bindingOnlyDescriptor)
	withResource := pairHTTPTestNode(t, store, "device_resource_capable", "B Capable", "2.0.0", capableDescriptor)
	hub := agentdock.NewHub(store)

	// Presentation binding is deliberately insufficient: this node advertises _meta.ui but no ui_resources capability.
	withoutInvoked := connectResourceTestNode(t, hub, withoutResource, bindingOnlyDescriptor, nil, protocol.ContextUIResourceURI, "<html>wrong</html>", false)
	capability := agentdock.UIResourceCapability{
		URI: protocol.ContextUIResourceURI, Contract: protocol.ContextUIContract, MIMEType: protocol.MCPAppMIMEType,
	}
	// Capability is sufficient even when this tool descriptor has no _meta.ui presentation binding.
	withInvoked := connectResourceTestNode(t, hub, withResource, capableDescriptor, []agentdock.UIResourceCapability{capability}, protocol.ContextUIResourceURI, "<html>capable</html>", true)

	nodes, err := store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{agentDock: store, agentDockHub: hub, cfg: config.Config{PublicURL: "https://nexus.example.test"}}
	read, err := server.readMCPAppResourceWithTimeout(t.Context(), nodes, protocol.ContextUIResourceURI, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Contents) != 1 || read.Contents[0].Text != "<html>capable</html>" {
		t.Fatalf("resource=%#v", read.Contents)
	}
	select {
	case <-withInvoked:
	case <-time.After(time.Second):
		t.Fatal("explicit resource provider was not invoked")
	}
	select {
	case <-withoutInvoked:
		t.Fatal("node without ui_resources capability was invoked")
	default:
	}
}

func TestMCPAppResourceBoundsStalledCompatibleProvider(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := agentdock.ToolDescriptor{Name: "agentdock_context", InputSchema: map[string]any{"type": "object"}}
	node := pairHTTPTestNode(t, store, "device_resource_stalled", "Stalled", "2.0.0", descriptor)
	hub := agentdock.NewHub(store)
	capability := agentdock.UIResourceCapability{
		URI: protocol.ContextUIResourceURI, Contract: protocol.ContextUIContract, MIMEType: protocol.MCPAppMIMEType,
	}
	_ = connectResourceTestNode(t, hub, node, descriptor, []agentdock.UIResourceCapability{capability}, protocol.ContextUIResourceURI, "", false)
	nodes, err := store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{agentDock: store, agentDockHub: hub}
	started := time.Now()
	_, err = server.readMCPAppResourceWithTimeout(t.Context(), nodes, protocol.ContextUIResourceURI, 30*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "resource timeout") {
		t.Fatalf("timeout err=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("resource timeout took %v", elapsed)
	}
}

func connectResourceTestNode(t *testing.T, hub *agentdock.Hub, node agentdock.Node, descriptor agentdock.ToolDescriptor, resources []agentdock.UIResourceCapability, uri, html string, shouldRespond bool) <-chan struct{} {
	t.Helper()
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, node.ID); err != nil {
			t.Errorf("accept resource node: %v", err)
			return
		}
		close(connected)
	}))
	t.Cleanup(server.Close)
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if resources == nil {
		resources = []agentdock.UIResourceCapability{}
	}
	if err := socket.WriteJSON(map[string]any{
		"type": protocol.MessageNodeHello, "protocol_version": agentdock.ConnectionProtocolVersion,
		"hello": agentdock.Hello{
			DeviceID: node.DeviceID, Version: node.Version, ProtocolVersion: agentdock.ConnectionProtocolVersion,
			OS: node.OS, Arch: node.Arch, Capabilities: []string{descriptor.Name}, Tools: []agentdock.ToolDescriptor{descriptor}, UIResources: resources,
		},
	}); err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := socket.ReadJSON(&ready); err != nil || ready["type"] != protocol.MessageNodeReady {
		t.Fatalf("ready=%#v err=%v", ready, err)
	}
	<-connected
	invoked := make(chan struct{}, 1)
	go func() {
		var request struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Operation string `json:"operation"`
		}
		if err := socket.ReadJSON(&request); err != nil {
			return
		}
		invoked <- struct{}{}
		if request.Operation != protocol.OperationResourceRead || !shouldRespond {
			return
		}
		_ = socket.WriteJSON(map[string]any{
			"type": protocol.MessageToolResult, "request_id": request.RequestID,
			"result": map[string]any{"contents": []any{map[string]any{"uri": uri, "mimeType": protocol.MCPAppMIMEType, "text": html}}},
		})
	}()
	return invoked
}
