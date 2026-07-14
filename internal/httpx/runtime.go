package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	runtimeTaskListLimit    = 200
	maxSkillFilePreviewSize = 256 * 1024
)

type opsTaskStep struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type opsTaskSummary struct {
	ID                 string       `json:"id"`
	Title              string       `json:"title"`
	Goal               string       `json:"goal"`
	Status             string       `json:"status"`
	Phase              string       `json:"phase"`
	ReviewStatus       string       `json:"review_status"`
	Summary            string       `json:"summary,omitempty"`
	Blocker            string       `json:"blocker,omitempty"`
	CurrentStep        *opsTaskStep `json:"current_step,omitempty"`
	CompletedStepCount int          `json:"completed_step_count"`
	UpdatedAt          string       `json:"updated_at"`
	CreatedAt          string       `json:"created_at"`
	TemplateID         string       `json:"template_id,omitempty"`
	TemplateVersion    string       `json:"template_version,omitempty"`
	ConditionCount     int          `json:"condition_count"`
	StepCount          int          `json:"step_count"`
	AttemptCount       int          `json:"attempt_count"`
	EventCount         int          `json:"event_count"`
	FileName           string       `json:"file_name"`
}

type opsTaskDetail struct {
	opsTaskSummary
	Path        string         `json:"path"`
	Content     string         `json:"content"`
	JSON        map[string]any `json:"json"`
	Conditions  []any          `json:"conditions,omitempty"`
	Steps       []any          `json:"steps,omitempty"`
	Attempts    []any          `json:"attempts,omitempty"`
	Events      []any          `json:"events,omitempty"`
	FinalReview map[string]any `json:"final_review,omitempty"`
}

type opsSkillSummary struct {
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	Source           string            `json:"source"`
	Path             string            `json:"path"`
	Description      string            `json:"description,omitempty"`
	UpdatedAt        string            `json:"updated_at"`
	FileCount        int               `json:"file_count"`
	Status           string            `json:"status"`
	ActiveVersion    string            `json:"active_version,omitempty"`
	Versions         []string          `json:"versions,omitempty"`
	Channels         map[string]string `json:"channels,omitempty"`
	RuntimeStatePath string            `json:"runtime_state_path,omitempty"`
	DocRoot          string            `json:"doc_root,omitempty"`
}

type opsSkillFile struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
}

type opsSkillDetail struct {
	opsSkillSummary
	Root         string         `json:"root"`
	SkillDoc     string         `json:"skill_doc,omitempty"`
	Files        []opsSkillFile `json:"files"`
	RuntimeState map[string]any `json:"runtime_state,omitempty"`
}

type opsSkillFileContent struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type opsSkillRuntimeState struct {
	ActiveVersion string            `json:"active_version"`
	Channels      map[string]string `json:"channels"`
	History       []string          `json:"history"`
	UpdatedAt     string            `json:"updated_at"`
}

type opsSkillStateRecord struct {
	ID      string
	Path    string
	ModTime string
	Raw     map[string]any
	State   opsSkillRuntimeState
}

func (s *Server) registerRuntimeRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/runtime/overview", protected(s.runtimeOverview))
	mux.HandleFunc("GET /v1/runtime/tasks", protected(s.runtimeTasks))
	mux.HandleFunc("GET /v1/runtime/tasks/{fileName}", protected(s.runtimeTaskDetail))
	mux.HandleFunc("DELETE /v1/runtime/tasks/{fileName}", protected(s.runtimeDeleteTask))
	mux.HandleFunc("GET /v1/runtime/skills", protected(s.runtimeSkills))
	mux.HandleFunc("GET /v1/runtime/skills/{source}/{skillID}/files/{filePath...}", protected(s.runtimeSkillFile))
	mux.HandleFunc("GET /v1/runtime/skills/{source}/{skillID}", protected(s.runtimeSkillDetail))
	mux.HandleFunc("GET /v1/runtime/workflow-templates", protected(s.listRuntimeWorkflowTemplates))
	mux.HandleFunc("GET /v1/runtime/workflow-templates/", protected(s.runtimeWorkflowTemplateDetail))
	s.registerRuntimeMCPRoutes(mux, protected)
}

