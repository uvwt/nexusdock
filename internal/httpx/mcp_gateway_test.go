package httpx

import (
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/recall"
)

// newGatewayTestServer 构造绑定契约 Bridge 的最小 MCP 网关测试环境：
// Bridge 的公开/退休回调与生产 initializeMCPGateway 一样映射到 Server 的 MCP 注册方法。
func newGatewayTestServer(t *testing.T, store *agentdock.Store) *Server {
	t.Helper()
	server := &Server{
		agentDock:    store,
		agentDockHub: agentdock.NewHub(store),
		mcpServer:    mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil),
	}
	if store != nil {
		server.publishedToolBridge = agentdock.NewPublishedToolBridge(store, slog.Default())
		server.bindPublishedToolBridge()
	}
	return server
}

func TestInitializeMCPGatewayAdvertisesFixedInstructions(t *testing.T) {
	server := &Server{mcpResources: make(map[string]struct{})}
	server.initializeMCPGateway()

	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "nexusdock-test", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	result := clientSession.InitializeResult()
	if result == nil {
		t.Fatal("InitializeResult is nil")
	}
	if result.Instructions != nexusServerInstructions {
		t.Fatalf("Instructions = %q, want %q", result.Instructions, nexusServerInstructions)
	}
	tools, err := clientSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	central := map[string]bool{"agentdock_context": false, "workspace_context": false, "workflow_template_manage": false}
	for _, tool := range tools.Tools {
		if tool.Name == "node_list" {
			t.Fatal("tools/list still exposes node_list")
		}
		if _, expected := central[tool.Name]; !expected {
			continue
		}
		central[tool.Name] = true
		input := tool.InputSchema.(map[string]any)
		properties := input["properties"].(map[string]any)
		_, hasNodeID := properties["node_id"]
		if tool.Name == "workspace_context" {
			if !hasNodeID {
				t.Fatalf("workspace_context must require node_id: %#v", input)
			}
			continue
		}
		if hasNodeID {
			t.Fatalf("fleet-owned central tool %s unexpectedly requires node_id: %#v", tool.Name, input)
		}
	}
	for name, found := range central {
		if !found {
			t.Fatalf("tools/list missing central tool %s", name)
		}
	}
}

func TestNodeInputSchemaRequiresNodeID(t *testing.T) {
	schema := nodeInputSchema(map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"},
	})
	properties := schema["properties"].(map[string]any)
	nodeID, ok := properties["node_id"].(map[string]any)
	if !ok {
		t.Fatal("node_id property is missing")
	}
	if nodeID["description"] != "Target AgentDock node ID from agentdock_context." {
		t.Fatalf("node_id description = %#v", nodeID["description"])
	}
	required := schema["required"].([]any)
	if len(required) != 2 || required[1] != "node_id" {
		t.Fatalf("required = %#v", required)
	}
}

func TestRecallUpdateFactPreviewsAndWrites(t *testing.T) {
	store, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(recall.WriteRequest{Path: "profile.md", Content: "# Profile\n\neditor: old\n", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	preview, err := server.updateRecallFacts(t.Context(), "profile.md", map[string]any{"key": "editor", "value": "new"})
	if err != nil || preview["dry_run"] != true {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	unchanged, _ := store.Read("profile.md")
	if strings.Contains(unchanged.Content, "editor: new") {
		t.Fatal("preview mutated Recall")
	}
	result, err := server.updateRecallFacts(t.Context(), "profile.md", map[string]any{"key": "editor", "value": "new", "confirmed": true})
	if err != nil || result["written"] != true {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	updated, _ := store.Read("profile.md")
	if !strings.Contains(updated.Content, "editor:new") {
		t.Fatalf("updated content = %q", updated.Content)
	}
}

func TestRegisterNodeToolsKeepsFirstPublishedContract(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	server := newGatewayTestServer(t, store)
	first := agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "integer"}},
		},
	}
	second := agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "number"}},
		},
	}
	pairHTTPTestNode(t, store, "device_first_contract", "DockMini", "1.8.3", first)
	pairHTTPTestNode(t, store, "device_second_contract", "DockAir", "1.9.0", second)

	server.registerNodeTools(agentdock.Node{ID: "node_first", Version: "1.8.3"}, agentdock.Hello{Tools: []agentdock.ToolDescriptor{first}})
	server.registerNodeTools(agentdock.Node{ID: "node_second", Version: "1.9.0"}, agentdock.Hello{Tools: []agentdock.ToolDescriptor{second}})

	published, ok := server.publishedToolBridge.Published("exec_command")
	if !ok {
		t.Fatal("exec_command was not published")
	}
	firstHash, _ := agentdock.ToolContractHash(first)
	if published.ContractHash != firstHash || len(published.AcceptedSemanticHashes) != 1 || published.AcceptedSemanticHashes[0] != firstHash {
		t.Fatalf("published contract changed: %#v", published)
	}
}

