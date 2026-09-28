package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	// Go 1.22+ ServeMux 的 GET pattern 同时接受 HEAD。Chi 默认不会自动回退，
	// 使用官方 GetHead 保持迁移前的 HTTP 行为，显式 HEAD 路由仍优先匹配。
	r.Use(middleware.GetHead)
	r.Use(s.requestBoundary)
	r.Use(s.securityHeaders)

	// 公开入口各自拥有业务层身份校验（签名 URL、配对码、Device Token 或 OAuth 协议），
	// 不继承管理员会话，避免把不同身份模型错误合并。
	r.Get("/health", s.health)
	r.Get("/ready", s.ready)
	r.Get("/artifacts/public/{nodeID}/{artifactID}/{filename}", s.servePublicArtifact)
	r.Head("/artifacts/public/{nodeID}/{artifactID}/{filename}", s.servePublicArtifact)
	r.Get("/oauth/mcp/nodes/{nodeID}/callback", s.mcpOAuthCallback)
	s.registerOAuthRoutes(r)
	s.registerAgentDockConnectionRoutes(r)
	s.registerWebAuthRoutes(r)

	if s.mcp != nil && s.mcp.handler != nil {
		r.Group(func(r chi.Router) {
			r.Use(s.withMCPAccess)
			r.Method(http.MethodGet, "/mcp", s.mcp.handler)
			r.Method(http.MethodPost, "/mcp", s.mcp.handler)
			r.Method(http.MethodDelete, "/mcp", s.mcp.handler)
		})
	}

	// 管理员域：新路由只要注册到这个分支就默认要求 Web Session，
	// 不再依赖每个 HandleFunc 手工套 protected，避免新增接口时静默漏鉴权。
	r.Group(func(r chi.Router) {
		r.Use(s.withAPIAccess)
		r.Get("/v1/system/status", s.systemStatus)
		r.Get("/v1/settings/ai", s.runtimeAI.getSettings)
		r.Get("/v1/settings/mcp", s.getMCPSettings)
		r.Put("/v1/settings/mcp", s.updateMCPSettings)
		r.Get("/v1/settings/mcp-token", s.getMCPAccessToken)
		r.Post("/v1/settings/mcp-token/reset", s.resetMCPAccessToken)
		r.Put("/v1/settings/ai", s.runtimeAI.updateSettings)
		r.Post("/v1/settings/ai/test/stage3", s.runtimeAI.testStage3Connection)
		r.Post("/v1/settings/ai/test/embedding", s.runtimeAI.testEmbeddingConnection)
		s.registerRuntimeRoutes(r)
		s.registerEvolutionAdminRoutes(r)
	})

	// 共享数据域：已启用的 AgentDock Device Token 与管理员 Web Session 均可访问。
	r.Group(func(r chi.Router) {
		r.Use(s.withDeviceOrAPIAccess)
		s.registerWorkflowTemplateRoutes(r)
		if s.privateNotes != nil {
			s.registerPrivateNoteRoutes(r)
		}
		s.registerRecallRoutes(r)
	})

	// 内部设备域：生命周期写操作只允许已配对且启用的 AgentDock。
	r.Group(func(r chi.Router) {
		r.Use(s.withEvolutionAccess)
		s.registerEvolutionInternalRoutes(r)
	})

	// UI 与 SPA fallback 最后注册。登录页所需 /ui/assets/* 已在公开路由中单独放行。
	r.With(s.withUIAccess).Get("/", s.uiIndex)
	r.With(s.withUIAccess).Get("/ui/*", s.uiIndex)
	r.Get("/v1/*", http.NotFound)
	r.Get("/api/*", http.NotFound)
	r.With(s.withUIAccess).Get("/*", s.uiIndex)

	return r
}

func withWildcardPathValue(name string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue(name, chi.URLParam(r, "*"))
		next(w, r)
	}
}

func (s *Server) registerRecallRoutes(r chi.Router) {
	r.Get("/v1/recall", s.listMemories)
	r.Post("/v1/recall", s.writeRecall)
	r.Post("/v1/recall/preview", s.previewRecall)
	r.Post("/v1/recall/move", s.moveRecall)
	r.Post("/v1/recall/search", s.searchMemories)
	r.Post("/v1/recall/context-index", s.contextIndexMemories)
	r.Get("/v1/recall/cards", s.listCards)
	r.Post("/v1/recall/cards", s.writeCard)
	r.Post("/v1/recall/cards/capture", s.captureCard)
	r.Post("/v1/recall/cards/search", s.searchCards)
	r.Get("/v1/embeddings/status", s.embeddingStatus)
	r.Post("/v1/embeddings/reindex", s.reindexEmbeddings)
	r.Post("/v1/embeddings/search", s.searchEmbeddings)
	r.Get("/v1/recall/*", withWildcardPathValue("path", s.readRecall))
	r.Patch("/v1/recall/*", withWildcardPathValue("path", s.patchRecall))
	r.Delete("/v1/recall/*", withWildcardPathValue("path", s.deleteRecall))
}
