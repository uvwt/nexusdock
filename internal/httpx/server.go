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
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/auth"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/privatenotes"
	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/settings"
	"github.com/uvwt/nexusdock/internal/versioning"
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
	mu                   sync.RWMutex
	cfg                  config.Config
	aiCfg                config.Config
	aiCfgSet             bool
	db                   *sql.DB
	store                *recall.Store
	privateNotes         *privatenotes.Store
	agentDock            *agentdock.Store
	agentDockHub         *agentdock.Hub
	versions             *versioning.Manager
	logger               *slog.Logger
	auth                 *auth.Service
	oauth                *auth.OAuthService
	oauthRegisterLimiter *fixedWindowLimiter
	embedding            *recall.EmbeddingService
	settings             *settings.Store
	mcpToken             *auth.MCPTokenStore
	stage3Wake           chan struct{}
	mcpServer            *mcpsdk.Server
	mcpHandler           http.Handler
	mcpReconcileMu       sync.Mutex
	mcpToolsMu           sync.RWMutex
	mcpTools             map[string]publishedNodeTool
	mcpResourcesMu       sync.RWMutex
	mcpResources         map[string]struct{}
	artifactSecretMu     sync.Mutex
}

type ServerOption func(*Server)

func WithSystemDatabase(db *sql.DB) ServerOption {
	return func(server *Server) { server.db = db }
}

func WithAgentDockNodes(store *agentdock.Store) ServerOption {
	return func(server *Server) {
		server.agentDock = store
		server.agentDockHub = agentdock.NewHub(store)
	}
}

func WithWebAuthentication(authService *auth.Service) ServerOption {
	return func(server *Server) { server.auth = authService }
}

func WithEmbeddingService(service *recall.EmbeddingService) ServerOption {
	return func(server *Server) { server.embedding = service }
}

func WithRuntimeSettings(store *settings.Store) ServerOption {
	return func(server *Server) { server.settings = store }
}

func WithPrivateNotes(store *privatenotes.Store) ServerOption {
	return func(server *Server) { server.privateNotes = store }
}

func WithMCPTokenStore(store *auth.MCPTokenStore) ServerOption {
	return func(server *Server) { server.mcpToken = store }
}