func TestCallNodeToolReturnsContractMismatchBeforeInvoke(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	target := pairHTTPTestNode(t, store, "device_abcdefgh", "DockAir", "1.9.0", agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "number"}},
		},
	})
	published := agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "integer"}},
		},
	}
	server := newGatewayTestServer(t, store)
	server.registerNodeTools(agentdock.Node{ID: "node_old", Version: "1.8.3"}, agentdock.Hello{Tools: []agentdock.ToolDescriptor{published}})

	result, err := server.callNodeTool(t.Context(), "exec_command", map[string]any{"node_id": target.ID, "timeout": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("result should be an MCP tool error: %#v", result)
	}
	details, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %#v", result.StructuredContent)
	}
	if details["code"] != "TOOL_CONTRACT_MISMATCH" {
		t.Fatalf("code = %#v", details["code"])
	}
	if details["error"] != "目标 AgentDock 的工具契约不在 Nexus 当前已发布的兼容集合中，请刷新 GPT 工具；若仍不一致，请检查相关设备的 AgentDock 版本或工具契约。" {
		t.Fatalf("error = %#v", details["error"])
	}
	differences, ok := details["differences"].([]any)
	if !ok || len(differences) != 1 {
		t.Fatalf("differences = %#v", details["differences"])
	}
	difference := differences[0].(map[string]any)
	if difference["path"] != "inputSchema.properties.timeout.type" || difference["published"] != "integer" || difference["node"] != "number" {
		t.Fatalf("difference = %#v", difference)
	}
}

func TestCallNodeToolAcceptsSameContractWithDifferentDescription(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := agentdock.ToolDescriptor{
		Name: "read_file", Description: "macOS description",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "macOS path"}}},
	}
	targetDescriptor := agentdock.ToolDescriptor{
		Name: descriptor.Name, Description: "Windows description",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "Windows or WSL path"}}},
	}
	target := pairHTTPTestNode(t, store, "device_ijklmnop", "DockWin", "1.8.3", targetDescriptor)
	server := newGatewayTestServer(t, store)
	server.registerNodeTools(agentdock.Node{ID: "node_source", Version: "1.8.3"}, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})

	result, err := server.callNodeTool(t.Context(), "read_file", map[string]any{"node_id": target.ID, "path": "/tmp/a"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("offline target should prove invocation was attempted: %#v", result)
	}
	details := result.StructuredContent.(map[string]any)
	if details["error"] != agentdock.ErrNodeOffline.Error() {
		t.Fatalf("expected compatible contract to reach hub, got %#v", details)
	}
}

func platformContractDescriptor(name string, properties map[string]any, required []any) agentdock.ToolDescriptor {
	return agentdock.ToolDescriptor{
		Name: name,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		},
	}
}

func newHTTPTestAgentDockStore(t *testing.T) *agentdock.Store {
	t.Helper()
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	store, err := agentdock.NewStore(db)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store
}

