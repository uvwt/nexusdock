package httpx

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/auth"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/observability"
	"github.com/uvwt/nexusdock/internal/privatenotes"
	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/settings"
	"github.com/uvwt/nexusdock/internal/stage3"
	"github.com/uvwt/nexusdock/internal/workflow"
)

const maxJSONRequestBytes = 2 << 20

var requestSequence atomic.Uint64

type requestIDContextKey struct{}

type trackedResponseWriter struct {
	http.ResponseWriter
	requestID   string
	statusCode  int
	wroteHeader bool
}

func (w *trackedResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = statusCode
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *trackedResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *trackedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *trackedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support WebSocket hijacking")
	}
	return hijacker.Hijack()
}

func (w *trackedResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type Server struct {
	mcpAppsMu           sync.RWMutex
	cfg                 config.Config
	runtimeAI           runtimeAIState
	access              accessControl
	mcpAppsEnabledState bool
	tracing             *observability.Tracing
	db                  *sql.DB
	store               *recall.Store
	privateNotes        *privatenotes.Store
	agentDock           *agentdock.Store
	agentDockHub        *agentdock.Hub
	logger              *slog.Logger
	mcpSettings         *settings.MCPStore
	workflowRegistry    *workflow.Registry
	publishedToolBridge *agentdock.PublishedToolBridge
	mcp                 *mcpGateway
	artifacts           *agentdock.ArtifactService
}

type ServerOption func(*Server)

func WithSystemDatabase(db *sql.DB) ServerOption {
	return func(server *Server) { server.db = db }
}

func WithTracing(tracing *observability.Tracing) ServerOption {
	return func(server *Server) { server.tracing = tracing }
}

// WithAgentDockNodes 注入组合根创建的节点存储与连接 Hub；
// Hub 由组合根持有，保证 REST、MCP 网关与后台 Worker 共享同一批节点连接。
func WithAgentDockNodes(store *agentdock.Store, hub *agentdock.Hub) ServerOption {
	return func(server *Server) {
		server.agentDock = store
		server.agentDockHub = hub
	}
}

func WithWebAuthentication(authService *auth.Service) ServerOption {
	return func(server *Server) { server.access.auth = authService }
}

func WithEmbeddingService(service *recall.EmbeddingService) ServerOption {
	return func(server *Server) { server.runtimeAI.embedding = service }
}

func WithRuntimeSettings(store *settings.Store) ServerOption {
	return func(server *Server) { server.runtimeAI.settingsStore = store }
}

func WithRuntimeAIConfig(cfg settings.RuntimeAIConfig) ServerOption {
	return func(server *Server) { server.runtimeAI.config = cfg }
}

func WithMCPSettings(store *settings.MCPStore) ServerOption {
	return func(server *Server) { server.mcpSettings = store }
}

func WithMCPAppsEnabled(enabled bool) ServerOption {
	return func(server *Server) { server.mcpAppsEnabledState = enabled }
}

func WithPrivateNotes(store *privatenotes.Store) ServerOption {
	return func(server *Server) { server.privateNotes = store }
}

func WithMCPTokenStore(store *auth.MCPTokenStore) ServerOption {
	return func(server *Server) { server.access.mcpToken = store }
}

// WithWorkflowRegistry 注入组合根创建的 Workflow 模板注册表；
// REST 与集中式 MCP workflow 工具共用同一实例。
func WithWorkflowRegistry(registry *workflow.Registry) ServerOption {
	return func(server *Server) { server.workflowRegistry = registry }
}

// WithEvolutionWorker 注入组合根拥有的 Stage 3 进化 Worker；
// HTTP 层只在运行期 AI 设置保存成功后唤醒它，不参与调度与执行。
func WithEvolutionWorker(worker *stage3.Worker) ServerOption {
	return func(server *Server) { server.runtimeAI.evolutionWorker = worker }
}

// WithPublishedToolBridge 注入组合根创建的节点工具契约 Bridge；
// Bridge 维护 fleet 公开契约的业务状态，HTTP 层只负责把它映射为 MCP SDK 的工具注册。
func WithPublishedToolBridge(bridge *agentdock.PublishedToolBridge) ServerOption {
	return func(server *Server) { server.publishedToolBridge = bridge }
}

// WithArtifactService 注入组合根创建的 Artifact 能力（签名密钥与每节点下载预算）；
// HTTP 层只保留下载路由、URL 拼装与响应映射。
func WithArtifactService(service *agentdock.ArtifactService) ServerOption {
	return func(server *Server) { server.artifacts = service }
}

func NewServer(cfg config.Config, store *recall.Store, logger *slog.Logger, options ...ServerOption) *Server {
	server := &Server{
		cfg: cfg, runtimeAI: newRuntimeAIState(store), access: newAccessControl(cfg.TrustedProxies, logger), mcpAppsEnabledState: settings.DefaultMCPAppsEnabled,
		store: store, logger: logger,
	}
	for _, option := range options {
		option(server)
	}
	if server.db != nil && server.access.auth != nil {
		server.access.oauth = auth.NewOAuthService(server.db)
		server.access.oauthRegisterLimiter = newFixedWindowLimiter(30, time.Minute)
	}
	server.initializeMCPGateway()
	return server
}

func (s *Server) requestBoundary(next http.Handler) http.Handler {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := newRequestID()
		tracked := &trackedResponseWriter{ResponseWriter: w, requestID: requestID}
		tracked.Header().Set("X-Request-ID", requestID)
		started := time.Now()
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				logger.Error("http handler panic", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "panic", recovered, "stack", string(debug.Stack()))
				if !tracked.wroteHeader {
					writeError(tracked, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
				}
			}
			statusCode := tracked.statusCode
			if statusCode == 0 {
				statusCode = http.StatusOK
			}
			logger.Debug("http request", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", statusCode, "duration", time.Since(started))
		}()
		next.ServeHTTP(tracked, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "req_" + hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("req_%x_%x", time.Now().UnixNano(), requestSequence.Add(1))
}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func requestIDFromWriter(w http.ResponseWriter) string {
	for current := w; current != nil; {
		if tracked, ok := current.(*trackedResponseWriter); ok {
			return tracked.requestID
		}
		unwrapper, ok := current.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		current = unwrapper.Unwrap()
	}
	return ""
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'")
		headers.Set("Cross-Origin-Opener-Policy", "same-origin")
		headers.Set("Cross-Origin-Resource-Policy", "same-origin")
		headers.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		headers.Set("Referrer-Policy", "no-referrer")
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/internal/") || r.URL.Path == "/login" || r.URL.Path == "/change-password" {
			headers.Set("Cache-Control", "no-store")
		}
		if r.TLS != nil || s.access.isTrustedProxy(r) && strings.EqualFold(lastForwardedValue(r.Header.Get("X-Forwarded-Proto")), "https") {
			headers.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// health 是 liveness 探针：进程能响应即存活，不做任何下游依赖检查，保持极轻量。
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "nexusdock"})
}

