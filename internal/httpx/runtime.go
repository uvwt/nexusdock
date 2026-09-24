package httpx

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/agentdock"
)

const (
	runtimeTaskListLimit = 200
	recentTaskWindow     = 24 * time.Hour
)

// 以下结构体是 Runtime 视图的 UI API JSON 形状。字段值全部来自 internal/agentdock
// 解析出的 DTO，本包不再接触 AgentDock 上游的动态 map 结构。

type runtimeTaskStep struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Phase     string `json:"phase,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type runtimeTaskSummary struct {
	ID                 string           `json:"id"`
	Title              string           `json:"title"`
	Goal               string           `json:"goal"`
	Status             string           `json:"status"`
	Phase              string           `json:"phase"`
	ReviewStatus       string           `json:"review_status"`
	Summary            string           `json:"summary,omitempty"`
	Blocker            string           `json:"blocker,omitempty"`
	CurrentStep        *runtimeTaskStep `json:"current_step,omitempty"`
	CompletedStepCount int              `json:"completed_step_count"`
	StepCount          int              `json:"step_count"`
	UpdatedAt          string           `json:"updated_at"`
	CreatedAt          string           `json:"created_at"`
	TemplateID         string           `json:"template_id,omitempty"`
	TemplateVersion    string           `json:"template_version,omitempty"`
	ConditionCount     int              `json:"condition_count"`
	EventCount         int              `json:"event_count"`
	FileName           string           `json:"file_name"`
}

type runtimeTaskCondition struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at,omitempty"`
}