func NewServer(cfg config.Config, store *recall.Store, versions *versioning.Manager, logger *slog.Logger, options ...ServerOption) *Server {
	server := &Server{
		cfg: cfg, aiCfg: cfg, aiCfgSet: true, store: store, versions: versions, logger: logger,
		stage3Wake: make(chan struct{}, 1), mcpTools: make(map[string]publishedNodeTool), mcpResources: make(map[string]struct{}),
	}
	for _, option := range options {
		option(server)
	}
	if server.db != nil && server.auth != nil {
		server.oauth = auth.NewOAuthService(server.db)
		server.oauthRegisterLimiter = newFixedWindowLimiter(30, time.Minute)
	}
	server.initializeMCPGateway()
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	protected := func(next http.HandlerFunc) http.HandlerFunc { return s.withAPIAccess(next) }
	deviceProtected := func(next http.HandlerFunc) http.HandlerFunc { return s.withDeviceOrAPIAccess(next) }
	uiProtected := func(next http.HandlerFunc) http.HandlerFunc { return s.withUIAccess(next) }
	mux.HandleFunc("GET /", uiProtected(s.uiIndex))
	mux.HandleFunc("GET /ui/", uiProtected(s.uiIndex))
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /artifacts/public/{nodeID}/{artifactID}/{filename}", s.servePublicArtifact)
	mux.HandleFunc("HEAD /artifacts/public/{nodeID}/{artifactID}/{filename}", s.servePublicArtifact)
	if s.mcpHandler != nil {
		gateway := s.withMCPAccess(s.mcpHandler.ServeHTTP)
		mux.HandleFunc("GET /mcp", gateway)
		mux.HandleFunc("POST /mcp", gateway)
		mux.HandleFunc("DELETE /mcp", gateway)
	}
	s.registerOAuthRoutes(mux)
	mux.HandleFunc("GET /v1/system/status", protected(s.systemStatus))
	mux.HandleFunc("GET /v1/settings/ai", protected(s.getRuntimeAISettings))
	mux.HandleFunc("GET /v1/settings/mcp-token", protected(s.getMCPAccessToken))
	mux.HandleFunc("POST /v1/settings/mcp-token/reset", protected(s.resetMCPAccessToken))
	mux.HandleFunc("PUT /v1/settings/ai", protected(s.updateRuntimeAISettings))
	mux.HandleFunc("POST /v1/settings/ai/test/stage3", protected(s.testStage3Connection))
	mux.HandleFunc("POST /v1/settings/ai/test/embedding", protected(s.testEmbeddingConnection))
	s.registerRuntimeRoutes(mux, protected)
	s.registerEvolutionLifecycleRoutes(mux, protected)
	s.registerWorkflowTemplateRoutes(mux, deviceProtected)
	if s.privateNotes != nil {
		s.registerPrivateNoteRoutes(mux, deviceProtected)
	}
	s.registerWebAuthRoutes(mux)
	mux.HandleFunc("GET /v1/git/diff", protected(s.gitDiff))
	mux.HandleFunc("GET /v1/git/log", protected(s.gitLog))
	mux.HandleFunc("GET /v1/git/commit", protected(s.gitCommit))
	mux.HandleFunc("POST /v1/git/commit", protected(s.gitRecordVersion))
	mux.HandleFunc("GET /v1/recall", deviceProtected(s.listMemories))
	mux.HandleFunc("POST /v1/recall", deviceProtected(s.writeRecall))
	mux.HandleFunc("POST /v1/recall/preview", deviceProtected(s.previewRecall))
	mux.HandleFunc("POST /v1/recall/move", deviceProtected(s.moveRecall))
	mux.HandleFunc("POST /v1/recall/search", deviceProtected(s.searchMemories))
	mux.HandleFunc("POST /v1/recall/context-index", deviceProtected(s.contextIndexMemories))
	mux.HandleFunc("GET /v1/recall/cards", deviceProtected(s.listCards))
	mux.HandleFunc("POST /v1/recall/cards", deviceProtected(s.writeCard))
	mux.HandleFunc("POST /v1/recall/cards/capture", deviceProtected(s.captureCard))
	mux.HandleFunc("POST /v1/recall/cards/search", deviceProtected(s.searchCards))
	mux.HandleFunc("GET /v1/embeddings/status", deviceProtected(s.embeddingStatus))
	mux.HandleFunc("POST /v1/embeddings/reindex", deviceProtected(s.reindexEmbeddings))
	mux.HandleFunc("POST /v1/embeddings/search", deviceProtected(s.searchEmbeddings))
	mux.HandleFunc("GET /v1/recall/{path...}", deviceProtected(s.readRecall))
	mux.HandleFunc("PATCH /v1/recall/{path...}", deviceProtected(s.patchRecall))
	mux.HandleFunc("DELETE /v1/recall/{path...}", deviceProtected(s.deleteRecall))
	mux.HandleFunc("GET /v1/", http.NotFound)
	mux.HandleFunc("GET /api/", http.NotFound)
	return s.requestBoundary(s.securityHeaders(mux))
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
		if r.TLS != nil || s.isTrustedProxy(r) && strings.EqualFold(lastForwardedValue(r.Header.Get("X-Forwarded-Proto")), "https") {
			headers.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "nexusdock"})
}

func (s *Server) gitDiff(w http.ResponseWriter, r *http.Request) {
	diff, err := s.versions.Diff(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "GIT_DIFF_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *Server) gitLog(w http.ResponseWriter, r *http.Request) {
	log, err := s.versions.Log(r.Context(), queryInt(r, "limit", 50))
	if err != nil {
		writeError(w, http.StatusConflict, "GIT_LOG_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, log)
}

func (s *Server) gitCommit(w http.ResponseWriter, r *http.Request) {
	detail, err := s.versions.CommitDetail(r.Context(), r.URL.Query().Get("hash"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "GIT_COMMIT_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) gitRecordVersion(w http.ResponseWriter, r *http.Request) {
	result, err := s.versions.Record(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "GIT_VERSION_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
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
	s.versions.MarkChanged(r.Context())
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
	s.versions.MarkChanged(r.Context())
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
	s.versions.MarkChanged(r.Context())
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
	s.versions.MarkChanged(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
}

func (s *Server) searchRecall(ctx context.Context, options recall.SearchOptions) ([]recall.SearchResult, error) {
	embedding := s.currentEmbedding()
	if embedding == nil {
		return s.store.SearchWithOptions(options)
	}
	return embedding.HybridSearch(ctx, options)
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
	results, err := s.searchRecall(r.Context(), recall.SearchOptions{
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
	s.versions.MarkChanged(r.Context())
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
	results, err := s.searchRecall(r.Context(), recall.SearchOptions{
		Query: req.Query, Prefix: "recall/managed/cards", MaxResults: req.MaxResults,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "SEARCH_CARDS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "query": req.Query, "results": results, "count": len(results), "prefix": "recall/managed/cards"})
}

func (s *Server) embeddingStatus(w http.ResponseWriter, r *http.Request) {
	embedding := s.currentEmbedding()
	if embedding == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": false, "configured": false, "reason": "embedding service is not configured"})
		return
	}
	writeJSON(w, http.StatusOK, embedding.Status(r.Context()))
}

func (s *Server) reindexEmbeddings(w http.ResponseWriter, r *http.Request) {
	embedding := s.currentEmbedding()
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
	embedding := s.currentEmbedding()
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