// ready 是 readiness 探针：控制库可执行查询且 Recall 根目录可访问才算就绪。
// 检查保持轻量（SELECT 1 + Stat），完整完整性检查只在启动时执行一次，
// 避免周期探针本身变成负载。公开响应只返回稳定组件状态，底层数据库错误与
// 宿主路径只写服务日志，避免故障时通过未认证探针泄露内部实现细节。
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]string, 2)
	if s.db == nil {
		checks["database"] = "unavailable"
	} else {
		var one int
		if err := s.db.QueryRowContext(r.Context(), `SELECT 1`).Scan(&one); err != nil {
			checks["database"] = "unavailable"
			if s.logger != nil {
				s.logger.Warn("readiness check failed", "component", "database", "error", err)
			}
		}
	}
	if s.store == nil {
		checks["recall"] = "unavailable"
	} else {
		info, err := os.Stat(s.store.Root())
		if err != nil {
			checks["recall"] = "unavailable"
			if s.logger != nil {
				s.logger.Warn("readiness check failed", "component", "recall", "error", err)
			}
		} else if !info.IsDir() {
			checks["recall"] = "unavailable"
			if s.logger != nil {
				s.logger.Warn("readiness check failed", "component", "recall", "error", "repository root is not a directory")
			}
		}
	}
	if len(checks) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "service": "nexusdock", "checks": checks})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "nexusdock"})
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.List(r.URL.Query().Get("prefix"), queryInt(r, "max_entries", 200))
	if err != nil {
		writeError(w, http.StatusBadRequest, "LIST_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entries": entries, "count": len(entries), "root": s.store.Root()})
}

func (s *Server) readRecall(w http.ResponseWriter, r *http.Request) {
	path, err := memoryPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PATH", err.Error())
		return
	}
	mem, err := s.store.Read(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "READ_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recall": mem})
}

func (s *Server) previewRecall(w http.ResponseWriter, r *http.Request) {
	var req recall.WriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	preview, err := s.store.PreviewWrite(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PREVIEW_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "path": preview.Path, "proposed_content": preview.ProposedContent,
		"overwrite": preview.Overwrite, "dry_run": true, "confirmed": req.Confirmed,
	})
}

func (s *Server) writeRecall(w http.ResponseWriter, r *http.Request) {
	var req recall.WriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	mem, err := s.store.Write(req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, recall.ErrFileExists) {
			status = http.StatusConflict
		}
		writeError(w, status, "WRITE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recall": mem})
}

func (s *Server) patchRecall(w http.ResponseWriter, r *http.Request) {
	path, err := memoryPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PATH", err.Error())
		return
	}
	var req recall.WriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Path = path
	req.Overwrite = true
	mem, err := s.store.Write(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PATCH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recall": mem})
}