func pairHTTPTestNode(t *testing.T, store *agentdock.Store, deviceID, name, version string, descriptor agentdock.ToolDescriptor) agentdock.Node {
	t.Helper()
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), agentdock.PairInput{Code: pairing.Code, DeviceID: deviceID, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	node, err = store.UpdateHello(t.Context(), node.ID, agentdock.Hello{
		DeviceID: node.DeviceID, Version: version, ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Capabilities: []string{descriptor.Name}, Tools: []agentdock.ToolDescriptor{descriptor}, UIResources: []agentdock.UIResourceCapability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestRegisterNodeToolsPromotesOnlyAfterProvidersConverge(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	oldDescriptor := agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "integer"}},
		},
	}
	newDescriptor := agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "number"}},
		},
	}
	first := pairHTTPTestNode(t, store, "device_qrstuvwx", "DockMini", "1.8.3", oldDescriptor)
	second := pairHTTPTestNode(t, store, "device_yzabcdef", "DockAir", "1.8.3", oldDescriptor)
	server := newGatewayTestServer(t, store)
	server.registerNodeTools(first, agentdock.Hello{Tools: []agentdock.ToolDescriptor{oldDescriptor}})
	oldHash, _ := agentdock.ToolContractHash(oldDescriptor)
	newHash, _ := agentdock.ToolContractHash(newDescriptor)

	first = updateHTTPTestNodeContract(t, store, first, "1.9.0", newDescriptor)
	server.registerNodeTools(first, agentdock.Hello{Tools: []agentdock.ToolDescriptor{newDescriptor}})
	published, _ := server.publishedToolBridge.Published("exec_command")
	if published.ContractHash != oldHash {
		t.Fatalf("mixed providers changed public contract: %#v", published)
	}

	second = updateHTTPTestNodeContract(t, store, second, "1.9.0", newDescriptor)
	server.registerNodeTools(second, agentdock.Hello{Tools: []agentdock.ToolDescriptor{newDescriptor}})
	published, _ = server.publishedToolBridge.Published("exec_command")
	if published.ContractHash != newHash || len(published.AcceptedSemanticHashes) != 1 || published.AcceptedSemanticHashes[0] != newHash {
		t.Fatalf("converged providers did not promote new contract: %#v", published)
	}
}

func TestRegisterNodeToolsRetiresToolWhenLastProviderDropsCapability(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := agentdock.ToolDescriptor{
		Name: "browser_act",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"session_id": map[string]any{"type": "string"}},
		},
	}
	node := pairHTTPTestNode(t, store, "device_retire_tool", "DockMini", "1.9.0", descriptor)
	server := newGatewayTestServer(t, store)
	server.registerNodeTools(node, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})

	updated, err := store.UpdateHello(t.Context(), node.ID, agentdock.Hello{
		DeviceID: node.DeviceID, Version: "1.9.1", ProtocolVersion: agentdock.ConnectionProtocolVersion, UIResources: []agentdock.UIResourceCapability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.registerNodeTools(updated, agentdock.Hello{})
	if _, ok := server.publishedToolBridge.Published("browser_act"); ok {
		t.Fatal("browser_act should retire after the last provider drops the capability")
	}
	contracts, err := store.ListPublishedToolContracts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 0 {
		t.Fatalf("published contracts after retirement = %#v", contracts)
	}
}

func TestReconcileKeepsToolWhenLastProviderIsDisabled(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := agentdock.ToolDescriptor{
		Name:        "exec_command",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	node := pairHTTPTestNode(t, store, "device_disabled_provider", "DockMini", "1.9.0", descriptor)
	server := newGatewayTestServer(t, store)
	server.registerNodeTools(node, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})

	disabled := false
	if _, err := store.Update(t.Context(), node.ID, agentdock.UpdateInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	server.publishedToolBridge.ReconcileNames([]string{"exec_command"})
	if _, ok := server.publishedToolBridge.Published("exec_command"); !ok {
		t.Fatal("disabled provider should keep its published tool contract")
	}
}

func updateHTTPTestNodeContract(t *testing.T, store *agentdock.Store, node agentdock.Node, version string, descriptor agentdock.ToolDescriptor) agentdock.Node {
	t.Helper()
	updated, err := store.UpdateHello(t.Context(), node.ID, agentdock.Hello{
		DeviceID: node.DeviceID, Version: version, ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Capabilities: []string{descriptor.Name}, Tools: []agentdock.ToolDescriptor{descriptor}, UIResources: []agentdock.UIResourceCapability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestInitializeMCPGatewayRestoresPublishedContractBeforeNodeOrder(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	oldDescriptor := agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "integer"}},
		},
	}
	newDescriptor := agentdock.ToolDescriptor{
		Name: "exec_command",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"timeout": map[string]any{"type": "number"}},
		},
	}
	oldNode := pairHTTPTestNode(t, store, "device_restart_old", "ZuluOld", "1.8.3", oldDescriptor)
	_ = pairHTTPTestNode(t, store, "device_restart_new", "AlphaNew", "1.9.0", newDescriptor)

	first := newGatewayTestServer(t, store)
	first.registerNodeTools(oldNode, agentdock.Hello{Tools: []agentdock.ToolDescriptor{oldDescriptor}})
	first.registerNodeTools(agentdock.Node{ID: "node_new", Version: "1.9.0"}, agentdock.Hello{Tools: []agentdock.ToolDescriptor{newDescriptor}})
	oldHash, _ := agentdock.ToolContractHash(oldDescriptor)

	restarted := newGatewayTestServer(t, store)
	restarted.initializeMCPGateway()
	published, ok := restarted.publishedToolBridge.Published("exec_command")
	if !ok || published.ContractHash != oldHash || len(published.AcceptedSemanticHashes) != 1 || published.AcceptedSemanticHashes[0] != oldHash {
		t.Fatalf("restart changed published contract: %#v", published)
	}
}

