package httpx

import (
	"net/http"
	"os"
	"testing"

	"github.com/uvwt/nexusdock/internal/config"
)

// 手工浏览器验收的隔离实例，仅在显式设置环境变量时启用，固定回环监听。
// 使用真实 Handler 和嵌入式页面；不连接生产节点，不更改生产管理员账号。
func TestCatalogBrowserFixture(t *testing.T) {
	if os.Getenv("NEXUS_CATALOG_BROWSER_TEST") != "1" {
		t.Skip("manual browser fixture")
	}
	handler := newTestHandler(t, config.Config{NexusDataDir: t.TempDir()})
	mux := http.NewServeMux()
	// 页面会先读取当前会话；该夹具沿用 newTestHandler 的回环测试认证边界。
	mux.HandleFunc("GET /v1/auth/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "session": map[string]any{"username": "local-test"}})
	})
	mux.Handle("/", handler)
	t.Log("Catalog browser fixture: http://127.0.0.1:19876/#skills")
	server := &http.Server{Addr: "127.0.0.1:19876", Handler: mux}
	t.Cleanup(func() { server.Close() })
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
