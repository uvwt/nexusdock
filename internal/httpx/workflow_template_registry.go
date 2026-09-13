package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/settings"
)

type workflowTemplateStatus string

const (
	workflowTemplateActive            workflowTemplateStatus = "active"
	workflowTemplateRetired           workflowTemplateStatus = "retired"
	maxWorkflowEmbeddingResponseBytes                        = 32 << 20
)

var errWorkflowVectorIndexStale = errors.New("workflow vector index is stale")

type workflowMatchRule struct {
	Keywords []string `json:"keywords,omitempty"`
	Devices  []string `json:"devices,omitempty"`
	Type     string   `json:"type,omitempty"`
}

type workflowTemplateStep struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Phase string `json:"phase"`
}

type workflowTemplate struct {
	ID                   string                 `json:"id"`
	Version              string                 `json:"version"`
	Title                string                 `json:"title"`
	Description          string                 `json:"description,omitempty"`
	SourceEvolutionID    string                 `json:"source_evolution_id,omitempty"`
	Status               workflowTemplateStatus `json:"status"`
	Match                workflowMatchRule      `json:"match,omitempty"`
	CompletionConditions []string               `json:"completion_conditions"`
	Steps                []workflowTemplateStep `json:"steps"`
	AllowLongTemplate    bool                   `json:"allow_long_template,omitempty"`
	LongTemplateReason   string                 `json:"long_template_reason,omitempty"`
	Hash                 string                 `json:"hash,omitempty"`
	PublishedAt          *time.Time             `json:"published_at,omitempty"`
	RetiredAt            *time.Time             `json:"retired_at,omitempty"`
}

type workflowTemplateCandidate struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Score   int    `json:"score"`
	Reason  string `json:"reason"`
}

var workflowRegistryMu sync.Mutex

type workflowOperationError struct {
	status int
	code   string
	err    error
}

func (e *workflowOperationError) Error() string { return e.err.Error() }
func (e *workflowOperationError) Unwrap() error { return e.err }

func newWorkflowOperationError(status int, code string, err error) error {
	return &workflowOperationError{status: status, code: code, err: err}
}

func writeWorkflowOperationError(w http.ResponseWriter, err error) {
	var operationErr *workflowOperationError
	if errors.As(err, &operationErr) {
		writeError(w, operationErr.status, operationErr.code, operationErr.Error())
		return
	}
	writeError(w, http.StatusConflict, "WORKFLOW_REGISTRY_FAILED", err.Error())
}

func (s *Server) registerWorkflowTemplateRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/workflow-templates", protected(s.workflowTemplatesList))
	mux.HandleFunc("POST /v1/workflow-templates/publish", protected(s.workflowTemplatePublish))
	mux.HandleFunc("POST /v1/workflow-templates/match", protected(s.workflowTemplatesMatch))
	mux.HandleFunc("POST /v1/workflow-templates/reindex", protected(s.workflowTemplatesReindex))
	mux.HandleFunc("GET /v1/workflow-templates/vector-index", protected(s.workflowTemplateVectorIndexRead))
	mux.HandleFunc("GET /v1/workflow-templates/{templateID}/{version}", protected(s.workflowTemplateRead))
	mux.HandleFunc("POST /v1/workflow-templates/{templateID}/{version}/retire", protected(s.workflowTemplateRetire))
}

func (s *Server) workflowRegistryRoot() string {
	root := strings.TrimSpace(s.cfg.NexusDataDir)
	if root == "" {
		root = filepath.Join(".", "nexus-data")
	}
	return filepath.Join(root, "workflow-templates")
}

