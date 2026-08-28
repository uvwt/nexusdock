package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/config"
)

func TestCallNodeToolKeepsSuccessWhenArtifactDecorationFails(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), agentdock.PairInput{Code: pairing.Code, DeviceID: "device_decorate_fail", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := agentdock.ToolDescriptor{
		Name:        "file_publish",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	hub := agentdock.NewHub(store)
	connected := make(chan struct{})
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, node.ID); err != nil {
			t.Errorf("accept node: %v", err)
			return
		}
		close(connected)
	}))
	defer bridge.Close()

	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(bridge.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if err := socket.WriteJSON(protocol.Message{
		Type: protocol.MessageNodeHello, ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Hello: &protocol.Hello{
			DeviceID: node.DeviceID, ProtocolVersion: agentdock.ConnectionProtocolVersion,
			Capabilities:       []string{descriptor.Name},
			BridgeCapabilities: []string{protocol.ArtifactReadCapability},
			Tools:              []protocol.ToolDescriptor{descriptor},
			UIResources:        []protocol.UIResourceCapability{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var ready protocol.Message
	if err := socket.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	<-connected

	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	serveDone := make(chan error, 1)
	go func() {
		var invoke protocol.Message
		if err := socket.ReadJSON(&invoke); err != nil {
			serveDone <- err
			return
		}
		if invoke.Operation != protocol.OperationToolCall {
			serveDone <- &unexpectedOperationError{got: invoke.Operation}
			return
		}
		envelope := map[string]any{
			"isError": false,
			"structuredContent": map[string]any{
				"artifact_id": "artifact123",
				"filename":    "result.txt",
				"sha256":      strings.Repeat("a", 64),
				"expires_at":  expiresAt.Format(time.RFC3339Nano),
			},
			"content": []any{map[string]any{"type": "text", "text": "published"}},
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			serveDone <- err
			return
		}
		serveDone <- socket.WriteJSON(protocol.Message{Type: protocol.MessageToolResult, RequestID: invoke.RequestID, Result: encoded})
	}()

	badDataDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badDataDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg:          config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: badDataDir},
		agentDock:    store,
		agentDockHub: hub,
		mcpServer:    mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil),
		mcpTools:     make(map[string]publishedNodeTool),
		mcpResources: make(map[string]struct{}),
		logger:       slog.Default(),
	}
	server.registerNodeTools(node, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})

	result, err := server.callNodeTool(t.Context(), descriptor.Name, map[string]any{"node_id": node.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("decoration failure changed successful tool call into MCP error: %#v", result.StructuredContent)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %#v", result.StructuredContent)
	}
	if _, exists := structured["url"]; exists {
		t.Fatalf("failed decoration unexpectedly added URL: %#v", structured)
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

type unexpectedOperationError struct{ got string }

func (e *unexpectedOperationError) Error() string { return "unexpected operation: " + e.got }
