package httpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/skillcatalog"
)

// 可选集成验收：使用官方 AgentDock 二进制与独立状态目录，真实运行两个节点。
// 不复用用户节点的令牌、端口、已安装技能或运行配置。
func TestCatalogRealAgentDockNodes(t *testing.T) {
	binary := os.Getenv("NEXUS_TEST_AGENTDOCK_BINARY")
	if binary == "" {
		t.Skip("set NEXUS_TEST_AGENTDOCK_BINARY for real-node integration")
	}
	s := newNodeTestServer(t)
	s.logger = slog.Default()
	s.skillCatalog = newSkillCatalog(t.TempDir())
	s.publishedToolBridge = agentdock.NewPublishedToolBridge(s.agentDock, s.logger)
	s.initializeMCPGateway()
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	s.cfg.PublicURL = server.URL
	e, err := s.skillCatalog.store.Put(catalogTestZIP(t, "1.0.0", "CATALOG_REAL_NODE_OK"), skillcatalog.Metadata{Portability: "general", ReviewNote: "Fixture contains only a literal text marker and no environment-specific instructions."})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		root := t.TempDir()
		env := []string{}
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "AGENTDOCK_") {
				env = append(env, v)
			}
		}
		env = append(env, "AGENTDOCK_HOME="+filepath.Join(root, "state"), "AGENTDOCK_DEFAULT_DIR="+filepath.Join(root, "workspace"), "AGENTDOCK_BROWSER_ENABLED=false", "AGENTDOCK_ACP_ENABLED=false")
		pairing, err := s.agentDock.CreatePairingCode(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("catalog-test-%d", i)
		pair := exec.CommandContext(t.Context(), binary, "nexus", "pair", "--endpoint", server.URL, "--code", pairing.Code, "--name", name)
		pair.Env = env
		if err := pair.Run(); err != nil {
			t.Fatalf("isolated node pairing failed: %v", err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		cmd := exec.CommandContext(t.Context(), binary, "-host", "127.0.0.1", "-port", strconv.Itoa(port), "-log-level", "error")
		cmd.Env = env
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		var nodeID string
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			nodes, err := s.agentDock.List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range nodes {
				if n.Name == name && s.agentDockHub.Online(n.ID) {
					nodeID = n.ID
				}
			}
			if nodeID != "" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if nodeID == "" {
			t.Fatal("isolated node did not connect")
		}
		call := func(action, digest string) *httptest.ResponseRecorder {
			b, _ := json.Marshal(catalogNodeRequest{Action: action, Digest: digest})
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
			r.SetPathValue("name", e.Name)
			r.SetPathValue("version", e.Version)
			r.SetPathValue("nodeID", nodeID)
			w := httptest.NewRecorder()
			s.distributeSkillCatalog(w, r)
			return w
		}
		w := call("validate", "")
		if w.Code != 200 {
			t.Fatalf("real validate: %d %s", w.Code, w.Body.String())
		}
		var validation struct {
			Digest string `json:"digest"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &validation); err != nil {
			t.Fatal(err)
		}
		w = call("install", "wrong-digest")
		if w.Code != 409 {
			t.Fatalf("wrong digest accepted: %d", w.Code)
		}
		w = call("install", validation.Digest)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "installed_dependencies_unverified") {
			t.Fatalf("real install: %d %s", w.Code, w.Body.String())
		}
		doc, err := s.agentDockHub.RuntimeSkillFile(t.Context(), nodeID, e.Name, "SKILL.md")
		if err != nil || !strings.Contains(doc.Content, "CATALOG_REAL_NODE_OK") {
			t.Fatalf("installed document readback: %v", err)
		}
		t.Logf("real node %d: validate, digest rejection, install, active version and document readback passed", i+1)
	}
}
