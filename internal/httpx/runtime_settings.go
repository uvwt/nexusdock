package httpx

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/settings"
)

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|auth[_-]?token|token|password|passwd|secret|cookie|private[_-]?key)\s*[:=]\s*([^\s,;]+)`)
	bearerPattern           = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	jwtPattern              = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	commonTokenPattern      = regexp.MustCompile(`\b(?:sk|ghp|github_pat|xox[baprs])[-_][A-Za-z0-9_-]{12,}\b`)
	privateKeyPattern       = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
)

func redactRuntimeError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = privateKeyPattern.ReplaceAllString(value, "[REDACTED_PRIVATE_KEY]")
	value = secretAssignmentPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = jwtPattern.ReplaceAllString(value, "[REDACTED_JWT]")
	value = commonTokenPattern.ReplaceAllString(value, "[REDACTED_TOKEN]")
	const maxBytes = 2048
	if len(value) <= maxBytes {
		return value
	}
	limit := maxBytes
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

type runtimeAITestResult struct {
	OK        bool   `json:"ok"`
	Target    string `json:"target"`
	Model     string `json:"model,omitempty"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
}

// runtimeAIState 只管理运行期可热更新的 AI 配置与由它派生的 Embedding 实例。
// 这组状态拥有共同的更新时机，使用独立锁，避免与 MCP Gateway 的展示开关共享 Server 全局锁。
type runtimeAIState struct {
	mu            sync.RWMutex
	config        settings.RuntimeAIConfig
	embedding     *recall.EmbeddingService
	settingsStore *settings.Store
	recallStore   *recall.Store
}

func newRuntimeAIState(store *recall.Store) runtimeAIState {
	return runtimeAIState{
		config:      settings.DefaultRuntimeAIConfig(),
		recallStore: store,
	}
}

func (s *runtimeAIState) getSettings(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		writeError(w, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "运行时 AI 设置存储不可用")
		return
	}
	_, view, err := s.settingsStore.Load(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SETTINGS_READ_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": view})
}

func (s *runtimeAIState) updateSettings(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		writeError(w, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "运行时 AI 设置存储不可用")
		return
	}
	var request settings.UpdateInput
	if !decodeJSON(w, r, &request) {
		return
	}
	cfg, view, err := s.settingsStore.Update(r.Context(), request)
	if err != nil {
		var validation settings.ValidationError
		if errors.As(err, &validation) {
			writeError(w, http.StatusBadRequest, "INVALID_RUNTIME_SETTINGS", validation.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "SETTINGS_UPDATE_FAILED", err.Error())
		return
	}
	s.applyConfig(cfg)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": view})
}

func (s *runtimeAIState) testEmbeddingConnection(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	result := runtimeAITestResult{Target: "embedding"}
	embedding := s.currentEmbedding()
	if embedding == nil {
		result.Message = "向量服务尚未配置。"
		writeJSON(w, http.StatusOK, result)
		return
	}
	status := embedding.Status(r.Context())
	result.LatencyMS = time.Since(started).Milliseconds()
	result.Model = status.Model
	if status.Reachable == nil || !*status.Reachable {
		if status.Error != "" {
			result.Message = "向量连接测试失败：" + redactRuntimeError(status.Error)
		} else {
			result.Message = "向量服务未启用或当前不可达。"
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	result.OK = true
	result.Message = "向量服务连接正常，Embedding 请求可用。"
	writeJSON(w, http.StatusOK, result)
}

func (s *runtimeAIState) currentConfig() settings.RuntimeAIConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *runtimeAIState) currentEmbedding() *recall.EmbeddingService {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.embedding
}

func (s *runtimeAIState) applyConfig(cfg settings.RuntimeAIConfig) {
	embedding := recall.NewEmbeddingService(s.recallStore, recall.EmbeddingConfig{
		Enabled: cfg.EmbeddingEnabled, Endpoint: cfg.EmbeddingEndpoint, Model: cfg.EmbeddingModel, APIKey: cfg.EmbeddingAPIKey,
		Timeout: cfg.EmbeddingTimeout,
	})

	s.mu.Lock()
	s.config = cfg
	s.embedding = embedding
	s.mu.Unlock()
}
