package e2e_test

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/httpx"
	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/syncer"
)

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	root := t.TempDir()
	store, err := recall.NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	manager := syncer.NewManager(syncer.Config{RepoDir: root}, slog.Default())
	return httpx.NewServer(config.Config{StoreDir: root}, store, manager, slog.Default()).Handler()
}

func TestHealthAndEmbeddedNexusUI(t *testing.T) {
	handler := newHandler(t)

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"ok":true`) {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}

	ui := httptest.NewRecorder()
	handler.ServeHTTP(ui, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if ui.Code != http.StatusOK {
		t.Fatalf("ui status=%d body=%s", ui.Code, ui.Body.String())
	}
	if !strings.Contains(ui.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("ui index does not contain application mount point: %s", ui.Body.String())
	}
	if !strings.Contains(ui.Body.String(), `<title>NexusDock</title>`) {
		t.Fatalf("ui index still exposes legacy title: %s", ui.Body.String())
	}
}

func TestRecallAPIWorks(t *testing.T) {
	handler := newHandler(t)

	create := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/recall", strings.NewReader(`{"path":"recall/docs/inbox/e2e.md","content":"# E2E\n\nRecall API is canonical."}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(create, request)
	if create.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/v1/recall/recall/docs/inbox/e2e.md", nil))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "Recall API is canonical") {
		t.Fatalf("read status=%d body=%s", read.Code, read.Body.String())
	}
}

func TestBuiltFrontendContainsNexusSectionsAndResponsiveRules(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "httpx", "web_dist")
	var javascript strings.Builder
	var styles strings.Builder
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		switch filepath.Ext(path) {
		case ".js":
			javascript.Write(data)
		case ".css":
			styles.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk frontend dist: %v", err)
	}
	for _, label := range []string{"AgentDock", "Nexus", "总览", "Recall", "运行时", "MCP 服务", "添加 MCP", "隔离环境", "设置", "个人控制台", "数据库", "拒绝跨源 API 请求", "INVALID_JSON"} {
		if !strings.Contains(javascript.String(), label) {
			t.Errorf("frontend bundle missing section label %q", label)
		}
	}
	for _, rule := range []string{"nexus-sidebar.is-open", "grid-template-columns:1fr", "nexus-mobile-menu", "nexus-scrim", "settings-grid", "mcp-layout", "mcp-env-form"} {
		if !strings.Contains(styles.String(), rule) {
			t.Errorf("frontend stylesheet missing responsive rule %q", rule)
		}
	}
}
