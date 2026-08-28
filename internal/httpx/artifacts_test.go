package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
			"capabilities": []string{}, "bridge_capabilities": []string{protocol.ArtifactReadCapability}, "tools": []any{}, "ui_resources": []any{},
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
			if invoke["operation"] != protocol.OperationArtifactRead {
				serveDone <- fmt.Errorf("unexpected operation: %v", invoke["operation"])
				return
			}
			arguments, _ := invoke["arguments"].(map[string]any)
			offset := int64(arguments["offset"].(float64))
			maximum := int(arguments["max_bytes"].(float64))
			end := offset
			var data []byte
			if maximum > 0 {
				end = offset + int64(maximum)
				if end > int64(len(payload)) {
					end = int64(len(payload))
				}
				data = payload[offset:end]
			}
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
			if maximum > 0 && end == int64(len(payload)) {
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

	headResponse := httptest.NewRecorder()
	mux.ServeHTTP(headResponse, httptest.NewRequest(http.MethodHead, parsed.RequestURI(), nil))
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 || headResponse.Header().Get("Content-Length") != fmt.Sprint(len(payload)) {
		t.Fatalf("HEAD status=%d bytes=%d content-length=%q", headResponse.Code, headResponse.Body.Len(), headResponse.Header().Get("Content-Length"))
	}

	request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	request.Header.Set("Range", "bytes=0-31")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != string(payload) {
		t.Fatalf("download status=%d bytes=%d want=%d body_prefix=%q", response.Code, response.Body.Len(), len(payload), response.Body.String()[:min(response.Body.Len(), 100)])
	}
	if response.Header().Get("Content-Disposition") == "" || response.Header().Get("Access-Control-Allow-Origin") != "*" || response.Header().Get("Accept-Ranges") != "none" {
		t.Fatalf("download headers = %#v", response.Header())
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

func TestNexusArtifactStreamWithholdsCorruptFinalChunk(t *testing.T) {
	good := bytes.Repeat([]byte("a"), protocol.MaxArtifactChunkBytes+137)
	served := append([]byte(nil), good...)
	served[len(served)-1] = 'b'
	digest := sha256.Sum256(good)
	sha := hex.EncodeToString(digest[:])
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)

	hub, node, serveDone := startArtifactBridgeNode(t, served, sha, expiresAt, nil)
	server := &Server{cfg: config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: t.TempDir()}, agentDockHub: hub, logger: slog.Default()}
	publicURL, err := server.signedArtifactURL(node.ID, "artifact123", "report.txt", sha, expiresAt.Unix())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	downloadServer := httptest.NewServer(server.Handler())
	defer downloadServer.Close()

	response, err := http.Get(downloadServer.URL + parsed.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	if readErr == nil {
		t.Fatal("corrupt final chunk should abort the HTTP response")
	}
	if len(body) != protocol.MaxArtifactChunkBytes || !bytes.Equal(body, served[:protocol.MaxArtifactChunkBytes]) {
		t.Fatalf("bytes released before checksum failure = %d", len(body))
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

func TestArtifactDownloadConcurrencyLimitIsPerNode(t *testing.T) {
	server := &Server{
		cfg:          config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: t.TempDir()},
		agentDockHub: agentdock.NewHub(nil),
		logger:       slog.Default(),
	}
	const nodeID = "node_busy"
	if !server.acquireArtifactDownload(nodeID) || !server.acquireArtifactDownload(nodeID) {
		t.Fatal("first two downloads should acquire a node slot")
	}
	if server.acquireArtifactDownload(nodeID) {
		t.Fatal("third concurrent download unexpectedly acquired a node slot")
	}
	if !server.acquireArtifactDownload("node_other") {
		t.Fatal("a different node should have an independent download budget")
	}
	server.releaseArtifactDownload("node_other")

	expires := time.Now().UTC().Add(time.Hour).Unix()
	sha := strings.Repeat("a", 64)
	publicURL, err := server.signedArtifactURL(nodeID, "artifact1", "report.txt", sha, expires)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("busy status = %d, want 429", response.Code)
	}
	if response.Header().Get("Retry-After") != "5" || response.Header().Get("Access-Control-Allow-Origin") != "*" || response.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Fatalf("busy headers = %#v", response.Header())
	}
	server.releaseArtifactDownload(nodeID)
	server.releaseArtifactDownload(nodeID)
	if !server.acquireArtifactDownload(nodeID) {
		t.Fatal("released node slots were not reusable")
	}
	server.releaseArtifactDownload(nodeID)
}

