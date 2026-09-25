package agentdock

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	protocol "github.com/uvwt/agentdock-protocol"
)

func TestHubInvokesConnectedNode(t *testing.T) {
	store, _ := newTestStore(t)
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), PairInput{Code: pairing.Code, DeviceID: "device_12345678", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	hub := NewHub(store)
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, node.ID, "https://nexus.example.test/"); err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		close(connected)
	}))
	defer server.Close()

	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if err := socket.WriteJSON(connectionMessage{Type: protocol.MessageNodeHello, ProtocolVersion: ConnectionProtocolVersion, Hello: &Hello{
		DeviceID: node.DeviceID, ProtocolVersion: ConnectionProtocolVersion, Capabilities: []string{"read_file"}, UIResources: []UIResourceCapability{},
	}}); err != nil {
		t.Fatal(err)
	}
	var ready connectionMessage
	if err := socket.ReadJSON(&ready); err != nil || ready.Type != protocol.MessageNodeReady {
		t.Fatalf("ready=%#v err=%v", ready, err)
	}
	if ready.PublicURL != "https://nexus.example.test" {
		t.Fatalf("ready public_url = %q", ready.PublicURL)
	}
	<-connected

	done := make(chan error, 1)
	go func() {
		var invoke connectionMessage
		if err := socket.ReadJSON(&invoke); err != nil {
			done <- err
			return
		}
		done <- socket.WriteJSON(connectionMessage{Type: protocol.MessageToolResult, RequestID: invoke.RequestID, Result: []byte(`{"ok":true}`)})
	}()
	result, err := hub.Invoke(context.Background(), node.ID, "tool.call", map[string]any{"tool": "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true {
		t.Fatalf("result = %#v", result)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHubRejectsStructurallyInvalidBridgeV2UIResourceHandshake(t *testing.T) {
	tests := []struct {
		name  string
		hello map[string]any
	}{
		{
			name: "missing ui_resources",
			hello: map[string]any{
				"protocol_version": ConnectionProtocolVersion,
				"tools":            []any{},
			},
		},
		{
			name: "malformed renderer URI",
			hello: map[string]any{
				"protocol_version": ConnectionProtocolVersion,
				"tools":            []any{},
				"ui_resources": []any{map[string]any{
					"uri": "https://example.test/widget", "contract": "future.v1", "mime_type": "text/html",
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			pairing, err := store.CreatePairingCode(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			node, err := store.Pair(t.Context(), PairInput{Code: pairing.Code, DeviceID: "device_reject_" + strings.ReplaceAll(tt.name, " ", "_"), Name: "DockMini"})
			if err != nil {
				t.Fatal(err)
			}
			hub := NewHub(store)
			acceptErr := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				acceptErr <- hub.Accept(w, r, node.ID, "")
			}))
			defer server.Close()

			socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer socket.Close()
			hello := make(map[string]any, len(tt.hello)+2)
			for key, value := range tt.hello {
				hello[key] = value
			}
			hello["device_id"] = node.DeviceID
			if err := socket.WriteJSON(map[string]any{
				"type": protocol.MessageNodeHello, "protocol_version": ConnectionProtocolVersion, "hello": hello,
			}); err != nil {
				t.Fatal(err)
			}

			_ = socket.SetReadDeadline(time.Now().Add(time.Second))
			var ready connectionMessage
			if err := socket.ReadJSON(&ready); err == nil {
				t.Fatalf("invalid handshake unexpectedly received ready: %#v", ready)
			}
			select {
			case err := <-acceptErr:
				var validation ValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("accept error = %#v, want ValidationError", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Accept did not reject invalid Bridge v2 handshake")
			}
			if hub.Online(node.ID) {
				t.Fatal("invalid Bridge v2 handshake marked node online")
			}
		})
	}
}