func (s *Server) runtimeOverview(w http.ResponseWriter, r *http.Request) {
	tasks, taskErr := s.collectOpsTasksFromRuntime(r.Context(), runtimeTaskListLimit)
	skills, skillErr := s.collectOpsSkillsFromRuntime(r.Context())
	counts := map[string]int{"active": 0, "completed": 0, "blocked": 0}
	for _, task := range tasks {
		counts[task.Status]++
	}
	payload := map[string]any{
		"ok":         taskErr == nil && skillErr == nil,
		"tasks":      counts,
		"skills":     map[string]any{"count": len(skills), "items": firstSkills(skills, 6)},
		"workflows":  s.workflowCountsFromRuntime(r.Context()),
		"paths":      s.opsPaths(),
		"source":     "agentdock-runtime-api",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if taskErr != nil || skillErr != nil {
		payload["runtime"] = runtimeUnavailablePayload(firstOpsError(taskErr, skillErr))
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) runtimeTasks(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := queryInt(r, "limit", runtimeTaskListLimit)
	if limit > runtimeTaskListLimit {
		limit = runtimeTaskListLimit
	}
	items, err := s.collectOpsTasksFromRuntime(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, runtimeUnavailablePayload(err))
		return
	}
	filtered := make([]opsTaskSummary, 0, len(items))
	for _, item := range items {
		if status != "" && status != "all" && item.Status != status {
			continue
		}
		currentStep := ""
		if item.CurrentStep != nil {
			currentStep = item.CurrentStep.Title
		}
		if query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{item.ID, item.Title, item.Goal, item.Status, item.Summary, item.Blocker, currentStep}, " ")), query) {
			continue
		}
		filtered = append(filtered, item)
		if len(filtered) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": filtered, "count": len(filtered), "total": len(items), "source": "agentdock-runtime-api"})
}