func (s *Server) moveRecall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromPath  string `json:"from_path"`
		ToPath    string `json:"to_path"`
		Confirmed bool   `json:"confirmed"`
		Overwrite bool   `json:"overwrite"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	mem, err := s.store.Move(req.FromPath, req.ToPath, req.Confirmed, req.Overwrite)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, recall.ErrFileExists) {
			status = http.StatusConflict
		}
		writeError(w, status, "MOVE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recall": mem})
}

func (s *Server) deleteRecall(w http.ResponseWriter, r *http.Request) {
	path, err := memoryPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PATH", err.Error())
		return
	}
	confirmed := r.URL.Query().Get("confirmed") == "true" || r.URL.Query().Get("confirmed") == "1"
	if err := s.store.Delete(path, confirmed); err != nil {
		writeError(w, http.StatusBadRequest, "DELETE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
}

func (s *Server) searchMemories(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query         string `json:"query"`
		Prefix        string `json:"prefix"`
		ExcludePrefix string `json:"exclude_prefix"`
		MaxResults    int    `json:"max_results"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	results, err := s.executeRecallSearch(r.Context(), recall.SearchOptions{
		Query: req.Query, Prefix: req.Prefix, ExcludePrefix: req.ExcludePrefix, MaxResults: req.MaxResults,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "SEARCH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "query": req.Query, "results": results, "count": len(results)})
}

func (s *Server) contextIndexMemories(w http.ResponseWriter, r *http.Request) {
	var req recall.ContextIndexRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	index, err := s.store.BuildContextIndex(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "CONTEXT_INDEX_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "context_index": index})
}

func (s *Server) listCards(w http.ResponseWriter, r *http.Request) {
	maxEntries := queryInt(r, "max_entries", 200)
	entries, err := s.store.List("recall/managed/cards", maxEntries)
	if err != nil {
		writeError(w, http.StatusBadRequest, "LIST_CARDS_FAILED", err.Error())
		return
	}
	cards, err := s.store.ListCards(maxEntries)
	if err != nil {
		writeError(w, http.StatusBadRequest, "LIST_CARDS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entries": entries, "cards": cards, "count": len(cards), "prefix": "recall/managed/cards"})
}

func (s *Server) captureCard(w http.ResponseWriter, r *http.Request) {
	var req recall.CardRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.store.CaptureCard(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "CAPTURE_CARD_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) writeCard(w http.ResponseWriter, r *http.Request) {
	var req recall.CardRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.store.WriteCard(req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, recall.ErrFileExists) {
			status = http.StatusConflict
		}
		writeError(w, status, "WRITE_CARD_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) searchCards(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	results, err := s.executeRecallSearch(r.Context(), recall.SearchOptions{
		Query: req.Query, Prefix: "recall/managed/cards", MaxResults: req.MaxResults,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "SEARCH_CARDS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "query": req.Query, "results": results, "count": len(results), "prefix": "recall/managed/cards"})
}

func (s *Server) embeddingStatus(w http.ResponseWriter, r *http.Request) {
	embedding := s.runtimeAI.currentEmbedding()
	if embedding == nil {
		writeJSON(w, http.StatusOK, recall.EmbeddingStatus{
			OK: true, Model: recall.DefaultEmbeddingModel,
			Reason: "embedding service is not configured",
		})
		return
	}
	writeJSON(w, http.StatusOK, embedding.Status(r.Context()))
}

func (s *Server) reindexEmbeddings(w http.ResponseWriter, r *http.Request) {
	embedding := s.runtimeAI.currentEmbedding()
	if embedding == nil {
		writeError(w, http.StatusServiceUnavailable, "EMBEDDING_DISABLED", "embedding service is not configured")
		return
	}
	var req recall.EmbeddingReindexRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := embedding.Reindex(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "EMBEDDING_REINDEX_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) searchEmbeddings(w http.ResponseWriter, r *http.Request) {
	embedding := s.runtimeAI.currentEmbedding()
	if embedding == nil {
		writeError(w, http.StatusServiceUnavailable, "EMBEDDING_DISABLED", "embedding service is not configured")
		return
	}
	var req recall.EmbeddingSearchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := embedding.Search(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "EMBEDDING_SEARCH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func memoryPath(r *http.Request) (string, error) {
	path := r.PathValue("path")
	if strings.TrimSpace(path) == "" {
		return "", errors.New("recall path is required")
	}
	return path, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	body := http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeJSONDecodeError(w, err)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("request body must contain exactly one JSON value")
		}
		writeJSONDecodeError(w, err)
		return false
	}
	return true
}

func writeJSONDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", fmt.Sprintf("JSON request body exceeds %d bytes", tooLarge.Limit))
		return
	}
	writeError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if status >= http.StatusBadRequest {
		if object, ok := value.(map[string]any); ok {
			if requestID := requestIDFromWriter(w); requestID != "" {
				copy := make(map[string]any, len(object)+1)
				for key, item := range object {
					copy[key] = item
				}
				if _, exists := copy["request_id"]; !exists {
					copy["request_id"] = requestID
				}
				value = copy
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": map[string]any{"code": code, "message": message}})
}

func queryInt(r *http.Request, key string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