func (s *Server) ensureWorkflowRegistryDirs() error {
	dir := filepath.Join(s.workflowRegistryRoot(), "published")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func (s *Server) workflowTemplatePath(area, id, version string) string {
	return filepath.Join(s.workflowRegistryRoot(), area, id+"@"+version+".json")
}

func (s *Server) workflowTemplatePublish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Template workflowTemplate `json:"template"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	t, err := s.publishWorkflowTemplateValue(req.Template)
	if err != nil {
		writeWorkflowOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "template": t, "template_summary": s.workflowTemplateSummary(t), "source": "nexus-registry"})
}

func (s *Server) workflowTemplateRetire(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("templateID")
	version := r.PathValue("version")
	t, err := s.retireWorkflowTemplateValue(id, version)
	if err != nil {
		writeWorkflowOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "template": t, "template_summary": s.workflowTemplateSummary(t), "source": "nexus-registry"})
}

func (s *Server) publishWorkflowTemplateValue(input workflowTemplate) (workflowTemplate, error) {
	workflowRegistryMu.Lock()
	defer workflowRegistryMu.Unlock()

	t := input
	// 发布是唯一写入口。忽略调用方携带的生命周期元数据，避免伪造状态或复用旧 hash。
	t.Status = workflowTemplateActive
	t.Hash = ""
	t.PublishedAt = nil
	t.RetiredAt = nil
	if err := s.ensureWorkflowRegistryDirs(); err != nil {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusConflict, "WORKFLOW_REGISTRY_FAILED", err)
	}
	if err := validateWorkflowTemplate(t); err != nil {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusBadRequest, "INVALID_WORKFLOW_TEMPLATE", err)
	}
	if _, err := os.Stat(s.workflowTemplatePath("published", t.ID, t.Version)); err == nil {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusConflict, "WORKFLOW_VERSION_IMMUTABLE", errors.New("published template version already exists and cannot be overwritten"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusConflict, "WORKFLOW_REGISTRY_FAILED", err)
	}

	now := time.Now().UTC()
	t.PublishedAt = &now
	t.Hash = workflowTemplateHash(t)
	errorCode, err := s.publishWorkflowTemplate(t, now, writeWorkflowTemplateJSON)
	if err != nil {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusConflict, errorCode, err)
	}
	return t, nil
}

func (s *Server) retireWorkflowTemplateValue(id, version string) (workflowTemplate, error) {
	if !validWorkflowTemplateToken(id) || !validWorkflowTemplateToken(version) {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusBadRequest, "INVALID_WORKFLOW_TEMPLATE", errors.New("template id or version is invalid"))
	}

	workflowRegistryMu.Lock()
	defer workflowRegistryMu.Unlock()
	if err := s.ensureWorkflowRegistryDirs(); err != nil {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusConflict, "WORKFLOW_REGISTRY_FAILED", err)
	}
	t, err := s.loadWorkflowTemplate("published", id, version)
	if err != nil {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusNotFound, "WORKFLOW_TEMPLATE_NOT_FOUND", err)
	}
	if t.Status != workflowTemplateActive {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusBadRequest, "WORKFLOW_TEMPLATE_NOT_ACTIVE", errors.New("only active templates can be retired"))
	}

	now := time.Now().UTC()
	t.Status = workflowTemplateRetired
	t.RetiredAt = &now
	t.Hash = workflowTemplateHash(t)
	if err := writeWorkflowTemplateJSON(s.workflowTemplatePath("published", id, version), t); err != nil {
		return workflowTemplate{}, newWorkflowOperationError(http.StatusConflict, "WORKFLOW_RETIRE_FAILED", err)
	}
	return t, nil
}

func (s *Server) workflowTemplateRead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("templateID")
	version := r.PathValue("version")
	if !validWorkflowTemplateToken(id) || !validWorkflowTemplateToken(version) {
		writeError(w, http.StatusBadRequest, "INVALID_WORKFLOW_TEMPLATE", "template id or version is invalid")
		return
	}
	workflowRegistryMu.Lock()
	defer workflowRegistryMu.Unlock()
	t, err := s.getWorkflowTemplate(id, version)
	if err != nil {
		writeError(w, http.StatusNotFound, "WORKFLOW_TEMPLATE_NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "template": t, "template_summary": s.workflowTemplateSummary(t), "source": "nexus-registry"})
}

func (s *Server) workflowTemplatesList(w http.ResponseWriter, r *http.Request) {
	status := workflowTemplateStatus(strings.TrimSpace(r.URL.Query().Get("status")))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	includeHistory := r.URL.Query().Get("include_history") == "true" || r.URL.Query().Get("view") == "history"
	templates, err := s.listWorkflowTemplates(status)
	if err != nil {
		writeError(w, http.StatusConflict, "WORKFLOW_LIST_FAILED", err.Error())
		return
	}
	summaries := make([]workflowTemplateSummary, 0, len(templates))
	for _, t := range templates {
		item := s.workflowTemplateSummary(t)
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items, "templates": workflowTemplateCompactList(templates), "count": len(items), "total_count": len(summaries), "root": s.workflowRegistryRoot(), "source": "nexus-registry", "mode": mode, "conflict_count": conflicts, "version_summary": counters})
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
	result, err := s.workflowTemplateMatchResult(r.Context(), req.Goal, req.Device, req.Type)
	if err != nil {
		writeError(w, http.StatusConflict, "WORKFLOW_MATCH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) workflowTemplatesReindex(w http.ResponseWriter, r *http.Request) {
	result, err := s.reindexWorkflowTemplateVectors(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "WORKFLOW_REINDEX_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) workflowTemplateVectorIndexRead(w http.ResponseWriter, r *http.Request) {
	result, err := s.workflowTemplateVectorIndexResult()
	if err != nil {
		writeError(w, http.StatusConflict, "WORKFLOW_VECTOR_INDEX_INVALID", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getWorkflowTemplate(id, version string) (workflowTemplate, error) {
	t, err := s.loadWorkflowTemplate("published", id, version)
	if err == nil {
		return t, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return workflowTemplate{}, fmt.Errorf("template %s@%s not found", id, version)
	}
	return workflowTemplate{}, fmt.Errorf("load published template %s@%s: %w", id, version, err)
}

func (s *Server) loadWorkflowTemplate(area, id, version string) (workflowTemplate, error) {
	if !validWorkflowTemplateToken(id) || !validWorkflowTemplateToken(version) {
		return workflowTemplate{}, errors.New("invalid template id or version")
	}
	data, err := os.ReadFile(s.workflowTemplatePath(area, id, version))
	if err != nil {
		return workflowTemplate{}, err
	}
	return decodeWorkflowTemplate(data)
}

func decodeWorkflowTemplate(data []byte) (workflowTemplate, error) {
	var template workflowTemplate
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&template); err != nil {
		return workflowTemplate{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return workflowTemplate{}, errors.New("template file contains multiple JSON values")
		}
		return workflowTemplate{}, fmt.Errorf("read trailing template data: %w", err)
	}
	return template, nil
}

func (s *Server) listWorkflowTemplates(status workflowTemplateStatus) ([]workflowTemplate, error) {
	workflowRegistryMu.Lock()
	defer workflowRegistryMu.Unlock()
	if err := s.ensureWorkflowRegistryDirs(); err != nil {
		return nil, err
	}

	dir := filepath.Join(s.workflowRegistryRoot(), "published")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]workflowTemplate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		t, err := decodeWorkflowTemplate(data)
		if err != nil {
			return nil, fmt.Errorf("read workflow template %s: %w", entry.Name(), err)
		}
		if status == "" || t.Status == status {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return compareWorkflowVersions(out[i].Version, out[j].Version) > 0
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

type workflowTemplateJSONWriter func(string, any) error

func (s *Server) publishWorkflowTemplate(t workflowTemplate, publishedAt time.Time, write workflowTemplateJSONWriter) (string, error) {
	publishedPath := s.workflowTemplatePath("published", t.ID, t.Version)
	if err := write(publishedPath, t); err != nil {
		rollbackErr := removeWorkflowTemplateFile(publishedPath)
		if rollbackErr != nil {
			return "WORKFLOW_PUBLISH_FAILED", fmt.Errorf("write new published template: %w; rollback partial file: %v", err, rollbackErr)
		}
		return "WORKFLOW_PUBLISH_FAILED", fmt.Errorf("write new published template: %w", err)
	}
	if err := s.retireActiveWorkflowTemplates(t.ID, t.Version, publishedAt, write); err != nil {
		rollbackErr := removeWorkflowTemplateFile(publishedPath)
		if rollbackErr != nil {
			return "WORKFLOW_RETIRE_OLD_FAILED", fmt.Errorf("retire old templates: %w; rollback new template: %v", err, rollbackErr)
		}
		return "WORKFLOW_RETIRE_OLD_FAILED", fmt.Errorf("retire old templates: %w", err)
	}
	return "", nil
}

func (s *Server) retireActiveWorkflowTemplates(id, exceptVersion string, retiredAt time.Time, write workflowTemplateJSONWriter) error {
	entries, err := os.ReadDir(filepath.Join(s.workflowRegistryRoot(), "published"))
	if err != nil {
		return err
	}
	originals := make([]workflowTemplate, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.workflowRegistryRoot(), "published", entry.Name()))
		if err != nil {
			return err
		}
		t, err := decodeWorkflowTemplate(data)
		if err != nil {
			return fmt.Errorf("read workflow template %s: %w", entry.Name(), err)
		}
		if t.ID != id || t.Version == exceptVersion || t.Status != workflowTemplateActive {
			continue
		}
		originals = append(originals, t)
	}

	updated := make([]workflowTemplate, 0, len(originals))
	for _, original := range originals {
		retired := original
		retired.Status = workflowTemplateRetired
		retired.RetiredAt = &retiredAt
		retired.Hash = workflowTemplateHash(retired)
		if err := write(s.workflowTemplatePath("published", retired.ID, retired.Version), retired); err != nil {
			rollbackErrors := make([]string, 0)
			rollbackTargets := append(append([]workflowTemplate{}, updated...), original)
			for _, previous := range rollbackTargets {
				if rollbackErr := write(s.workflowTemplatePath("published", previous.ID, previous.Version), previous); rollbackErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s@%s: %v", previous.ID, previous.Version, rollbackErr))
				}
			}
			if len(rollbackErrors) > 0 {
				return fmt.Errorf("retire %s@%s: %w; rollback failures: %s", retired.ID, retired.Version, err, strings.Join(rollbackErrors, "; "))
			}
			return fmt.Errorf("retire %s@%s: %w", retired.ID, retired.Version, err)
		}
		updated = append(updated, original)
	}
	return nil
}

func (s *Server) matchWorkflowTemplates(ctx context.Context, goal, device, taskType string) ([]workflowTemplateCandidate, error) {
	templates, err := s.listWorkflowTemplates(workflowTemplateActive)
	if err != nil {
		return nil, err
	}
	templates = latestWorkflowTemplateVersions(templates)
	vectorScores := s.workflowTemplateVectorScores(ctx, goal, device, taskType)
	query := workflowTemplateMatchText(strings.Join([]string{goal, taskType, device}, " "))
	out := []workflowTemplateCandidate{}
	fallback := []workflowTemplateCandidate{}
	for _, t := range templates {
		score := 0
		semantic := false
		reasons := []string{}
		for _, keyword := range t.Match.Keywords {
			keyword = strings.TrimSpace(keyword)
			if keyword != "" && strings.Contains(query, workflowTemplateMatchText(keyword)) {
				if weakWorkflowTemplateKeyword(keyword) {
					score += 15
					reasons = append(reasons, "context_keyword:"+keyword)
					continue
				}
				score += 15
				semantic = true
				reasons = append(reasons, "keyword:"+keyword)
			}
		}
		if containsWorkflowTemplateHint([]string{t.Match.Type}, taskType) {
			score += 80
			semantic = true
			reasons = append(reasons, "type:"+taskType)
		}
		if vectorScore := vectorScores[t.ID+"@"+t.Version]; vectorScore >= 0.55 {
			score += workflowTemplateVectorBonus(vectorScore)
			semantic = true
			reasons = append(reasons, fmt.Sprintf("vector:%.2f", vectorScore))
		}
		if containsWorkflowTemplateHint(t.Match.Devices, device) {
			score += 5
			reasons = append(reasons, "device:"+device)
		}
		if score > 0 && len(reasons) > 0 {
			c := workflowTemplateCandidate{ID: t.ID, Version: t.Version, Score: score, Reason: strings.Join(reasons, ", ")}
			if semantic {
				out = append(out, c)
			} else {
				fallback = append(fallback, c)
			}
		}
	}
	if len(out) == 0 {
		out = fallback
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			if out[i].ID == out[j].ID {
				return compareWorkflowVersions(out[i].Version, out[j].Version) > 0
			}
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	return out, nil
}

type workflowTemplateVectorIndex struct {
	Model     string                            `json:"model"`
	Dimension int                               `json:"dimension,omitempty"`
	UpdatedAt time.Time                         `json:"updated_at"`
	Documents map[string]workflowTemplateVector `json:"documents"`
}

type workflowTemplateVector struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Hash      string    `json:"hash"`
	Text      string    `json:"text"`
	Vector    []float64 `json:"vector"`
	UpdatedAt time.Time `json:"updated_at"`
}

func workflowTemplateVectorEnabled(cfg settings.RuntimeAIConfig) bool {
	return cfg.EmbeddingEnabled && strings.TrimSpace(cfg.EmbeddingEndpoint) != ""
}

func (s *Server) workflowTemplateVectorIndexPath() string {
	return filepath.Join(s.workflowRegistryRoot(), "vector-index.json")
}

func (s *Server) workflowTemplateVectorIndexInfo() (string, int) {
	return s.workflowTemplateVectorIndexInfoForConfig(s.currentAIConfig())
}

func (s *Server) workflowTemplateVectorIndexInfoForConfig(cfg settings.RuntimeAIConfig) (string, int) {
	if !workflowTemplateVectorEnabled(cfg) {
		return "not_configured", 0
	}
	idx, err := s.loadWorkflowTemplateVectorIndex(cfg.EmbeddingModel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing", 0
		}
		if errors.Is(err, errWorkflowVectorIndexStale) {
			return "stale", 0
		}
		return "invalid", 0
	}
	return "ready", len(idx.Documents)
}

func (s *Server) reindexWorkflowTemplateVectors(ctx context.Context) (map[string]any, error) {
	cfg := s.currentAIConfig()
	if !workflowTemplateVectorEnabled(cfg) {
		return nil, errors.New("workflow template vector search is disabled; configure and enable vector search")
	}
	templates, err := s.listWorkflowTemplates(workflowTemplateActive)
	if err != nil {
		return nil, err
	}
	templates = latestWorkflowTemplateVersions(templates)
	texts := make([]string, 0, len(templates))
	for _, t := range templates {
		texts = append(texts, workflowTemplateVectorText(t))
	}
	vectors, err := s.embedWorkflowTemplateTexts(ctx, cfg, texts)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(templates) {
		return nil, fmt.Errorf("embedding response count mismatch: got %d want %d", len(vectors), len(templates))
	}
	idx := workflowTemplateVectorIndex{Model: cfg.EmbeddingModel, UpdatedAt: time.Now().UTC(), Documents: map[string]workflowTemplateVector{}}
	if len(vectors) > 0 {
		idx.Dimension = len(vectors[0])
	}
	for i, t := range templates {
		if len(vectors[i]) != idx.Dimension {
			return nil, fmt.Errorf("embedding dimension mismatch at result %d: got %d want %d", i, len(vectors[i]), idx.Dimension)
		}
		key := t.ID + "@" + t.Version
		idx.Documents[key] = workflowTemplateVector{ID: t.ID, Version: t.Version, Hash: t.Hash, Text: texts[i], Vector: vectors[i], UpdatedAt: time.Now().UTC()}
	}
	if err := validateWorkflowTemplateVectorIndex(idx, cfg.EmbeddingModel); err != nil {
		return nil, err
	}
	if err := writeWorkflowTemplateJSON(s.workflowTemplateVectorIndexPath(), idx); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "source": "nexus-registry", "count": len(idx.Documents), "vector_search_enabled": true, "vector_index_status": "ready", "embedding_model": cfg.EmbeddingModel, "dimension": idx.Dimension, "index_path": s.workflowTemplateVectorIndexPath()}, nil
}

func (s *Server) workflowTemplateVectorScores(ctx context.Context, goal, device, taskType string) map[string]float64 {
	cfg := s.currentAIConfig()
	if !workflowTemplateVectorEnabled(cfg) || strings.TrimSpace(goal) == "" {
		return nil
	}
	idx, err := s.loadWorkflowTemplateVectorIndex(cfg.EmbeddingModel)
	if err != nil || len(idx.Documents) == 0 {
		return nil
	}
	vectors, err := s.embedWorkflowTemplateTexts(ctx, cfg, []string{strings.Join([]string{goal, taskType, device}, "\n")})
	if err != nil || len(vectors) != 1 {
		return nil
	}
	if len(vectors[0]) != idx.Dimension {
		return nil
	}
	out := map[string]float64{}
	for key, doc := range idx.Documents {
		out[key] = cosineWorkflowVector(vectors[0], doc.Vector)
	}
	return out
}

func (s *Server) loadWorkflowTemplateVectorIndex(model string) (workflowTemplateVectorIndex, error) {
	data, err := os.ReadFile(s.workflowTemplateVectorIndexPath())
	if err != nil {
		return workflowTemplateVectorIndex{}, err
	}
	return decodeWorkflowTemplateVectorIndex(data, model)
}

func decodeWorkflowTemplateVectorIndex(data []byte, model string) (workflowTemplateVectorIndex, error) {
	var idx workflowTemplateVectorIndex
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&idx); err != nil {
		return workflowTemplateVectorIndex{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return workflowTemplateVectorIndex{}, errors.New("workflow vector index contains multiple JSON values")
		}
		return workflowTemplateVectorIndex{}, fmt.Errorf("read trailing workflow vector index data: %w", err)
	}
	if err := validateWorkflowTemplateVectorIndex(idx, model); err != nil {
		return workflowTemplateVectorIndex{}, err
	}
	return idx, nil
}

func validateWorkflowTemplateVectorIndex(idx workflowTemplateVectorIndex, model string) error {
	if strings.TrimSpace(idx.Model) == "" {
		return errors.New("workflow vector index model is empty")
	}
	if strings.TrimSpace(model) != "" && idx.Model != model {
		return fmt.Errorf("%w: model %q does not match %q", errWorkflowVectorIndexStale, idx.Model, model)
	}
	if idx.Documents == nil {
		return errors.New("workflow vector index documents are missing")
	}
	if len(idx.Documents) == 0 {
		if idx.Dimension != 0 {
			return errors.New("empty workflow vector index must have dimension 0")
		}
		return nil
	}
	if idx.Dimension <= 0 {
		return errors.New("workflow vector index dimension must be positive")
	}
	for key, document := range idx.Documents {
		if document.ID == "" || document.Version == "" || key != document.ID+"@"+document.Version {
			return fmt.Errorf("workflow vector index document key mismatch for %q", key)
		}
		if len(document.Vector) != idx.Dimension {
			return fmt.Errorf("workflow vector index document %q has dimension %d, want %d", key, len(document.Vector), idx.Dimension)
		}
	}
	return nil
}

func (s *Server) embedWorkflowTemplateTexts(ctx context.Context, cfg settings.RuntimeAIConfig, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.EmbeddingEndpoint), "/")
	if endpoint == "" {
		return nil, errors.New("embedding endpoint is empty")
	}
	if !strings.HasSuffix(endpoint, "/v1/embeddings") {
		endpoint += "/v1/embeddings"
	}
	model := strings.TrimSpace(cfg.EmbeddingModel)
	if model == "" {
		model = recall.DefaultEmbeddingModel
	}
	payload, err := json.Marshal(map[string]any{"model": model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	timeout := cfg.EmbeddingTimeout
	if timeout <= 0 {
		timeout = recall.DefaultEmbeddingTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(cfg.EmbeddingAPIKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWorkflowEmbeddingResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	if len(data) > maxWorkflowEmbeddingResponseBytes {
		return nil, fmt.Errorf("embedding response exceeds %d bytes", maxWorkflowEmbeddingResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding endpoint returned %s", resp.Status)
	}
	return parseWorkflowEmbeddingResponse(data)
}

func parseWorkflowEmbeddingResponse(data []byte) ([][]float64, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if value, exists := raw["embeddings"]; exists {
		values, ok := value.([]any)
		if !ok {
			return nil, errors.New("embedding response embeddings is not an array")
		}
		return workflowVectorsFromArray(values)
	}
	if value, exists := raw["data"]; exists {
		dataValues, ok := value.([]any)
		if !ok {
			return nil, errors.New("embedding response data is not an array")
		}
		vectors := make([][]float64, len(dataValues))
		indexMode := -1
		for position, item := range dataValues {
			entry, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("embedding data item is not an object")
			}
			array, ok := entry["embedding"].([]any)
			if !ok {
				return nil, errors.New("embedding data item missing embedding")
			}
			vector, err := workflowVectorFromArray(array)
			if err != nil {
				return nil, err
			}
			rawIndex, hasIndex := entry["index"]
			mode := 0
			if hasIndex {
				mode = 1
			}
			if indexMode == -1 {
				indexMode = mode
			} else if indexMode != mode {
				return nil, errors.New("embedding response mixes indexed and unindexed items")
			}
			target := position
			if hasIndex {
				index, ok := rawIndex.(float64)
				if !ok || index != math.Trunc(index) || index < 0 || int(index) >= len(dataValues) {
					return nil, fmt.Errorf("embedding response index is invalid: %v", rawIndex)
				}
				target = int(index)
			}
			if vectors[target] != nil {
				return nil, fmt.Errorf("embedding response index %d is duplicated", target)
			}
			vectors[target] = vector
		}
		for index, vector := range vectors {
			if vector == nil {
				return nil, fmt.Errorf("embedding response index %d is missing", index)
			}
		}
		return vectors, nil
	}
	return nil, errors.New("embedding response missing data or embeddings")
}

func workflowVectorsFromArray(values []any) ([][]float64, error) {
	vectors := make([][]float64, 0, len(values))
	for _, item := range values {
		arr, ok := item.([]any)
		if !ok {
			return nil, errors.New("embedding item is not an array")
		}
		vector, err := workflowVectorFromArray(arr)
		if err != nil {
			return nil, err
		}
		vectors = append(vectors, vector)
	}
	return vectors, nil
}

func workflowVectorFromArray(values []any) ([]float64, error) {
	vector := make([]float64, 0, len(values))
	for _, value := range values {
		n, ok := value.(float64)
		if !ok {
			return nil, errors.New("embedding value is not a number")
		}
		vector = append(vector, n)
	}
	if len(vector) == 0 {
		return nil, errors.New("embedding vector is empty")
	}
	return vector, nil
}

func workflowTemplateVectorText(t workflowTemplate) string {
	parts := []string{t.ID, t.Version, t.Title, t.Description, t.Match.Type}
	parts = append(parts, t.Match.Keywords...)
	parts = append(parts, t.Match.Devices...)
	parts = append(parts, t.CompletionConditions...)
	for _, step := range t.Steps {
		parts = append(parts, step.ID, step.Title, step.Phase)
	}
	return strings.Join(normalizeWorkflowTexts(parts), "\n")
}

func cosineWorkflowVector(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func workflowTemplateVectorBonus(score float64) int {
	if score >= 0.85 {
		return 50
	}
	if score >= 0.75 {
		return 35
	}
	if score >= 0.65 {
		return 25
	}
	return 15
}

func workflowTemplateSummaryFromTemplate(t workflowTemplate) workflowTemplateSummary {
	fileName := t.ID + "@" + t.Version + ".json"
	return workflowTemplateSummary{ID: t.ID, Version: t.Version, Title: firstNonEmptyString(t.Title, t.ID), Description: t.Description, Status: string(t.Status), FileName: fileName, Path: filepath.ToSlash(filepath.Join("workflow-templates", "published", fileName)), StepCount: len(t.Steps), Keywords: t.Match.Keywords}
}

func workflowTemplateCompactList(templates []workflowTemplate) []map[string]any {
	out := make([]map[string]any, 0, len(templates))
	for _, t := range templates {
		out = append(out, map[string]any{"id": t.ID, "version": t.Version, "title": t.Title, "description": t.Description, "status": t.Status, "match": t.Match, "completion_conditions": t.CompletionConditions, "steps": t.Steps, "step_count": len(t.Steps), "hash": t.Hash, "published_at": t.PublishedAt, "retired_at": t.RetiredAt})
	}
	return out
}

func validateWorkflowTemplate(t workflowTemplate) error {
	if !validWorkflowTemplateToken(t.ID) || !validWorkflowTemplateToken(t.Version) {
		return errors.New("template id and version must contain only letters, numbers, dot, dash, or underscore")
	}
	if strings.TrimSpace(t.Title) == "" {
		return errors.New("template title is required")
	}
	if t.SourceEvolutionID != "" && !validWorkflowEvolutionID(t.SourceEvolutionID) {
		return errors.New("source_evolution_id must be a valid evo_ identifier")
	}
	if len(normalizeWorkflowTexts(t.CompletionConditions)) == 0 {
		return errors.New("template requires at least one completion condition")
	}
	if len(t.Steps) == 0 {
		return errors.New("template requires at least one step")
	}
	if err := validateWorkflowTemplateGuardrails(t); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, step := range t.Steps {
		if !validWorkflowTemplateToken(step.ID) || strings.TrimSpace(step.Title) == "" {
			return fmt.Errorf("invalid template step %q", step.ID)
		}
		if !validWorkflowPhase(step.Phase) {
			return fmt.Errorf("step %s has invalid phase %q", step.ID, step.Phase)
		}
		if seen[step.ID] {
			return fmt.Errorf("duplicate step id %q", step.ID)
		}
		seen[step.ID] = true
	}
	return nil
}

const maxWorkflowTemplateSteps = 8
const maxWorkflowTemplateConditions = 10

var sopWorkflowTemplateTerms = []string{"每条命令", "每个命令", "逐命令", "逐条命令", "记录证据", "补充证据", "再次记录", "逐项记录", "证据账本", "详细证据", "每一步", "每个步骤", "逐来源", "逐条", "分别记录"}

func validateWorkflowTemplateGuardrails(t workflowTemplate) error {
	if t.AllowLongTemplate && len([]rune(strings.TrimSpace(t.LongTemplateReason))) < 12 {
		return errors.New("long_template_reason is required when allow_long_template=true")
	}
	if !t.AllowLongTemplate && len(t.Steps) > maxWorkflowTemplateSteps {
		return fmt.Errorf("template has %d steps; max %d unless allow_long_template=true with long_template_reason", len(t.Steps), maxWorkflowTemplateSteps)
	}
	if !t.AllowLongTemplate && len(normalizeWorkflowTexts(t.CompletionConditions)) > maxWorkflowTemplateConditions {
		return fmt.Errorf("template has %d completion conditions; max %d unless allow_long_template=true with long_template_reason", len(normalizeWorkflowTexts(t.CompletionConditions)), maxWorkflowTemplateConditions)
	}
	texts := []string{t.Title, t.Description, t.LongTemplateReason}
	texts = append(texts, t.CompletionConditions...)
	for _, step := range t.Steps {
		texts = append(texts, step.ID, step.Title)
	}
	for _, text := range texts {
		for _, term := range sopWorkflowTemplateTerms {
			if strings.Contains(text, term) {
				return fmt.Errorf("template text looks like verbose SOP or evidence ledger; move details to script/Skill/runbook instead of using term %q", term)
			}
		}
	}
	return nil
}

func validWorkflowEvolutionID(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "evo_") || len(value) < len("evo_")+16 || len(value) > len("evo_")+64 {
		return false
	}
	for _, r := range value[len("evo_"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validWorkflowPhase(value string) bool {
	switch value {
	case "check", "execute", "verify", "closeout":
		return true
	default:
		return false
	}
}
func validWorkflowTemplateToken(v string) bool {
	if strings.TrimSpace(v) == "" {
		return false
	}
	for _, r := range v {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func workflowTemplateHash(t workflowTemplate) string {
	t.Hash = ""
	t.PublishedAt = nil
	t.RetiredAt = nil
	data, _ := json.Marshal(t)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func writeWorkflowTemplateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncWorkflowTemplateDirectory(path)
}

func removeWorkflowTemplateFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncWorkflowTemplateDirectory(path)
}

func syncWorkflowTemplateDirectory(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func normalizeWorkflowTexts(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
func latestWorkflowTemplateVersions(templates []workflowTemplate) []workflowTemplate {
	byID := map[string]workflowTemplate{}
	for _, template := range templates {
		current, exists := byID[template.ID]
		if !exists || compareWorkflowVersions(template.Version, current.Version) > 0 {
			byID[template.ID] = template
		}
	}
	out := make([]workflowTemplate, 0, len(byID))
	for _, template := range byID {
		out = append(out, template)
	}
	return out
}
func weakWorkflowTemplateKeyword(keyword string) bool {
	switch workflowTemplateMatchText(keyword) {
	case "agentdock", "nexus", "vitapulse":
		return true
	default:
		return false
	}
}
func workflowTemplateMatchText(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if r <= ' ' || r == '个' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
func containsWorkflowTemplateHint(values []string, hint string) bool {
	hint = workflowTemplateMatchText(hint)
	if hint == "" {
		return false
	}
	for _, value := range values {
		if workflowTemplateMatchText(value) == hint {
			return true
		}
	}
	return false
}
func workflowMatchRecommendation(candidates []workflowTemplateCandidate) map[string]any {
	best := 0
	if len(candidates) > 0 {
		best = candidates[0].Score
	}
	recommended := "plain_task"
	reason := "no active template is specific enough; create a plain recoverable task"
	if best >= 85 {
		recommended = "use_template"
		reason = "top candidate score is strong enough to select by default"
	} else if best >= 60 {
		recommended = "consider_template"
		reason = "top candidate is plausible but should be checked against the user goal"
	}
	return map[string]any{"recommended": recommended, "recommendation_reason": reason, "best_candidate_score": best, "score_thresholds": map[string]any{"use_template": 85, "consider_template": 60, "plain_task_below": 60}}
}