func TestInitializeMCPGatewayRetiresPersistedToolWithoutProvider(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := agentdock.ToolDescriptor{
		Name:        "browser_act",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	if err := store.SavePublishedToolContract(t.Context(), agentdock.PublishedToolContract{
		ToolName: "browser_act", Descriptor: descriptor, SourceNodeID: "node_gone", SourceVersion: "1.8.3",
	}); err != nil {
		t.Fatal(err)
	}

	server := newGatewayTestServer(t, store)
	server.initializeMCPGateway()
	if _, ok := server.publishedToolBridge.Published("browser_act"); ok {
		t.Fatal("startup should retire persisted tools that no node provides")
	}
	contracts, err := store.ListPublishedToolContracts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 0 {
		t.Fatalf("persisted stale contracts after startup = %#v", contracts)
	}
}

func TestRegisterNodeToolsMergesCompatibleCrossVersionContracts(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	oldDescriptor := platformContractDescriptor("exec_command", map[string]any{
		"command": map[string]any{"type": "string"},
		"workdir": map[string]any{"type": "string", "description": "host path"},
	}, []any{"command"})
	newDescriptor := platformContractDescriptor("exec_command", map[string]any{
		"command":          map[string]any{"type": "string"},
		"workdir":          map[string]any{"type": "string", "description": "Windows or WSL path"},
		"runtime":          map[string]any{"type": "string", "enum": []any{"windows", "wsl"}},
		"wsl_distribution": map[string]any{"type": "string"},
	}, []any{"command"})
	oldNode := pairHTTPTestNode(t, store, "device_crossver_old", "DockMini", "1.8.3", oldDescriptor)
	newNode := pairHTTPTestNode(t, store, "device_crossver_new", "DockWin", "1.9.0", newDescriptor)
	server := newGatewayTestServer(t, store)

	server.registerNodeTools(oldNode, agentdock.Hello{Tools: []agentdock.ToolDescriptor{oldDescriptor}})
	server.registerNodeTools(newNode, agentdock.Hello{Tools: []agentdock.ToolDescriptor{newDescriptor}})

	published, ok := server.publishedToolBridge.Published("exec_command")
	if !ok {
		t.Fatal("exec_command was not published")
	}
	properties := published.Descriptor.InputSchema["properties"].(map[string]any)
	if _, ok := properties["runtime"]; !ok {
		t.Fatalf("fleet schema did not include compatible optional property: %#v", properties)
	}
	oldHash, _ := agentdock.ToolContractHash(oldDescriptor)
	newHash, _ := agentdock.ToolContractHash(newDescriptor)
	if !slices.Contains(published.AcceptedSemanticHashes, oldHash) || !slices.Contains(published.AcceptedSemanticHashes, newHash) {
		t.Fatalf("accepted hashes = %#v", published.AcceptedSemanticHashes)
	}
	for _, node := range []agentdock.Node{oldNode, newNode} {
		mismatch, err := server.publishedToolBridge.ToolContractMismatch(t.Context(), node, "exec_command")
		if err != nil {
			t.Fatal(err)
		}
		if mismatch != nil {
			t.Fatalf("compatible cross-version node %s rejected: %#v", node.Name, mismatch)
		}
	}

	contracts, err := store.ListPublishedToolContracts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 1 || len(contracts[0].AcceptedSemanticHashes) != 2 {
		t.Fatalf("persisted fleet generation = %#v", contracts)
	}
	if contracts[0].SourceNodeID != "" || contracts[0].SourceVersion != "" {
		t.Fatalf("synthetic fleet contract should not claim one provider as source: %#v", contracts[0])
	}

	restarted := newGatewayTestServer(t, store)
	restarted.initializeMCPGateway()
	restored, ok := restarted.publishedToolBridge.Published("exec_command")
	if !ok || restored.ContractHash != published.ContractHash || !reflect.DeepEqual(restored.AcceptedSemanticHashes, published.AcceptedSemanticHashes) {
		t.Fatalf("restart did not restore full fleet generation: before=%#v after=%#v", published, restored)
	}
}

func TestNodeToolContractRequiresAcceptedVariantMembership(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	variantA := platformContractDescriptor("exec_command", map[string]any{
		"command": map[string]any{"type": "string"},
		"alpha":   map[string]any{"type": "string"},
	}, []any{"command"})
	variantB := platformContractDescriptor("exec_command", map[string]any{
		"command": map[string]any{"type": "string"},
		"beta":    map[string]any{"type": "string"},
	}, []any{"command"})
	nodeA := pairHTTPTestNode(t, store, "device_variant_a", "DockMini", "1.8.3", variantA)
	nodeB := pairHTTPTestNode(t, store, "device_variant_b", "DockWin", "1.9.0", variantB)
	server := newGatewayTestServer(t, store)
	server.registerNodeTools(nodeA, agentdock.Hello{Tools: []agentdock.ToolDescriptor{variantA}})
	server.registerNodeTools(nodeB, agentdock.Hello{Tools: []agentdock.ToolDescriptor{variantB}})

	published, _ := server.publishedToolBridge.Published("exec_command")
	publicHash, _ := agentdock.ToolContractHash(published.Descriptor)
	if slices.Contains(published.AcceptedSemanticHashes, publicHash) {
		t.Fatalf("test requires a synthetic public contract distinct from provider variants: %#v", published)
	}

	// 节点现场漂移成刚好等于 Fleet 并集，也不能因为公共 schema 能覆盖就绕过 generation membership。
	driftedNode := updateHTTPTestNodeContract(t, store, nodeA, "2.0.0", published.Descriptor)
	driftedHash, _ := agentdock.ToolContractHash(published.Descriptor)
	if driftedHash != published.ContractHash {
		t.Fatalf("drifted hash = %s, public hash = %s", driftedHash, published.ContractHash)
	}
	mismatch, err := server.publishedToolBridge.ToolContractMismatch(t.Context(), driftedNode, "exec_command")
	if err != nil {
		t.Fatal(err)
	}
	if mismatch == nil || mismatch.Code != "TOOL_CONTRACT_MISMATCH" {
		t.Fatalf("unaccepted live variant was not rejected: %#v", mismatch)
	}
}

func TestLoadPublishedNodeToolsSeedsLegacyAcceptedVariant(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	descriptor := platformContractDescriptor("exec_command", map[string]any{
		"command": map[string]any{"type": "string"},
	}, []any{"command"})
	if err := store.SavePublishedToolContract(t.Context(), agentdock.PublishedToolContract{
		ToolName: "exec_command", Descriptor: descriptor, SourceNodeID: "legacy_node", SourceVersion: "1.8.3",
	}); err != nil {
		t.Fatal(err)
	}
	server := newGatewayTestServer(t, store)
	if err := server.publishedToolBridge.LoadPublished(t.Context()); err != nil {
		t.Fatal(err)
	}
	published, ok := server.publishedToolBridge.Published("exec_command")
	if !ok {
		t.Fatal("legacy published contract was not restored")
	}
	hash, _ := agentdock.ToolContractHash(descriptor)
	if !reflect.DeepEqual(published.AcceptedSemanticHashes, []string{hash}) {
		t.Fatalf("legacy accepted hashes = %#v", published.AcceptedSemanticHashes)
	}
}