func (s *Server) runtimeTaskDetail(w http.ResponseWriter, r *http.Request) {
	id, err := cleanOpsTaskID(r.PathValue("fileName"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_TASK_ID", err.Error())
		return
	}
	detail, err := s.runtimeTaskDetailFromRuntime(r.Context(), id)
	if err != nil {
		writeJSON(w, runtimeErrorHTTPStatus(err), runtimeUnavailablePayload(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "task": detail, "source": "agentdock-runtime-api"})
}

func (s *Server) runtimeDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := cleanOpsTaskID(r.PathValue("fileName"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_TASK_ID", err.Error())
		return
	}
	payload, err := s.runtimeDelete(r.Context(), "/internal/runtime/tasks/"+urlPath(id))
	if err != nil {
		writeJSON(w, runtimeErrorHTTPStatus(err), runtimeUnavailablePayload(err))
		return
	}
	payload["source"] = "agentdock-runtime-api"
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) runtimeSkills(w http.ResponseWriter, r *http.Request) {
	items, err := s.collectOpsSkillsFromRuntime(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, runtimeUnavailablePayload(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items, "count": len(items), "source": "agentdock-runtime-api"})
}

func (s *Server) runtimeSkillDetail(w http.ResponseWriter, r *http.Request) {
	skillID, err := cleanOpsName(r.PathValue("skillID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SKILL_ID", err.Error())
		return
	}
	detail, err := s.runtimeSkillDetailFromRuntime(r.Context(), skillID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, runtimeUnavailablePayload(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "skill": detail, "source": "agentdock-runtime-api"})
}

func (s *Server) runtimeSkillFile(w http.ResponseWriter, r *http.Request) {
	skillID, err := cleanOpsName(r.PathValue("skillID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SKILL_ID", err.Error())
		return
	}
	detail, err := s.runtimeSkillDetailFromRuntime(r.Context(), skillID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, runtimeUnavailablePayload(err))
		return
	}
	relativePath := filepath.Clean(filepath.FromSlash(strings.TrimSpace(r.PathValue("filePath"))))
	if relativePath == "." || filepath.IsAbs(relativePath) || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
		writeError(w, http.StatusBadRequest, "INVALID_SKILL_FILE", "Skill 文件路径无效")
		return
	}
	if isPrivateSkillFilePath(relativePath) {
		writeError(w, http.StatusNotFound, "SKILL_FILE_NOT_FOUND", "Skill 文件不存在")
		return
	}
	if detail.Root == "" || detail.Root == "agentdock-runtime-api" {
		if strings.EqualFold(filepath.ToSlash(relativePath), "SKILL.md") && detail.SkillDoc != "" {
			content := opsSkillFileContent{Path: "SKILL.md", Kind: "doc", SizeBytes: int64(len(detail.SkillDoc)), UpdatedAt: detail.UpdatedAt, Content: detail.SkillDoc}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "file": content})
			return
		}
		writeError(w, http.StatusNotFound, "SKILL_FILES_UNAVAILABLE", "Skill 文件目录不可用")
		return
	}
	target := filepath.Join(detail.Root, relativePath)
	rootResolved, rootErr := filepath.EvalSymlinks(detail.Root)
	targetResolved, targetErr := filepath.EvalSymlinks(target)
	if rootErr != nil || targetErr != nil {
		writeError(w, http.StatusNotFound, "SKILL_FILE_NOT_FOUND", "Skill 文件不存在")
		return
	}
	resolved, err := filepath.Rel(rootResolved, targetResolved)
	if err != nil || resolved == ".." || strings.HasPrefix(resolved, ".."+string(os.PathSeparator)) {
		writeError(w, http.StatusBadRequest, "INVALID_SKILL_FILE", "Skill 文件路径超出安装目录")
		return
	}
	target = targetResolved
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "SKILL_FILE_NOT_FOUND", "Skill 文件不存在")
		return
	}
	file, err := os.Open(target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SKILL_FILE_READ_FAILED", err.Error())
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSkillFilePreviewSize+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SKILL_FILE_READ_FAILED", err.Error())
		return
	}
	truncated := len(data) > maxSkillFilePreviewSize
	if truncated {
		data = data[:maxSkillFilePreviewSize]
	}
	if !utf8.Valid(data) {
		writeError(w, http.StatusUnsupportedMediaType, "SKILL_FILE_BINARY", "该文件不是可预览的文本文件")
		return
	}
	content := opsSkillFileContent{Path: filepath.ToSlash(relativePath), Kind: skillFileKind(relativePath), SizeBytes: info.Size(), UpdatedAt: modTime(info), Content: string(data), Truncated: truncated}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "file": content})
}

func (s *Server) collectOpsTasks() []opsTaskSummary {
	items, err := s.collectOpsTasksFromRuntime(context.Background(), runtimeTaskListLimit)
	if err != nil {
		return nil
	}
	return items
}

func (s *Server) collectOpsTasksFromRuntime(ctx context.Context, limit int) ([]opsTaskSummary, error) {
	body, err := s.runtimeGet(ctx, "/internal/runtime/tasks", runtimeQueryLimitStatus(limit, ""))
	if err != nil {
		return nil, err
	}
	items := make([]opsTaskSummary, 0, len(opsArray(body["tasks"])))
	for _, raw := range opsArray(body["tasks"]) {
		m := opsMap(raw)
		id := firstNonEmptyString(opsString(m["id"]), opsString(m["task_id"]))
		if id == "" {
			continue
		}
		summary := opsTaskSummary{
			ID: id, Title: opsString(m["title"]), Goal: opsString(m["goal"]),
			Status: firstNonEmptyString(opsString(m["status"]), "unknown"), Phase: opsString(m["phase"]), ReviewStatus: firstNonEmptyString(opsString(m["review_status"]), "not_started"),
			Summary: opsString(m["summary"]), Blocker: opsString(m["blocker"]), CurrentStep: opsTaskStepFromValue(m["current_step"]),
			CompletedStepCount: opsInt(m["completed_step_count"]), StepCount: opsInt(m["step_count"]),
			UpdatedAt: opsString(m["updated_at"]), CreatedAt: opsString(m["created_at"]),
			TemplateID: opsString(m["template_id"]), TemplateVersion: opsString(m["template_version"]),
			ConditionCount: opsInt(m["condition_count"]), AttemptCount: opsInt(m["attempt_count"]), EventCount: opsInt(m["event_count"]), FileName: id,
		}
		items = append(items, summary)
	}
	return items, nil
}

