package httpx

import (
	"errors"
	"net/http"
	"time"

	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/settings"
	"github.com/uvwt/nexusdock/internal/stage3"
)

type runtimeAITestResult struct {
	OK        bool   `json:"ok"`
	Target    string `json:"target"`
	Model     string `json:"model,omitempty"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
}

func (s *Server) getRuntimeAISettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "运行时 AI 设置存储不可用")
		return
	}
	_, view, err := s.settings.Load(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SETTINGS_READ_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": view})
}

func (s *Server) updateRuntimeAISettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "运行时 AI 设置存储不可用")
		return
	}
	var request settings.UpdateInput
	if !decodeJSON(w, r, &request) {
		return
	}
	cfg, view, err := s.settings.Update(r.Context(), request)
	if err != nil {
		var validation settings.ValidationError
		if errors.As(err, &validation) {
			writeError(w, http.StatusBadRequest, "INVALID_RUNTIME_SETTINGS", validation.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "SETTINGS_UPDATE_FAILED", err.Error())
		return
	}
	s.applyRuntimeAIConfig(cfg)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": view})
}

func (s *Server) testStage3Connection(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentAIConfig()
	started := time.Now()
	result := runtimeAITestResult{Target: "stage3", Model: cfg.Stage3Model}
	client, err := stage3.NewClient(stage3.Config{
		Endpoint: cfg.Stage3Endpoint,
		Model:    cfg.Stage3Model,
		APIKey:   cfg.Stage3APIKey,
		Timeout:  cfg.Stage3Timeout,
	})
	if err == nil {
		err = client.Probe(r.Context())
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Message = "模型连接测试失败：" + stage3.RedactText(err.Error())
		writeJSON(w, http.StatusOK, result)
		return
	}
	result.OK = true
	result.Message = "模型连接正常，认证和模型名均可用。"
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) testEmbeddingConnection(w http.ResponseWriter, r *http.Request) {
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
	if model, ok := status["model"].(string); ok {
		result.Model = model
	}
	reachable, _ := status["reachable"].(bool)
	if !reachable {
		if message, ok := status["error"].(string); ok && message != "" {
			result.Message = "向量连接测试失败：" + stage3.RedactText(message)
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

func (s *Server) currentAIConfig() settings.RuntimeAIConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.aiCfg
}

func (s *Server) currentEmbedding() *recall.EmbeddingService {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.embedding
}

func (s *Server) applyRuntimeAIConfig(cfg settings.RuntimeAIConfig) {
	embedding := recall.NewEmbeddingService(s.store, recall.EmbeddingConfig{
		Enabled: cfg.EmbeddingEnabled, Endpoint: cfg.EmbeddingEndpoint, Model: cfg.EmbeddingModel, APIKey: cfg.EmbeddingAPIKey,
		Timeout: cfg.EmbeddingTimeout,
	})

	s.mu.Lock()
	s.aiCfg = cfg
	s.embedding = embedding
	s.mu.Unlock()

	s.notifyStage3ConfigChanged()
}

func (s *Server) notifyStage3ConfigChanged() {
	if s.stage3Wake == nil {
		return
	}
	select {
	case s.stage3Wake <- struct{}{}:
	default:
	}
}