func TestNexusArtifactDownloadSupportsEmptyPayload(t *testing.T) {
	payload := []byte{}
	digest := sha256.Sum256(payload)
	sha := hex.EncodeToString(digest[:])
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	hub, node, serveDone := startArtifactBridgeNode(t, payload, sha, expiresAt, nil)
	server := &Server{cfg: config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: t.TempDir()}, agentDockHub: hub, logger: slog.Default()}
	publicURL, err := server.signedArtifactURL(node.ID, "artifact123", "report.txt", sha, expiresAt.Unix())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	downloadServer := httptest.NewServer(server.Handler())
	defer downloadServer.Close()

	response, err := http.Get(downloadServer.URL + parsed.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(body) != 0 || response.Header.Get("Content-Length") != "0" {
		t.Fatalf("empty download status=%d bytes=%d content-length=%q", response.StatusCode, len(body), response.Header.Get("Content-Length"))
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

func TestRequestBoundaryPropagatesAbortHandler(t *testing.T) {
	server := &Server{logger: slog.Default()}
	handler := server.requestBoundary(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("recovered = %#v, want http.ErrAbortHandler", recovered)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/artifacts/public/test", nil))
}

func TestValidateArtifactChunkRejectsNonEOFFinalOffset(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	chunk := agentdock.ArtifactChunk{
		ArtifactID: "artifact123",
		Filename:   "report.txt",
		Size:       100,
		SHA256:     strings.Repeat("a", 64),
		ExpiresAt:  expiresAt,
		Offset:     50,
		NextOffset: 100,
		EOF:        false,
	}
	if err := validateArtifactChunk(chunk, chunk.ArtifactID, chunk.Filename, chunk.SHA256, expiresAt.Unix(), 50, false); err == nil || !strings.Contains(err.Error(), "end-of-stream marker") {
		t.Fatalf("non-EOF final offset error = %v", err)
	}
}

func TestNexusArtifactStreamRejectsCrossChunkSizeChange(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), protocol.MaxArtifactChunkBytes+137)
	digest := sha256.Sum256(payload)
	sha := hex.EncodeToString(digest[:])
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	hub, node, serveDone := startArtifactBridgeNode(t, payload, sha, expiresAt, func(result map[string]any, offset int64, maximum int) {
		if maximum > 0 && offset > 0 {
			result["size_bytes"] = int64(len(payload)) + 1
		}
	})
	server := &Server{cfg: config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: t.TempDir()}, agentDockHub: hub, logger: slog.Default()}
	publicURL, err := server.signedArtifactURL(node.ID, "artifact123", "report.txt", sha, expiresAt.Unix())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	downloadServer := httptest.NewServer(server.Handler())
	defer downloadServer.Close()

	response, err := http.Get(downloadServer.URL + parsed.RequestURI())
	if err != nil {
		if !errors.Is(err, io.EOF) {
			t.Fatalf("early abort error = %v, want EOF", err)
		}
	} else {
		defer response.Body.Close()
		body, readErr := io.ReadAll(response.Body)
		if readErr == nil {
			t.Fatal("cross-chunk size change should abort the HTTP response")
		}
		if len(body) != 0 {
			t.Fatalf("bytes released before size-change rejection = %d", len(body))
		}
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

func TestNexusArtifactDownloadStatusBoundaries(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		server := &Server{cfg: config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: t.TempDir()}, agentDockHub: agentdock.NewHub(nil), logger: slog.Default()}
		expires := time.Now().UTC().Add(-time.Minute).Unix()
		sha := strings.Repeat("a", 64)
		publicURL, err := server.signedArtifactURL("node_1", "artifact1", "report.txt", sha, expires)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(publicURL)
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
		request.SetPathValue("nodeID", "node_1")
		request.SetPathValue("artifactID", "artifact1")
		request.SetPathValue("filename", "report.txt")
		server.servePublicArtifact(response, request)
		if response.Code != http.StatusGone {
			t.Fatalf("expired status = %d, want 410", response.Code)
		}
	})

	t.Run("offline", func(t *testing.T) {
		server := &Server{cfg: config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: t.TempDir()}, agentDockHub: agentdock.NewHub(nil), logger: slog.Default()}
		expires := time.Now().UTC().Add(time.Hour).Unix()
		sha := strings.Repeat("a", 64)
		publicURL, err := server.signedArtifactURL("node_1", "artifact1", "report.txt", sha, expires)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(publicURL)
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
		request.SetPathValue("nodeID", "node_1")
		request.SetPathValue("artifactID", "artifact1")
		request.SetPathValue("filename", "report.txt")
		server.servePublicArtifact(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("offline status = %d, want 503", response.Code)
		}
	})

	t.Run("too_large", func(t *testing.T) {
		payload := bytes.Repeat([]byte("x"), protocol.MaxArtifactChunkBytes)
		digest := sha256.Sum256(payload)
		sha := hex.EncodeToString(digest[:])
		expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
		hub, node, serveDone := startArtifactBridgeNode(t, payload, sha, expiresAt, func(result map[string]any, _ int64, maximum int) {
			if maximum > 0 {
				result["size_bytes"] = int64(maxProxiedArtifactBytes) + 1
				result["eof"] = false
			}
		})
		server := &Server{cfg: config.Config{PublicURL: "https://nexus.example.test", NexusDataDir: t.TempDir()}, agentDockHub: hub, logger: slog.Default()}
		publicURL, err := server.signedArtifactURL(node.ID, "artifact123", "report.txt", sha, expiresAt.Unix())
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(publicURL)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /artifacts/public/{nodeID}/{artifactID}/{filename}", server.servePublicArtifact)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil))
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("too-large status = %d, want 413", response.Code)
		}
		if err := <-serveDone; err != nil {
			t.Fatal(err)
		}
	})
}