func (s *Server) runtimeTaskDetailFromRuntime(ctx context.Context, id string) (opsTaskDetail, error) {
	body, err := s.runtimeGet(ctx, "/internal/runtime/tasks/"+urlPath(id), nil)
	if err != nil {
		return opsTaskDetail{}, err
	}
	task := opsMap(body["task"])
	summary := opsTaskSummaryFromMap(task)
	if summary.ID == "" {
		summary.ID = id
		summary.FileName = id
	}
	return opsTaskDetail{opsTaskSummary: summary, Path: "agentdock-runtime-api", Conditions: opsArray(task["conditions"]), Steps: opsArray(task["steps"]), Attempts: opsArray(task["attempts"]), Events: opsArray(task["events"]), FinalReview: opsMap(task["final_review"])}, nil
}

func opsTaskSummaryFromMap(task map[string]any) opsTaskSummary {
	finalReview := opsMap(task["final_review"])
	review := firstNonEmptyString(opsString(task["review_status"]), opsString(finalReview["status"]), "not_started")
	template := opsMap(task["template"])
	steps := opsArray(task["steps"])
	completedSteps, currentStep := opsTaskProgress(steps)
	return opsTaskSummary{
		ID: opsString(task["id"]), Title: opsString(task["title"]), Goal: opsString(task["goal"]),
		Status: firstNonEmptyString(opsString(task["status"]), "unknown"), Phase: opsString(task["phase"]), ReviewStatus: review,
		Summary: opsString(task["summary"]), Blocker: opsString(task["blocker"]), CurrentStep: currentStep, CompletedStepCount: completedSteps,
		UpdatedAt: opsString(task["updated_at"]), CreatedAt: opsString(task["created_at"]),
		TemplateID: opsString(template["id"]), TemplateVersion: opsString(template["version"]),
		ConditionCount: len(opsArray(task["conditions"])), StepCount: len(steps), AttemptCount: len(opsArray(task["attempts"])), EventCount: len(opsArray(task["events"])), FileName: opsString(task["id"]),
	}
}

func opsTaskStepFromValue(value any) *opsTaskStep {
	step := opsMap(value)
	if len(step) == 0 {
		return nil
	}
	result := &opsTaskStep{ID: opsString(step["id"]), Title: opsString(step["title"]), Status: opsString(step["status"])}
	if result.ID == "" && result.Title == "" {
		return nil
	}
	return result
}

func opsTaskProgress(steps []any) (int, *opsTaskStep) {
	completed := 0
	var current, pending *opsTaskStep
	for _, raw := range steps {
		step := opsTaskStepFromValue(raw)
		if step == nil {
			continue
		}
		switch step.Status {
		case "completed":
			completed++
		case "in_progress":
			if current == nil {
				current = step
			}
		case "pending":
			if pending == nil {
				pending = step
			}
		}
	}
	if current != nil {
		return completed, current
	}
	return completed, pending
}

