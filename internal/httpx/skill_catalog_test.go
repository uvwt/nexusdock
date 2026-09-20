package httpx

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/skillcatalog"
)

func catalogTestZIP(t *testing.T, version, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, err := z.Create("sample/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "---\nname: sample\nversion: "+version+"\ndescription: Example portable Skill\n---\n"+body); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestCatalogUploadHistoryDownloadAndConflict(t *testing.T) {
	h := newTestHandler(t, config.Config{NexusDataDir: t.TempDir()})
	upload := func(data []byte) *httptest.ResponseRecorder {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		f, err := mw.CreateFormFile("package", "skill.zip")
		if err != nil {
			t.Fatal(err)
		}
		f.Write(data)
		mw.Close()
		r := httptest.NewRequest("POST", "/v1/skill-catalog", &b)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	original := catalogTestZIP(t, "1.0.0", "Original procedure")
	for _, data := range [][]byte{original, original, catalogTestZIP(t, "1.1.0", "New procedure")} {
		w := upload(data)
		if w.Code != 201 {
			t.Fatalf("upload %d %s", w.Code, w.Body.String())
		}
	}
	if w := upload(catalogTestZIP(t, "1.0.0", "Different procedure")); w.Code != 409 {
		t.Fatalf("conflict %d %s", w.Code, w.Body.String())
	}
	list := doJSON(t, h, "GET", "/v1/skill-catalog", "")
	var entries struct {
		Items []skillcatalog.Entry `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &entries); err != nil || len(entries.Items) != 2 {
		t.Fatalf("history %s %v", list.Body.String(), err)
	}
	w := doJSON(t, h, "GET", "/v1/skill-catalog/sample/1.0.0/download", "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), original) {
		t.Fatalf("download %d", w.Code)
	}
}
func TestCatalogManagementRequiresAdminAndTicketsExpire(t *testing.T) {
	s := newNodeTestServer(t)
	s.skillCatalog = newSkillCatalog(t.TempDir())
	h := s.Handler()
	for _, route := range []string{"/v1/skill-catalog", "/v1/skill-catalog/sample/1.0.0/download"} {
		r := httptest.NewRequest("GET", route, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("unprotected %s: %d", route, w.Code)
		}
	}
	_, err := s.skillCatalog.store.Put(catalogTestZIP(t, "1.0.0", "Procedure"), skillcatalog.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	s.skillCatalog.tickets["expired"] = skillDownloadTicket{"sample", "1.0.0", time.Now().Add(-time.Minute)}
	for _, ticket := range []string{"missing", "expired"} {
		w := httptest.NewRecorder()
		s.downloadSkillTicket(w, httptest.NewRequest("GET", "/skill-packages/download?ticket="+ticket, nil))
		if w.Code != 403 {
			t.Fatalf("ticket %s: %d", ticket, w.Code)
		}
	}
}

func TestCatalogDistributesVerifiedZIPOverReverseConnection(t *testing.T) {
	store := newHTTPTestAgentDockStore(t)
	s := newGatewayTestServer(t, store)
	s.logger = slog.Default()
	s.skillCatalog = newSkillCatalog(t.TempDir())
	descriptor := agentdock.ToolDescriptor{Name: "skill_package", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
	node := pairHTTPTestNode(t, store, "catalog-test-node", "Test node", "0.8.3", descriptor)
	s.registerNodeTools(node, agentdock.Hello{Tools: []agentdock.ToolDescriptor{descriptor}})
	connected := make(chan struct{})
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.agentDockHub.Accept(w, r, node.ID); err != nil {
			t.Error(err)
		}
		close(connected)
	}))
	defer bridge.Close()
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(bridge.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if err := socket.WriteJSON(protocol.Message{Type: protocol.MessageNodeHello, ProtocolVersion: agentdock.ConnectionProtocolVersion, Hello: &protocol.Hello{DeviceID: node.DeviceID, ProtocolVersion: agentdock.ConnectionProtocolVersion, Version: "0.8.3", Capabilities: []string{"skill_package"}, Tools: []protocol.ToolDescriptor{descriptor}, UIResources: []protocol.UIResourceCapability{}}}); err != nil {
		t.Fatal(err)
	}
	var ready protocol.Message
	if err := socket.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	<-connected
	download := httptest.NewServer(http.HandlerFunc(s.downloadSkillTicket))
	defer download.Close()
	s.cfg.PublicURL = download.URL
	entry, err := s.skillCatalog.store.Put(catalogTestZIP(t, "1.0.0", "Read a file."), skillcatalog.Metadata{Portability: "general", ReviewNote: "Read-only plain-text fixture; no machine-specific tools."})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + entry.InstallSHA256
	served := make(chan error, 1)
	var sources []string
	go func() {
		// Explicit validate, revalidate install, install, then active-version readback.
		for step := 0; step < 4; step++ {
			var msg protocol.Message
			if err := socket.ReadJSON(&msg); err != nil {
				served <- err
				return
			}
			var args struct {
				Tool      string                                  `json:"tool"`
				Arguments struct{ Action, Source, Digest string } `json:"arguments"`
			}
			json.Unmarshal(msg.Arguments, &args)
			var payload map[string]any
			if step < 3 {
				resp, err := http.Get(args.Arguments.Source)
				if err != nil {
					served <- err
					return
				}
				b, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					served <- err
					return
				}
				sum := sha256.Sum256(b)
				if resp.StatusCode != 200 || hex.EncodeToString(sum[:]) != entry.InstallSHA256 {
					served <- io.ErrUnexpectedEOF
					return
				}
				z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
				if err != nil || z.File[0].Name != "SKILL.md" {
					served <- io.ErrUnexpectedEOF
					return
				}
				sources = append(sources, args.Arguments.Source)
				if step == 2 && args.Arguments.Digest != digest {
					served <- io.ErrUnexpectedEOF
					return
				}
				payload = map[string]any{"valid": true, "digest": digest}
				if step == 2 {
					payload = map[string]any{"action": "install", "result": map[string]any{"Activated": true}}
				}
			} else {
				payload = map[string]any{"skill": "sample", "selection": map[string]any{"active_version": "1.0.0"}, "versions": []string{"1.0.0"}, "files": []any{}}
			}
			b, _ := json.Marshal(payload)
			if err := socket.WriteJSON(protocol.Message{Type: protocol.MessageToolResult, RequestID: msg.RequestID, Result: b}); err != nil {
				served <- err
				return
			}
		}
		served <- nil
	}()
	call := func(action, d string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(catalogNodeRequest{Action: action, Digest: d})
		r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
		r.SetPathValue("name", "sample")
		r.SetPathValue("version", "1.0.0")
		r.SetPathValue("nodeID", node.ID)
		w := httptest.NewRecorder()
		s.distributeSkillCatalog(w, r)
		return w
	}
	w := call("validate", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "validated") {
		t.Fatalf("validate %d %s", w.Code, w.Body.String())
	}
	w = call("install", digest)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "installed_dependencies_unverified") {
		t.Fatalf("install %d %s", w.Code, w.Body.String())
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		resp, err := http.Get(source)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatal("download ticket survived operation")
		}
	}
}
