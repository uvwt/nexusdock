package httpx

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uvwt/nexusdock/internal/workflow"
)

// registerWorkflowTemplateRoutes 挂载 Workflow 模板 REST 端点。
// 注册表业务（模型、published 文件、生命周期、向量索引、match）都在
// internal/workflow.Registry，这里只做 JSON/HTTP 映射。
func (s *Server) registerWorkflowTemplateRoutes(r chi.Router) {
	r.Get("/v1/workflow-templates", s.workflowTemplatesList)
	r.Post("/v1/workflow-templates/publish", s.workflowTemplatePublish)
	r.Post("/v1/workflow-templates/match", s.workflowTemplatesMatch)
	r.Post("/v1/workflow-templates/reindex", s.workflowTemplatesReindex)
	r.Get("/v1/workflow-templates/vector-index", s.workflowTemplateVectorIndexRead)
	r.Get("/v1/workflow-templates/{templateID}/{version}", s.workflowTemplateRead)
	r.Post("/v1/workflow-templates/{templateID}/{version}/retire", s.workflowTemplateRetire)
}

// writeWorkflowOperationError 把注册表业务错误映射为 HTTP 响应；
// 非业务错误按注册表整体失败（409）处理，与既有行为一致。
func writeWorkflowOperationError(w http.ResponseWriter, err error) {
	var operationErr *workflow.OperationError
	if errors.As(err, &operationErr) {
		status := http.StatusConflict
		switch operationErr.Code {
		case "INVALID_WORKFLOW_TEMPLATE", "WORKFLOW_TEMPLATE_NOT_ACTIVE":
			status = http.StatusBadRequest
		case "WORKFLOW_TEMPLATE_NOT_FOUND":
			status = http.StatusNotFound
		}
		writeError(w, status, operationErr.Code, operationErr.Error())
		return
	}
	writeError(w, http.StatusConflict, "WORKFLOW_REGISTRY_FAILED", err.Error())
}

func (s *Server) workflowTemplatePublish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Template workflow.Template `json:"template"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.executeWorkflowTemplatePublish(req.Template)
	if err != nil {
		writeWorkflowOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "template": result.Template, "template_summary": result.Summary, "source": "nexus-registry",
	})
}

func (s *Server) workflowTemplateRetire(w http.ResponseWriter, r *http.Request) {
	result, err := s.executeWorkflowTemplateRetire(r.PathValue("templateID"), r.PathValue("version"))
	if err != nil {
		writeWorkflowOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "template": result.Template, "template_summary": result.Summary, "source": "nexus-registry",
	})
}

func (s *Server) workflowTemplateRead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("templateID")
	version := r.PathValue("version")
	if !workflow.ValidToken(id) || !workflow.ValidToken(version) {
		writeError(w, http.StatusBadRequest, "INVALID_WORKFLOW_TEMPLATE", "template id or version is invalid")
		return
	}
	result, err := s.executeWorkflowTemplateGet(id, version)
	if err != nil {
		writeError(w, http.StatusNotFound, "WORKFLOW_TEMPLATE_NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "template": result.Template, "template_summary": result.Summary, "source": "nexus-registry",
	})
}

func (s *Server) workflowTemplatesList(w http.ResponseWriter, r *http.Request) {
	status := workflow.Status(strings.TrimSpace(r.URL.Query().Get("status")))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	includeHistory := r.URL.Query().Get("include_history") == "true" || r.URL.Query().Get("view") == "history"

	result, err := s.executeWorkflowTemplateList(status)
	if err != nil {
		writeError(w, http.StatusConflict, "WORKFLOW_LIST_FAILED", err.Error())
		return
	}
	summaries := make([]workflowTemplateSummary, 0, len(result.Summaries))
	for _, item := range result.Summaries {
		if query != "" && !templateSummaryMatches(item, query) {
			continue
		}
		summaries = append(summaries, item)
	}
	counters := workflowTemplateCounters(summaries)
	items := summaries
	mode := "history"
	if status == "" && !includeHistory {
		items = currentWorkflowTemplates(summaries)
		mode = "current"
	}
	conflicts := 0
	for _, counter := range counters {
		if counter.Active > 1 {
			conflicts++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "items": items, "templates": workflowTemplateCompactList(result.Templates),
		"count": len(items), "total_count": len(summaries), "root": s.workflowRegistry.Root(),
		"source": "nexus-registry", "mode": mode, "conflict_count": conflicts, "version_summary": counters,
	})
}

func (s *Server) workflowTemplatesMatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Goal   string `json:"goal"`
		Device string `json:"device"`
		Type   string `json:"type"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.executeWorkflowTemplateMatch(r.Context(), req.Goal, req.Device, req.Type)
	if err != nil {
		writeError(w, http.StatusConflict, "WORKFLOW_MATCH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) workflowTemplatesReindex(w http.ResponseWriter, r *http.Request) {
	result, err := s.workflowRegistry.ReindexVectors(r.Context(), s.workflowAIConfig())
	if err != nil {
		writeError(w, http.StatusConflict, "WORKFLOW_REINDEX_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "source": "nexus-registry", "count": result.Count, "vector_search_enabled": true, "vector_index_status": workflow.VectorIndexReady, "embedding_model": result.Model, "dimension": result.Dimension, "index_path": result.IndexPath})
}

func (s *Server) workflowTemplateVectorIndexRead(w http.ResponseWriter, r *http.Request) {
	result, err := s.executeWorkflowTemplateVectorIndex()
	if err != nil {
		writeError(w, http.StatusConflict, "WORKFLOW_VECTOR_INDEX_INVALID", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// workflowAIConfig 把运行期可变的 AI 设置映射成 workflow 包需要的 embedding 子集。
// 设置可能随时通过设置页更新，因此每次请求都重新读取而不是启动时固定。
func (s *Server) workflowAIConfig() workflow.AIConfig {
	cfg := s.currentAIConfig()
	return workflow.AIConfig{
		Enabled: cfg.EmbeddingEnabled, Endpoint: cfg.EmbeddingEndpoint, Model: cfg.EmbeddingModel,
		APIKey: cfg.EmbeddingAPIKey, Timeout: cfg.EmbeddingTimeout,
	}
}