func (s *Server) collectOpsSkillsFromRuntime(ctx context.Context) ([]opsSkillSummary, error) {
	body, err := s.runtimeGet(ctx, "/internal/runtime/skills", nil)
	if err != nil {
		return nil, err
	}
	items := make([]opsSkillSummary, 0, len(opsArray(body["skills"])))
	for _, raw := range opsArray(body["skills"]) {
		m := opsMap(raw)
		id := firstNonEmptyString(opsString(m["skill"]), opsString(m["id"]), opsString(m["name"]))
		if id == "" {
			continue
		}
		selection := opsMap(m["selection"])
		channels := opsStringMap(firstNonNil(m["channels"], selection["channels"]))
		active := firstNonEmptyString(opsString(m["active_version"]), opsString(selection["active_version"]))
		versions := opsStringArray(m["versions"])
		summary := opsSkillSummary{ID: id, Title: id, Source: "agentdock-api", Path: filepath.ToSlash(filepath.Join("agentdock-api", id)), UpdatedAt: firstNonEmptyString(opsString(m["updated_at"]), opsString(selection["updated_at"])), Status: "installed", ActiveVersion: active, Versions: versions, Channels: channels}
		items = append(items, s.enrichOpsSkillSummary(summary))
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *Server) runtimeSkillDetailFromRuntime(ctx context.Context, skillID string) (opsSkillDetail, error) {
	body, err := s.runtimeGet(ctx, "/internal/runtime/skills/"+urlPath(skillID), nil)
	if err != nil {
		return opsSkillDetail{}, err
	}
	document := opsMap(body["document"])
	selection := opsMap(body["selection"])
	versions := opsStringArray(body["versions"])
	channels := opsStringMap(selection["channels"])
	active := firstNonEmptyString(opsString(body["version"]), opsString(selection["active_version"]), opsString(document["version"]))
	title := firstNonEmptyString(opsString(document["name"]), skillID)
	desc := opsString(document["description"])
	summary := s.enrichOpsSkillSummary(opsSkillSummary{ID: skillID, Title: title, Source: "agentdock-api", Path: filepath.ToSlash(filepath.Join("agentdock-api", skillID)), Description: desc, UpdatedAt: opsString(selection["updated_at"]), Status: "installed", ActiveVersion: active, Versions: versions, Channels: channels})
	detail := opsSkillDetail{opsSkillSummary: summary, Root: "agentdock-runtime-api", SkillDoc: skillDocumentText(document), Files: []opsSkillFile{}, RuntimeState: body}
	if root := s.opsSkillPackageRoot(skillID, active); root != "" {
		detail.Root = filepath.ToSlash(root)
		detail.SkillDoc = readSmallText(filepath.Join(root, "SKILL.md"), 32000)
		detail.Files = collectOpsSkillFiles(root, 4, 160)
	} else if detail.SkillDoc != "" {
		detail.Files = []opsSkillFile{{Path: "SKILL.md", Kind: "doc", SizeBytes: int64(len(detail.SkillDoc)), UpdatedAt: summary.UpdatedAt}}
	}
	detail.FileCount = len(detail.Files)
	return detail, nil
}

func skillDocumentText(document map[string]any) string {
	name := strings.TrimSpace(opsString(document["name"]))
	description := strings.TrimSpace(opsString(document["description"]))
	version := strings.TrimSpace(opsString(document["version"]))
	body := strings.TrimSpace(opsString(document["body"]))
	if name == "" || description == "" || version == "" || body == "" {
		return ""
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\nversion: %s\n---\n\n%s\n", strconv.Quote(name), strconv.Quote(description), strconv.Quote(version), body)
}

func (s *Server) enrichOpsSkillSummary(summary opsSkillSummary) opsSkillSummary {
	root := s.opsSkillPackageRoot(summary.ID, summary.ActiveVersion)
	if root == "" {
		return summary
	}
	title, description := readSkillDoc(filepath.Join(root, "SKILL.md"))
	summary.Title = firstNonEmptyString(title, summary.Title, summary.ID)
	summary.Description = firstNonEmptyString(description, summary.Description)
	summary.FileCount = len(collectOpsSkillFiles(root, 4, 160))
	summary.DocRoot = filepath.ToSlash(root)
	if info, err := os.Stat(root); err == nil {
		summary.UpdatedAt = firstNonEmptyString(summary.UpdatedAt, modTime(info))
	}
	return summary
}

func (s *Server) opsSkillPackageRoot(skillID, version string) string {
	if strings.TrimSpace(s.cfg.AgentDockDir) == "" {
		return ""
	}
	version = strings.TrimSpace(version)
	if version != "" && filepath.Base(version) == version && !strings.Contains(version, "..") {
		candidate := s.agentDockPath("skill-store", "installed", skillID, version)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return s.findOpsSkillDocRoot(skillID)
}

func (s *Server) workflowCountsFromRuntime(ctx context.Context) map[string]int {
	counts := map[string]int{"drafts": 0, "published": 0, "retired": 0}
	body, err := s.runtimeGet(ctx, "/internal/runtime/workflows", nil)
	if err != nil {
		return counts
	}
	for _, raw := range opsArray(body["templates"]) {
		status := opsString(opsMap(raw)["status"])
		switch status {
		case "draft":
			counts["drafts"]++
		case "active", "validated":
			counts["published"]++
		case "retired":
			counts["retired"]++
		}
	}
	return counts
}

func cleanOpsTaskID(value string) (string, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".json")
	return cleanOpsName(value)
}

func urlPath(value string) string {
	return strings.ReplaceAll(strings.Trim(value, "/"), " ", "%20")
}

func firstOpsError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func readOpsTask(path string) (map[string]any, opsTaskSummary, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, opsTaskSummary{}, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, opsTaskSummary{}, err
	}
	summary := opsTaskSummaryFromMap(body)
	summary.FileName = filepath.Base(path)
	return body, summary, nil
}

func (s *Server) opsSkillStateDir() string {
	return s.agentDockPath("nexus/skills/state")
}

func (s *Server) opsSkillDocRoots() []string {
	roots := []string{
		s.agentDockPath("skill-sources"),
		filepath.Join(strings.TrimSpace(s.cfg.WorkspaceDir), "skills"),
		s.agentDockPath("skills"),
		filepath.Join(strings.TrimSpace(s.cfg.WorkspaceDir), ".agents/skills"),
		s.agentDockPath(".agents/skills"),
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		result = append(result, root)
	}
	return result
}

func (s *Server) collectOpsSkillStates() map[string]opsSkillStateRecord {
	root := s.opsSkillStateDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	items := map[string]opsSkillStateRecord{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if _, err := cleanOpsName(id); err != nil {
			continue
		}
		path := filepath.Join(root, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state opsSkillRuntimeState
		if err := json.Unmarshal(raw, &state); err != nil {
			continue
		}
		var rawState map[string]any
		_ = json.Unmarshal(raw, &rawState)
		info, _ := entry.Info()
		items[id] = opsSkillStateRecord{ID: id, Path: path, ModTime: modTime(info), Raw: rawState, State: state}
	}
	return items
}

func (s *Server) collectOpsSkills() []opsSkillSummary {
	items, err := s.collectOpsSkillsFromRuntime(context.Background())
	if err != nil {
		return nil
	}
	return items
}

func (s *Server) opsSkillSummaryFromState(skillID string, record opsSkillStateRecord) opsSkillSummary {
	docRoot := s.findOpsSkillDocRoot(skillID)
	title, desc, updatedAt, fileCount := "", "", firstNonEmptyString(record.State.UpdatedAt, record.ModTime), 0
	if docRoot != "" {
		info, _ := os.Stat(docRoot)
		title, desc = readSkillDoc(filepath.Join(docRoot, "SKILL.md"))
		fileCount = countFiles(docRoot, 4)
		updatedAt = firstNonEmptyString(record.State.UpdatedAt, modTime(info), record.ModTime)
	}
	return opsSkillSummary{
		ID:               skillID,
		Title:            firstNonEmptyString(title, skillID),
		Description:      desc,
		Source:           "runtime",
		Path:             filepath.ToSlash(filepath.Join("runtime", skillID)),
		UpdatedAt:        updatedAt,
		FileCount:        fileCount,
		Status:           "installed",
		ActiveVersion:    record.State.ActiveVersion,
		Versions:         opsSkillVersions(record.State),
		Channels:         record.State.Channels,
		RuntimeStatePath: filepath.ToSlash(record.Path),
		DocRoot:          filepath.ToSlash(docRoot),
	}
}

func (s *Server) collectOpsSkillDirectoryFallback() []opsSkillSummary {
	items := []opsSkillSummary{}
	seen := map[string]bool{}
	for _, root := range s.opsSkillDocRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		label := s.opsSkillRootLabel(root)
		for _, entry := range entries {
			if !entry.IsDir() || seen[entry.Name()] {
				continue
			}
			seen[entry.Name()] = true
			path := filepath.Join(root, entry.Name())
			info, _ := entry.Info()
			title, desc := readSkillDoc(filepath.Join(path, "SKILL.md"))
			items = append(items, opsSkillSummary{ID: entry.Name(), Title: firstNonEmptyString(title, entry.Name()), Description: desc, Source: label, Path: filepath.ToSlash(filepath.Join(label, entry.Name())), UpdatedAt: modTime(info), FileCount: countFiles(path, 4), Status: "source-only", DocRoot: filepath.ToSlash(path)})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (s *Server) runtimeSkillDetailModel(source, skillID string) (opsSkillDetail, error) {
	states := s.collectOpsSkillStates()
	record, hasState := states[skillID]
	root := s.findOpsSkillDocRoot(skillID)
	if !hasState && root == "" {
		return opsSkillDetail{}, fmt.Errorf("skill %s not found", skillID)
	}
	if source != "" && source != "runtime" && root != "" && s.opsSkillRootLabel(filepath.Dir(root)) != source {
		// Keep old links usable, but reject clearly unrelated source labels when no runtime state exists.
		if !hasState {
			return opsSkillDetail{}, fmt.Errorf("skill %s not found in source %s", skillID, source)
		}
	}
	summary := opsSkillSummary{ID: skillID, Title: skillID, Source: firstNonEmptyString(source, "runtime"), Path: filepath.ToSlash(filepath.Join(firstNonEmptyString(source, "runtime"), skillID)), Status: "installed"}
	if hasState {
		summary = s.opsSkillSummaryFromState(skillID, record)
	} else if root != "" {
		info, _ := os.Stat(root)
		title, desc := readSkillDoc(filepath.Join(root, "SKILL.md"))
		summary = opsSkillSummary{ID: skillID, Title: firstNonEmptyString(title, skillID), Description: desc, Source: s.opsSkillRootLabel(filepath.Dir(root)), Path: filepath.ToSlash(filepath.Join(s.opsSkillRootLabel(filepath.Dir(root)), skillID)), UpdatedAt: modTime(info), FileCount: countFiles(root, 4), Status: "source-only", DocRoot: filepath.ToSlash(root)}
	}
	detail := opsSkillDetail{opsSkillSummary: summary, Root: filepath.ToSlash(root), RuntimeState: record.Raw}
	if root != "" {
		detail.SkillDoc = readSmallText(filepath.Join(root, "SKILL.md"), 32000)
		detail.Files = collectOpsSkillFiles(root, 4, 160)
	} else {
		detail.Files = []opsSkillFile{}
	}
	return detail, nil
}

func (s *Server) findOpsSkillDocRoot(skillID string) string {
	for _, root := range s.opsSkillDocRoots() {
		candidate := filepath.Join(root, skillID)
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func (s *Server) opsSkillRootLabel(root string) string {
	clean := filepath.Clean(root)
	switch clean {
	case filepath.Clean(s.agentDockPath("skill-sources")):
		return "source"
	case filepath.Clean(filepath.Join(strings.TrimSpace(s.cfg.WorkspaceDir), "skills")):
		return "workspace"
	case filepath.Clean(s.agentDockPath("skills")):
		return "legacy"
	case filepath.Clean(filepath.Join(strings.TrimSpace(s.cfg.WorkspaceDir), ".agents/skills")), filepath.Clean(s.agentDockPath(".agents/skills")):
		return "agents"
	default:
		return "source"
	}
}

func opsSkillVersions(state opsSkillRuntimeState) []string {
	seen := map[string]bool{}
	versions := []string{}
	for i := len(state.History) - 1; i >= 0; i-- {
		version := strings.TrimSpace(state.History[i])
		if version != "" && !seen[version] {
			seen[version] = true
			versions = append(versions, version)
		}
	}
	if version := strings.TrimSpace(state.ActiveVersion); version != "" && !seen[version] {
		versions = append(versions, version)
	}
	return versions
}

func (s *Server) workflowCounts() map[string]int {
	return s.workflowCountsFromRuntime(context.Background())
}

func (s *Server) opsPaths() map[string]string {
	return map[string]string{"agentdock": s.cfg.AgentDockDir, "workspace": s.cfg.WorkspaceDir}
}

func (s *Server) agentDockPath(parts ...string) string {
	return filepath.Join(append([]string{strings.TrimSpace(s.cfg.AgentDockDir)}, parts...)...)
}

func cleanOpsFileName(value, suffix string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value != filepath.Base(value) || strings.Contains(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "..") {
		return "", fmt.Errorf("invalid file name")
	}
	if suffix != "" && !strings.HasSuffix(value, suffix) {
		return "", fmt.Errorf("file name must end with %s", suffix)
	}
	return value, nil
}

func cleanOpsName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value != filepath.Base(value) || strings.Contains(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "..") {
		return "", fmt.Errorf("invalid name")
	}
	return value, nil
}

func collectOpsSkillFiles(root string, maxDepth, maxItems int) []opsSkillFile {
	root = filepath.Clean(root)
	baseDepth := strings.Count(root, string(os.PathSeparator))
	items := []opsSkillFile{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		depth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - baseDepth
		if d.IsDir() && depth > maxDepth {
			return fs.SkipDir
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel := trimKnownRoot(path, root)
		if isPrivateSkillFilePath(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		items = append(items, opsSkillFile{Path: rel, Kind: skillFileKind(rel), SizeBytes: info.Size(), UpdatedAt: modTime(info)})
		if len(items) >= maxItems {
			return fs.SkipAll
		}
		return nil
	})
	sort.SliceStable(items, func(i, j int) bool {
		left, right := skillFileKindRank(items[i]), skillFileKindRank(items[j])
		if left != right {
			return left < right
		}
		return items[i].Path < items[j].Path
	})
	return items
}

func isPrivateSkillFilePath(path string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		name := strings.ToLower(strings.TrimSpace(segment))
		if name == "" || strings.HasPrefix(name, ".") || name == "_meta.json" {
			return true
		}
	}
	return false
}

func skillFileKind(path string) string {
	name := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case name == "skill.md" || name == "readme.md":
		return "doc"
	case name == "manifest.json" || name == "package.json" || name == "skill.json":
		return "manifest"
	case ext == ".go" || ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".py" || ext == ".sh":
		return "code"
	case ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".toml":
		return "config"
	default:
		return "asset"
	}
}

func skillFileKindRank(file opsSkillFile) int {
	if strings.EqualFold(file.Path, "SKILL.md") {
		return 0
	}
	switch file.Kind {
	case "doc":
		return 1
	case "code":
		return 2
	case "config", "manifest":
		return 3
	default:
		return 4
	}
}

func firstSkills(items []opsSkillSummary, n int) []opsSkillSummary {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func opsInt(v any) int {
	switch typed := v.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

func opsStringArray(v any) []string {
	items := []string{}
	switch typed := v.(type) {
	case []string:
		return append(items, typed...)
	case []any:
		for _, item := range typed {
			if s := opsString(item); s != "" {
				items = append(items, s)
			}
		}
	}
	return items
}

func opsStringMap(v any) map[string]string {
	out := map[string]string{}
	switch typed := v.(type) {
	case map[string]string:
		for key, value := range typed {
			out[key] = value
		}
	case map[string]any:
		for key, value := range typed {
			if s := opsString(value); s != "" {
				out[key] = s
			}
		}
	}
	return out
}

func opsString(v any) string { s, _ := v.(string); return s }
func opsMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func opsArray(v any) []any { a, _ := v.([]any); return a }
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

func writeJSONFile(path string, body map[string]any) error {
	content, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o644)
}

func readSkillDoc(path string) (string, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(raw), "\n")
	title, desc := "", ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if title == "" && strings.HasPrefix(line, "#") {
			title = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		}
		if title != "" && desc == "" && line != "" && !strings.HasPrefix(line, "#") {
			desc = line
			break
		}
	}
	return title, desc
}

func countFiles(root string, maxDepth int) int {
	root = filepath.Clean(root)
	baseDepth := strings.Count(root, string(os.PathSeparator))
	count := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.Count(filepath.Clean(path), string(os.PathSeparator))-baseDepth > maxDepth {
			return fs.SkipDir
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}

func readSmallText(path string, limit int) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	if len(raw) > limit {
		raw = raw[:limit]
	}
	return string(raw)
}

func trimKnownRoot(path, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}