func startArtifactBridgeNode(t *testing.T, payload []byte, advertisedSHA string, expiresAt time.Time, mutate func(map[string]any, int64, int)) (*agentdock.Hub, agentdock.Node, <-chan error) {
	t.Helper()
	store := newHTTPTestAgentDockStore(t)
	pairing, err := store.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Pair(t.Context(), agentdock.PairInput{Code: pairing.Code, DeviceID: "device_artifact_script", Name: "DockWin"})
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
	t.Cleanup(bridge.Close)

	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(bridge.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if err := socket.WriteJSON(map[string]any{
		"type": protocol.MessageNodeHello, "protocol_version": agentdock.ConnectionProtocolVersion,
		"hello": map[string]any{
			"device_id": node.DeviceID, "protocol_version": agentdock.ConnectionProtocolVersion,
			"capabilities": []string{}, "bridge_capabilities": []string{protocol.ArtifactReadCapability}, "tools": []any{}, "ui_resources": []any{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := socket.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	<-connected

	serveDone := make(chan error, 1)
	go func() {
		for {
			var invoke map[string]any
			if err := socket.ReadJSON(&invoke); err != nil {
				serveDone <- err
				return
			}
			if invoke["operation"] != protocol.OperationArtifactRead {
				serveDone <- fmt.Errorf("unexpected operation: %v", invoke["operation"])
				return
			}
			arguments, _ := invoke["arguments"].(map[string]any)
			offset := int64(arguments["offset"].(float64))
			maximum := int(arguments["max_bytes"].(float64))
			end := offset
			var data []byte
			if maximum > 0 {
				end = offset + int64(maximum)
				if end > int64(len(payload)) {
					end = int64(len(payload))
				}
				data = payload[offset:end]
			}
			result := map[string]any{
				"artifact_id": "artifact123", "filename": "report.txt", "mime_type": "text/plain; charset=utf-8",
				"size_bytes": len(payload), "sha256": advertisedSHA, "created_at": time.Now().UTC(), "expires_at": expiresAt,
				"offset": offset, "next_offset": end, "data_base64": base64.StdEncoding.EncodeToString(data), "eof": end == int64(len(payload)),
			}
			if mutate != nil {
				mutate(result, offset, maximum)
			}
			encoded, _ := json.Marshal(result)
			if err := socket.WriteJSON(map[string]any{"type": protocol.MessageToolResult, "request_id": invoke["request_id"], "result": json.RawMessage(encoded)}); err != nil {
				serveDone <- err
				return
			}
			if maximum > 0 && end == int64(len(payload)) {
				serveDone <- nil
				return
			}
		}
	}()
	return hub, node, serveDone
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