type runtimeTaskEvent struct {
	Type      string `json:"type,omitempty"`
	Summary   string `json:"summary,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type runtimeTaskFinalReview struct {
	Status         string   `json:"status"`
	Summary        string   `json:"summary,omitempty"`
	VerifiedFacts  []string `json:"verified_facts,omitempty"`
	OpenRisks      []string `json:"open_risks,omitempty"`
	MissingChecks  []string `json:"missing_checks,omitempty"`
	ReviewRevision string   `json:"review_revision"`
	ReviewedAt     string   `json:"reviewed_at,omitempty"`
}

type runtimeTaskDetail struct {
	runtimeTaskSummary
	Path        string                  `json:"path"`
	Conditions  []runtimeTaskCondition  `json:"conditions"`
	Steps       []runtimeTaskStep       `json:"steps"`
	Events      []runtimeTaskEvent      `json:"events"`
	FinalReview *runtimeTaskFinalReview `json:"final_review,omitempty"`
}

type runtimeSkillSummary struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Source        string `json:"source"`
	Path          string `json:"path"`
	Description   string `json:"description"`
	FileCount     int    `json:"file_count"`
	Status        string `json:"status"`
	SkillRef      string `json:"skill_ref"`
	SourceType    string `json:"source_type"`
	PluginName    string `json:"plugin_name,omitempty"`
	ContentDigest string `json:"content_digest"`
}

type runtimeSkillDetail struct {
	runtimeSkillSummary
	// RuntimeState 是上游 Skill 详情的原始 JSON，供 UI 的“原始响应”调试面板透传展示；
	// Nexus 不解读其内容，因此保留 RawMessage 而不是映射成结构体。
	RuntimeState json.RawMessage    `json:"runtime_state,omitempty"`
	Files        []runtimeSkillFile `json:"files"`
}

type runtimeSkillFile struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
}

type runtimeSkillFileContent struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

func (s *Server) registerRuntimeRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	s.registerAgentDockNodeRoutes(mux, protected)
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/overview", protected(s.runtimeOverview))
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/tasks", protected(s.runtimeTasks))
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/tasks/{fileName}", protected(s.runtimeTaskDetail))
	mux.HandleFunc("DELETE /v1/runtime/nodes/{nodeID}/tasks/{fileName}", protected(s.runtimeDeleteTask))
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/skills", protected(s.runtimeSkills))
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/skills/{source}/{skillID}/files/{filePath...}", protected(s.runtimeSkillFile))
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/skills/{source}/{skillID}", protected(s.runtimeSkillDetail))
	s.registerRuntimePluginRoutes(mux, protected)
	s.registerRuntimeMCPRoutes(mux, protected)
}

func (s *Server) runtimeOverview(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")
	tasks, taskErr := s.agentDockHub.RuntimeTasks(r.Context(), nodeID, runtimeTaskListLimit)
	skills, skillErr := s.agentDockHub.RuntimeSkills(r.Context(), nodeID)
	servers, mcpErr := s.agentDockHub.RuntimeMCPServers(r.Context(), nodeID)
	// 概览只展示前 6 个 Skill，先按安装名排序保证结果稳定。
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].Skill < skills[j].Skill })
	counts := map[string]int{"active": 0, "completed": 0, "blocked": 0, "active_recent_24h": 0}
	recentCutoff := time.Now().UTC().Add(-recentTaskWindow)
	for _, task := range tasks {
		counts[task.Status]++
		if task.Status == "active" && taskUpdatedSince(task.UpdatedAt, recentCutoff) {
			counts["active_recent_24h"]++
		}
	}
	skillItems := make([]runtimeSkillSummary, 0, len(skills))
	for _, skill := range skills {
		skillItems = append(skillItems, runtimeSkillSummaryView(skill))
	}
	payload := map[string]any{
		"ok":         taskErr == nil && skillErr == nil && mcpErr == nil,
		"tasks":      counts,
		"skills":     map[string]any{"count": len(skillItems), "items": firstSkills(skillItems, 6)},
		"mcp":        map[string]any{"count": len(servers)},
		"paths":      s.opsPaths(),
		"node_id":    nodeID,
		"source":     "agentdock-runtime-api",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if taskErr != nil || skillErr != nil || mcpErr != nil {
		err := firstOpsError(taskErr, skillErr, mcpErr)
		writeRuntimeUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func taskUpdatedSince(value string, cutoff time.Time) bool {
	updatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	return err == nil && !updatedAt.Before(cutoff)
}

func (s *Server) runtimeTasks(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := queryInt(r, "limit", runtimeTaskListLimit)
	if limit > runtimeTaskListLimit {
		limit = runtimeTaskListLimit
	}
	tasks, err := s.agentDockHub.RuntimeTasks(r.Context(), nodeID, limit)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	filtered := make([]runtimeTaskSummary, 0, len(tasks))
	for _, task := range tasks {
		if status != "" && status != "all" && task.Status != status {
			continue
		}
		currentStep := ""
		if task.CurrentStep != nil {
			currentStep = task.CurrentStep.Title
		}
		if query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{task.ID, task.Title, task.Goal, task.Status, task.Summary, task.Blocker, currentStep}, " ")), query) {
			continue
		}
		filtered = append(filtered, runtimeTaskSummaryView(task))
		if len(filtered) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node_id": nodeID, "items": filtered, "count": len(filtered), "total": len(tasks), "source": "agentdock-runtime-api"})
}

func (s *Server) runtimeTaskDetail(w http.ResponseWriter, r *http.Request) {
	id, err := cleanOpsTaskID(r.PathValue("fileName"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_TASK_ID", err.Error())
		return
	}
	nodeID := r.PathValue("nodeID")
	detail, err := s.agentDockHub.RuntimeTask(r.Context(), nodeID, id)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node_id": nodeID, "task": runtimeTaskDetailView(detail), "source": "agentdock-runtime-api"})
}

func (s *Server) runtimeDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := cleanOpsTaskID(r.PathValue("fileName"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_TASK_ID", err.Error())
		return
	}
	nodeID := r.PathValue("nodeID")
	result, err := s.agentDockHub.RuntimeDeleteTask(r.Context(), nodeID, id)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	payload := map[string]any{"ok": true, "task_id": result.TaskID, "node_id": nodeID, "source": "agentdock-runtime-api"}
	if len(result.DeletedTask) > 0 {
		payload["deleted_task"] = json.RawMessage(result.DeletedTask)
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) runtimeSkills(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")
	skills, err := s.agentDockHub.RuntimeSkills(r.Context(), nodeID)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].Skill < skills[j].Skill })
	items := make([]runtimeSkillSummary, 0, len(skills))
	for _, skill := range skills {
		items = append(items, runtimeSkillSummaryView(skill))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node_id": nodeID, "items": items, "count": len(items), "source": "agentdock-runtime-api"})
}

func (s *Server) runtimeSkillDetail(w http.ResponseWriter, r *http.Request) {
	skillID, err := cleanOpsName(r.PathValue("skillID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SKILL_ID", err.Error())
		return
	}
	nodeID := r.PathValue("nodeID")
	detail, raw, err := s.agentDockHub.RuntimeSkill(r.Context(), nodeID, skillID, r.URL.Query().Get("skill_ref"))
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node_id": nodeID, "skill": runtimeSkillDetailView(skillID, detail, raw), "source": "agentdock-runtime-api"})
}

func (s *Server) runtimeSkillFile(w http.ResponseWriter, r *http.Request) {
	skillID, err := cleanOpsName(r.PathValue("skillID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SKILL_ID", err.Error())
		return
	}
	relativePath, err := cleanRuntimeSkillFilePath(r.PathValue("filePath"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SKILL_FILE", err.Error())
		return
	}
	nodeID := r.PathValue("nodeID")
	file, err := s.agentDockHub.RuntimeSkillFile(r.Context(), nodeID, skillID, r.URL.Query().Get("skill_ref"), urlPathSegments(relativePath))
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node_id": nodeID, "file": runtimeSkillFileContentView(file), "source": "agentdock-runtime-api"})
}

// runtimeTaskSummaryView 把上游任务 DTO 映射成 UI API 摘要；file_name 是 UI 侧任务文件名，等于任务 ID。
func runtimeTaskSummaryView(task agentdock.RuntimeTaskSummary) runtimeTaskSummary {
	var currentStep *runtimeTaskStep
	if task.CurrentStep != nil {
		currentStep = &runtimeTaskStep{ID: task.CurrentStep.ID, Title: task.CurrentStep.Title, Status: task.CurrentStep.Status}
	}
	return runtimeTaskSummary{
		ID:                 task.ID,
		Title:              task.Title,
		Goal:               task.Goal,
		Status:             task.Status,
		Phase:              task.Phase,
		ReviewStatus:       task.ReviewStatus,
		Summary:            task.Summary,
		Blocker:            task.Blocker,
		CurrentStep:        currentStep,
		CompletedStepCount: task.CompletedStepCount,
		StepCount:          task.StepCount,
		UpdatedAt:          task.UpdatedAt,
		CreatedAt:          task.CreatedAt,
		TemplateID:         task.TemplateID,
		TemplateVersion:    task.TemplateVersion,
		ConditionCount:     task.ConditionCount,
		EventCount:         task.EventCount,
		FileName:           task.ID,
	}
}

func runtimeTaskDetailView(detail agentdock.RuntimeTaskDetail) runtimeTaskDetail {
	steps := make([]runtimeTaskStep, 0, len(detail.Steps))
	for _, step := range detail.Steps {
		steps = append(steps, runtimeTaskStep{ID: step.ID, Title: step.Title, Status: step.Status, Phase: step.Phase, UpdatedAt: step.UpdatedAt})
	}
	conditions := make([]runtimeTaskCondition, 0, len(detail.Conditions))
	for _, condition := range detail.Conditions {
		conditions = append(conditions, runtimeTaskCondition{ID: condition.ID, Text: condition.Text, CreatedAt: condition.CreatedAt})
	}
	events := make([]runtimeTaskEvent, 0, len(detail.Events))
	for _, event := range detail.Events {
		events = append(events, runtimeTaskEvent{Type: event.Type, Summary: event.Summary, CreatedAt: event.CreatedAt})
	}
	view := runtimeTaskDetail{
		runtimeTaskSummary: runtimeTaskSummaryView(detail.Summary),
		Path:               "agentdock-runtime-api",
		Conditions:         conditions,
		Steps:              steps,
		Events:             events,
	}
	if detail.FinalReview != nil {
		view.FinalReview = &runtimeTaskFinalReview{
			Status:         detail.FinalReview.Status,
			Summary:        detail.FinalReview.Summary,
			VerifiedFacts:  detail.FinalReview.VerifiedFacts,
			OpenRisks:      detail.FinalReview.OpenRisks,
			MissingChecks:  detail.FinalReview.MissingChecks,
			ReviewRevision: detail.FinalReview.ReviewRevision,
			ReviewedAt:     detail.FinalReview.ReviewedAt,
		}
	}
	return view
}

// runtimeSkillSummaryView 把上游 Skill DTO 映射成 UI API 摘要。
// source/path/status 是 UI 侧的展示约定：Skill 一律来自 agentdock-api 且视为已安装。
func runtimeSkillSummaryView(skill agentdock.RuntimeSkillSummary) runtimeSkillSummary {
	title := skill.Name
	if strings.TrimSpace(title) == "" {
		title = skill.Skill
	}
	return runtimeSkillSummary{
		ID:            skill.Skill,
		Title:         title,
		Source:        "agentdock-api",
		Path:          "agentdock-api/" + skill.Skill,
		Description:   skill.Description,
		FileCount:     skill.FileCount,
		Status:        "installed",
		SkillRef:      skill.SkillRef,
		SourceType:    skill.SourceType,
		PluginName:    skill.PluginName,
		ContentDigest: skill.ContentDigest,
	}
}

func runtimeSkillDetailView(skillID string, detail agentdock.RuntimeSkillDetail, raw json.RawMessage) runtimeSkillDetail {
	view := runtimeSkillDetail{
		runtimeSkillSummary: runtimeSkillSummaryView(agentdock.RuntimeSkillSummary{
			Skill: skillID, Name: detail.Name, Description: detail.Description,
			SkillRef: detail.SkillRef, SourceType: detail.SourceType, PluginName: detail.PluginName,
			ContentDigest: detail.ContentDigest,
		}),
		RuntimeState: raw,
		Files:        make([]runtimeSkillFile, 0, len(detail.Files)),
	}
	view.FileCount = len(detail.Files)
	for _, file := range detail.Files {
		view.Files = append(view.Files, runtimeSkillFile{Path: file.Path, Kind: file.Kind, SizeBytes: file.SizeBytes, UpdatedAt: file.UpdatedAt})
	}
	return view
}

func runtimeSkillFileContentView(file agentdock.RuntimeSkillFileContent) runtimeSkillFileContent {
	return runtimeSkillFileContent{
		Path: file.Path, Kind: file.Kind, SizeBytes: file.SizeBytes,
		UpdatedAt: file.UpdatedAt, Content: file.Content, Truncated: file.Truncated,
	}
}

func cleanOpsTaskID(value string) (string, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".json")
	return cleanOpsName(value)
}

func urlPathSegments(value string) string {
	parts := strings.Split(value, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func cleanRuntimeSkillFilePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("invalid Skill file path")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", fmt.Errorf("invalid Skill file path")
		}
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid Skill file path")
	}
	return clean, nil
}

func firstOpsError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) opsPaths() map[string]string {
	return map[string]string{"agentdock": "agentdock-runtime-api"}
}

func cleanOpsName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value != filepath.Base(value) || strings.Contains(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "..") {
		return "", fmt.Errorf("invalid name")
	}
	return value, nil
}

func firstSkills(items []runtimeSkillSummary, n int) []runtimeSkillSummary {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func modTime(info fs.FileInfo) string {
	if info == nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339Nano)
}

func fileSize(info fs.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.Size()
}
