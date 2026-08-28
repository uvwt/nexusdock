package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/config"
)

func TestNexusSignedArtifactURLStreamsFromConnectedNode(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), agentdock.PairInput{Code: pairing.Code, DeviceID: "device_artifact_proxy", Name: "DockWin"})
	if err != nil {
		t.Fatal(err)
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
	if err := socket.WriteJSON(map[string]any{
		"type": protocol.MessageNodeHello, "protocol_version": agentdock.ConnectionProtocolVersion,
		"hello": map[string]any{
			"device_id": node.DeviceID, "protocol_version": agentdock.ConnectionProtocolVersion,
			"capabilities": []string{agentdock.ArtifactReadCapability}, "tools": []any{}, "ui_resources": []any{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := socket.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	<-connected

	payload := []byte(strings.Repeat("Nexus Artifact 分块内容。", 40000))
	digest := sha256.Sum256(payload)
	sha := hex.EncodeToString(digest[:])
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	serveDone := make(chan error, 1)
	go func() {
		for {
			var invoke map[string]any
			if err := socket.ReadJSON(&invoke); err != nil {
				serveDone <- err
				return
			}
			if invoke["operation"] != agentdock.OperationArtifactRead {
				serveDone <- fmt.Errorf("unexpected operation: %v", invoke["operation"])
				return
			}
			arguments, _ := invoke["arguments"].(map[string]any)
			offset := int64(arguments["offset"].(float64))
			maximum := int(arguments["max_bytes"].(float64))
			end := offset + int64(maximum)
			if maximum == 0 || end > int64(len(payload)) {
				end = int64(len(payload))
			}
			data := payload[offset:end]
			result := map[string]any{
				"artifact_id": "artifact123", "filename": "report.txt", "mime_type": "text/plain; charset=utf-8",
				"size_bytes": len(payload), "sha256": sha, "created_at": time.Now().UTC(), "expires_at": expiresAt,
				"offset": offset, "next_offset": end, "data_base64": base64.StdEncoding.EncodeToString(data), "eof": end == int64(len(payload)),
			}
			encoded, _ := json.Marshal(result)
			if err := socket.WriteJSON(map[string]any{
				"type": protocol.MessageToolResult, "request_id": invoke["request_id"], "result": json.RawMessage(encoded),
			}); err != nil {
				serveDone <- err
				return
			}
			if end == int64(len(payload)) {
				serveDone <- nil
				return
			}
		}
	}()

	server := &Server{
		cfg:          config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: t.TempDir()},
		agentDockHub: hub, logger: slog.Default(),
	}
	envelope := map[string]any{
		"isError": false,
		"structuredContent": map[string]any{
			"artifact_id": "artifact123", "filename": "report.txt", "mime_type": "text/plain; charset=utf-8",
			"size_bytes": len(payload), "sha256": sha, "expires_at": expiresAt.Format(time.RFC3339Nano),
		},
		"content": []any{map[string]any{"type": "text", "text": "without URL"}},
	}
	if err := server.decorateArtifactToolResult(node.ID, envelope); err != nil {
		t.Fatal(err)
	}
	structured := envelope["structuredContent"].(map[string]any)
	publicURL, _ := structured["url"].(string)
	if !strings.HasPrefix(publicURL, "https://nexus.example.test/artifacts/public/") || structured["download_via"] != "nexusdock" {
		t.Fatalf("decorated result = %#v", structured)
	}
	if text := envelope["content"].([]any)[0].(map[string]any)["text"].(string); !strings.Contains(text, "https://nexus.example.test/artifacts/public/") {
		t.Fatalf("text content did not receive signed URL: %s", text)
	}

	parsed, err := url.Parse(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /artifacts/public/{nodeID}/{artifactID}/{filename}", server.servePublicArtifact)
	request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != string(payload) {
		t.Fatalf("download status=%d bytes=%d want=%d body_prefix=%q", response.Code, response.Body.Len(), len(payload), response.Body.String()[:min(response.Body.Len(), 100)])
	}
	if response.Header().Get("Content-Disposition") == "" || response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("download headers = %#v", response.Header())
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

func TestNexusSignedArtifactURLRejectsTampering(t *testing.T) {
	server := &Server{
		cfg:          config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: t.TempDir()},
		agentDockHub: agentdock.NewHub(nil), logger: slog.Default(),
	}
	expires := time.Now().UTC().Add(time.Hour).Unix()
	sha := strings.Repeat("a", 64)
	publicURL, err := server.signedArtifactURL("node_1", "artifact1", "report.txt", sha, expires)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(publicURL)
	query := parsed.Query()
	query.Set("sha256", strings.Repeat("b", 64))
	parsed.RawQuery = query.Encode()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /artifacts/public/{nodeID}/{artifactID}/{filename}", server.servePublicArtifact)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, parsed.RequestURI(), nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("tampered signed URL status = %d, want 404", response.Code)
	}
}
