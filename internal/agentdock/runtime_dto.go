package agentdock

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件定义 AgentDock Runtime API 在 Nexus 边界内的 DTO 与解析逻辑。
// 字段形状以 AgentDock internal/runtimeapi 的真实响应为准：
//   - 任务：internal/tool/task（compactTaskListItem 与完整 taskstate.Task）
//   - Skill：internal/tool/skill（list/inspect/RuntimeSkillFile）
//   - 动态 MCP：internal/mcp/client（ServerSummary/ServerConfig）与 envstore.Entry
//
// 只建模 Nexus 真正消费的字段；上游新增字段会被忽略，删除/改名/类型漂移则通过 ContractError 暴露。

// 任务与步骤的合法状态枚举，与 AgentDock internal/taskstate 的常量保持一致。
var (
	runtimeTaskStatuses   = []string{"active", "blocked", "completed"}
	runtimeReviewStatuses = []string{"not_started", "pass", "failed"}
	runtimeStepStatuses   = []string{"pending", "in_progress", "completed"}
)

// RuntimeTaskStep 是任务的执行步骤。列表项里的 current_step 只携带 id/title/status，
// 任务详情中的完整步骤会额外带 phase/updated_at，缺失时保持零值。
type RuntimeTaskStep struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Phase     string `json:"phase,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// RuntimeTaskSummary 是任务列表项与任务详情共用的摘要视图。
// 列表项由上游直接给出计数；详情的计数由解析器按步骤/事件数组推导。
type RuntimeTaskSummary struct {
	ID                 string           `json:"id"`
	Title              string           `json:"title"`
	Goal               string           `json:"goal"`
	Status             string           `json:"status"`
	Phase              string           `json:"phase"`
	ReviewStatus       string           `json:"review_status"`
	Summary            string           `json:"summary,omitempty"`
	Blocker            string           `json:"blocker,omitempty"`
	CurrentStep        *RuntimeTaskStep `json:"current_step,omitempty"`
	CompletedStepCount int              `json:"completed_step_count"`
	StepCount          int              `json:"step_count"`
	UpdatedAt          string           `json:"updated_at"`
	CreatedAt          string           `json:"created_at,omitempty"`
	TemplateID         string           `json:"template_id,omitempty"`
	TemplateVersion    string           `json:"template_version,omitempty"`
	ConditionCount     int              `json:"condition_count"`
	EventCount         int              `json:"event_count"`
}

type RuntimeTaskCondition struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at,omitempty"`
}

type RuntimeTaskEvent struct {
	Type      string `json:"type,omitempty"`
	Summary   string `json:"summary,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// RuntimeTaskFinalReview 对应任务详情里的 final_review；Stage 3 进化依赖 review_revision 与事实清单。
type RuntimeTaskFinalReview struct {
	Status         string   `json:"status"`
	Summary        string   `json:"summary,omitempty"`
	VerifiedFacts  []string `json:"verified_facts,omitempty"`
	OpenRisks      []string `json:"open_risks,omitempty"`
	MissingChecks  []string `json:"missing_checks,omitempty"`
	ReviewRevision string   `json:"review_revision"`
	ReviewedAt     string   `json:"reviewed_at,omitempty"`
}

type RuntimeTaskDetail struct {
	Summary     RuntimeTaskSummary
	Conditions  []RuntimeTaskCondition
	Steps       []RuntimeTaskStep
	Events      []RuntimeTaskEvent
	FinalReview *RuntimeTaskFinalReview
}

// RuntimeSkillSummary 是当前 managed Skill 内容的列表项。
// skill_ref 与来源字段直接来自 AgentDock，Nexus 不再维护独立版本或激活态。
type RuntimeSkillSummary struct {
	Skill         string `json:"skill"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	SkillRef      string `json:"skill_ref"`
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id"`
	PluginName    string `json:"plugin_name,omitempty"`
	ContentDigest string `json:"content_digest"`
	FileCount     int    `json:"file_count"`
}

type RuntimeSkillFile struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
}

type RuntimeSkillDetail struct {
	Skill         string             `json:"skill"`
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	SkillRef      string             `json:"skill_ref"`
	SourceType    string             `json:"source_type"`
	SourceID      string             `json:"source_id"`
	PluginName    string             `json:"plugin_name,omitempty"`
	ContentDigest string             `json:"content_digest"`
	Files         []RuntimeSkillFile `json:"files"`
}

// RuntimeSkillFileContent 是 Skill 包内单个文本文件的读取结果。
type RuntimeSkillFileContent struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// RuntimeMCPServerSummary 对应 AgentDock 动态 MCP 的 ServerSummary，字段名与上游 JSON 一致，
// 因此 UI API 可以直接序列化该类型。
type RuntimeMCPServerSummary struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Transport     string `json:"transport"`
	Enabled       bool   `json:"enabled"`
	Status        string `json:"status"`
	ToolCount     int    `json:"tool_count"`
	LastError     string `json:"last_error,omitempty"`
	LastErrorCode string `json:"last_error_code,omitempty"`
	RefreshedAt   string `json:"refreshed_at,omitempty"`
}

// RuntimeMCPServerConfig 对应上游 ServerConfig；header_env/env_from_env 只含变量名映射，
// 不含变量值，可以安全透传给 UI。
type RuntimeMCPServerConfig struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Transport   string            `json:"transport"`
	URL         string            `json:"url,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	HeaderEnv   map[string]string `json:"header_env,omitempty"`
	EnvFromEnv  map[string]string `json:"env_from_env,omitempty"`
	Enabled     bool              `json:"enabled"`
	TimeoutMS   int               `json:"timeout_ms,omitempty"`
}

type RuntimeMCPServerDetail struct {
	Server RuntimeMCPServerSummary
	Config RuntimeMCPServerConfig
}

// RuntimeMCPEnvEntry 是 MCP 隔离环境变量的元数据；Configured 只表示是否配置过值，不含值本身。
type RuntimeMCPEnvEntry struct {
	Key        string `json:"key"`
	Configured bool   `json:"configured"`
}

// RuntimeTaskDeleteResult 是删除任务的确认结果。
type RuntimeTaskDeleteResult struct {
	TaskID string
	// DeletedTask 是上游删除确认的回显，UI 只依赖成功状态；保留原始 JSON 透传，
	// 避免为一条不再被解读的回显维护完整任务 DTO。
	DeletedTask json.RawMessage
}

// parseRuntimeTaskList 解析 GET /internal/runtime/tasks 响应。
func parseRuntimeTaskList(node string, payload map[string]any) ([]RuntimeTaskSummary, error) {
	p := runtimeParser{node: node, operation: "GET /internal/runtime/tasks"}
	raw, err := p.array("tasks", payload["tasks"], false)
	if err != nil {
		return nil, err
	}
	tasks := make([]RuntimeTaskSummary, 0, len(raw))
	for i, item := range raw {
		field := fieldIndex("tasks", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return nil, err
		}
		summary, err := p.parseTaskListItem(field, object)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, summary)
	}
	return tasks, nil
}

// parseTaskListItem 解析单个列表项。id 兼容旧版 AgentDock 的 task_id 字段名。
func (p runtimeParser) parseTaskListItem(field string, task map[string]any) (RuntimeTaskSummary, error) {
	id, err := p.taskID(field+".id", task)
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	status, err := p.requiredEnum(field+".status", task["status"], runtimeTaskStatuses)
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	reviewStatus, err := p.requiredEnum(field+".review_status", task["review_status"], runtimeReviewStatuses)
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	updatedAt, err := p.requiredString(field+".updated_at", task["updated_at"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	completedSteps, err := p.optionalInt(field+".completed_step_count", task["completed_step_count"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	stepCount, err := p.optionalInt(field+".step_count", task["step_count"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	eventCount, err := p.optionalInt(field+".event_count", task["event_count"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	summary, err := p.optionalString(field+".summary", task["summary"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	blocker, err := p.optionalString(field+".blocker", task["blocker"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	currentStep, err := p.parseOptionalTaskStep(field+".current_step", task["current_step"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	title, err := p.optionalString(field+".title", task["title"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	goal, err := p.optionalString(field+".goal", task["goal"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	phase, err := p.optionalString(field+".phase", task["phase"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	createdAt, err := p.optionalString(field+".created_at", task["created_at"])
	if err != nil {
		return RuntimeTaskSummary{}, err
	}
	return RuntimeTaskSummary{
		ID: id, Title: title, Goal: goal, Status: status, Phase: phase, ReviewStatus: reviewStatus,
		Summary: summary, Blocker: blocker, CurrentStep: currentStep,
		CompletedStepCount: completedSteps, StepCount: stepCount,
		UpdatedAt: updatedAt, CreatedAt: createdAt, EventCount: eventCount,
	}, nil
}

// taskID 读取任务 ID；task_id 是旧版 AgentDock 列表项的字段名，保持兼容。
func (p runtimeParser) taskID(field string, task map[string]any) (string, error) {
	if value, ok := task["id"]; ok && value != nil {
		return p.requiredString(field, value)
	}
	if value, ok := task["task_id"]; ok && value != nil {
		return p.requiredString(field+".task_id", value)
	}
	return "", p.fail(field, "缺少必填字段")
}

// parseRuntimeTaskDetail 解析 GET /internal/runtime/tasks/{taskID} 响应。
// 完整任务对象不直接提供完成计数与当前步骤，这里按与上游 compactTaskSummary 相同的规则推导。
func parseRuntimeTaskDetail(node, taskID string, payload map[string]any) (RuntimeTaskDetail, error) {
	operation := "GET /internal/runtime/tasks/" + taskID
	p := runtimeParser{node: node, operation: operation}
	task, err := p.object("task", payload["task"], false)
	if err != nil {
		return RuntimeTaskDetail{}, err
	}

	stepsRaw, err := p.array("task.steps", task["steps"], true)
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	steps := make([]RuntimeTaskStep, 0, len(stepsRaw))
	for i, item := range stepsRaw {
		field := fieldIndex("task.steps", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return RuntimeTaskDetail{}, err
		}
		step, err := p.parseTaskStep(field, object)
		if err != nil {
			return RuntimeTaskDetail{}, err
		}
		steps = append(steps, step)
	}

	conditionsRaw, err := p.array("task.conditions", task["conditions"], true)
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	conditions := make([]RuntimeTaskCondition, 0, len(conditionsRaw))
	for i, item := range conditionsRaw {
		field := fieldIndex("task.conditions", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return RuntimeTaskDetail{}, err
		}
		condition, err := p.parseTaskCondition(field, object)
		if err != nil {
			return RuntimeTaskDetail{}, err
		}
		conditions = append(conditions, condition)
	}

	eventsRaw, err := p.array("task.events", task["events"], true)
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	events := make([]RuntimeTaskEvent, 0, len(eventsRaw))
	for i, item := range eventsRaw {
		field := fieldIndex("task.events", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return RuntimeTaskDetail{}, err
		}
		event, err := p.parseTaskEvent(field, object)
		if err != nil {
			return RuntimeTaskDetail{}, err
		}
		events = append(events, event)
	}

	finalReview, err := p.parseOptionalFinalReview("task.final_review", task["final_review"])
	if err != nil {
		return RuntimeTaskDetail{}, err
	}

	// 详情对象没有顶层 review_status 时回退到 final_review.status，再回退到 not_started，
	// 与旧解析链保持一致。
	reviewStatus, err := p.optionalEnum("task.review_status", task["review_status"], runtimeReviewStatuses)
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	if reviewStatus == "" && finalReview != nil {
		reviewStatus = finalReview.Status
	}
	if reviewStatus == "" {
		reviewStatus = "not_started"
	}

	id, err := p.requiredString("task.id", task["id"])
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	status, err := p.requiredEnum("task.status", task["status"], runtimeTaskStatuses)
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	updatedAt, err := p.requiredString("task.updated_at", task["updated_at"])
	if err != nil {
		return RuntimeTaskDetail{}, err
	}

	template, err := p.object("task.template", task["template"], true)
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	templateID := ""
	templateVersion := ""
	if template != nil {
		if templateID, err = p.optionalString("task.template.id", template["id"]); err != nil {
			return RuntimeTaskDetail{}, err
		}
		if templateVersion, err = p.optionalString("task.template.version", template["version"]); err != nil {
			return RuntimeTaskDetail{}, err
		}
	}

	title, err := p.optionalString("task.title", task["title"])
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	goal, err := p.optionalString("task.goal", task["goal"])
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	phase, err := p.optionalString("task.phase", task["phase"])
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	summaryText, err := p.optionalString("task.summary", task["summary"])
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	blocker, err := p.optionalString("task.blocker", task["blocker"])
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	createdAt, err := p.optionalString("task.created_at", task["created_at"])
	if err != nil {
		return RuntimeTaskDetail{}, err
	}

	// 当前步骤：优先第一个进行中的步骤，否则第一个待执行步骤，与上游摘要规则一致。
	completedSteps := 0
	var current, pending *RuntimeTaskStep
	for i := range steps {
		switch steps[i].Status {
		case "completed":
			completedSteps++
		case "in_progress":
			if current == nil {
				step := steps[i]
				current = &step
			}
		case "pending":
			if pending == nil {
				step := steps[i]
				pending = &step
			}
		}
	}
	if current == nil {
		current = pending
	}

	return RuntimeTaskDetail{
		Summary: RuntimeTaskSummary{
			ID: id, Title: title, Goal: goal, Status: status, Phase: phase, ReviewStatus: reviewStatus,
			Summary: summaryText, Blocker: blocker, CurrentStep: current,
			CompletedStepCount: completedSteps, StepCount: len(steps),
			UpdatedAt: updatedAt, CreatedAt: createdAt,
			TemplateID: templateID, TemplateVersion: templateVersion,
			ConditionCount: len(conditions), EventCount: len(events),
		},
		Conditions:  conditions,
		Steps:       steps,
		Events:      events,
		FinalReview: finalReview,
	}, nil
}

// parseTaskStep 解析完整步骤对象；id/status 是上游进度语义的必填字段。
func (p runtimeParser) parseTaskStep(field string, step map[string]any) (RuntimeTaskStep, error) {
	id, err := p.requiredString(field+".id", step["id"])
	if err != nil {
		return RuntimeTaskStep{}, err
	}
	status, err := p.requiredEnum(field+".status", step["status"], runtimeStepStatuses)
	if err != nil {
		return RuntimeTaskStep{}, err
	}
	title, err := p.optionalString(field+".title", step["title"])
	if err != nil {
		return RuntimeTaskStep{}, err
	}
	phase, err := p.optionalString(field+".phase", step["phase"])
	if err != nil {
		return RuntimeTaskStep{}, err
	}
	updatedAt, err := p.optionalString(field+".updated_at", step["updated_at"])
	if err != nil {
		return RuntimeTaskStep{}, err
	}
	return RuntimeTaskStep{ID: id, Title: title, Status: status, Phase: phase, UpdatedAt: updatedAt}, nil
}

// parseOptionalTaskStep 解析列表项里的 current_step（只含 id/title/status），缺失或为 null 时返回 nil。
func (p runtimeParser) parseOptionalTaskStep(field string, value any) (*RuntimeTaskStep, error) {
	step, err := p.object(field, value, true)
	if err != nil {
		return nil, err
	}
	if step == nil {
		return nil, nil
	}
	id, err := p.requiredString(field+".id", step["id"])
	if err != nil {
		return nil, err
	}
	status, err := p.requiredEnum(field+".status", step["status"], runtimeStepStatuses)
	if err != nil {
		return nil, err
	}
	title, err := p.optionalString(field+".title", step["title"])
	if err != nil {
		return nil, err
	}
	return &RuntimeTaskStep{ID: id, Title: title, Status: status}, nil
}

func (p runtimeParser) parseTaskCondition(field string, condition map[string]any) (RuntimeTaskCondition, error) {
	id, err := p.requiredString(field+".id", condition["id"])
	if err != nil {
		return RuntimeTaskCondition{}, err
	}
	text, err := p.optionalString(field+".text", condition["text"])
	if err != nil {
		return RuntimeTaskCondition{}, err
	}
	createdAt, err := p.optionalString(field+".created_at", condition["created_at"])
	if err != nil {
		return RuntimeTaskCondition{}, err
	}
	return RuntimeTaskCondition{ID: id, Text: text, CreatedAt: createdAt}, nil
}

func (p runtimeParser) parseTaskEvent(field string, event map[string]any) (RuntimeTaskEvent, error) {
	eventType, err := p.optionalString(field+".type", event["type"])
	if err != nil {
		return RuntimeTaskEvent{}, err
	}
	summary, err := p.optionalString(field+".summary", event["summary"])
	if err != nil {
		return RuntimeTaskEvent{}, err
	}
	createdAt, err := p.optionalString(field+".created_at", event["created_at"])
	if err != nil {
		return RuntimeTaskEvent{}, err
	}
	return RuntimeTaskEvent{Type: eventType, Summary: summary, CreatedAt: createdAt}, nil
}

func (p runtimeParser) parseOptionalFinalReview(field string, value any) (*RuntimeTaskFinalReview, error) {
	review, err := p.object(field, value, true)
	if err != nil {
		return nil, err
	}
	if review == nil {
		return nil, nil
	}
	status, err := p.requiredEnum(field+".status", review["status"], []string{"pass", "failed"})
	if err != nil {
		return nil, err
	}
	summary, err := p.optionalString(field+".summary", review["summary"])
	if err != nil {
		return nil, err
	}
	verifiedFacts, err := p.optionalStringArray(field+".verified_facts", review["verified_facts"])
	if err != nil {
		return nil, err
	}
	openRisks, err := p.optionalStringArray(field+".open_risks", review["open_risks"])
	if err != nil {
		return nil, err
	}
	missingChecks, err := p.optionalStringArray(field+".missing_checks", review["missing_checks"])
	if err != nil {
		return nil, err
	}
	reviewRevision, err := p.optionalString(field+".review_revision", review["review_revision"])
	if err != nil {
		return nil, err
	}
	reviewedAt, err := p.optionalString(field+".reviewed_at", review["reviewed_at"])
	if err != nil {
		return nil, err
	}
	return &RuntimeTaskFinalReview{
		Status: status, Summary: summary, VerifiedFacts: verifiedFacts, OpenRisks: openRisks,
		MissingChecks: missingChecks, ReviewRevision: reviewRevision, ReviewedAt: reviewedAt,
	}, nil
}

// parseRuntimeSkillList 解析 GET /internal/runtime/skills 响应。
// AgentDock 当前内容模型要求每个列表项都携带精确 skill_ref 与来源身份。
func parseRuntimeSkillList(node string, payload map[string]any) ([]RuntimeSkillSummary, error) {
	p := runtimeParser{node: node, operation: "GET /internal/runtime/skills"}
	raw, err := p.array("skills", payload["skills"], false)
	if err != nil {
		return nil, err
	}
	skills := make([]RuntimeSkillSummary, 0, len(raw))
	for i, item := range raw {
		field := fieldIndex("skills", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return nil, err
		}
		skill, err := p.requiredString(field+".skill", object["skill"])
		if err != nil {
			return nil, err
		}
		name, err := p.requiredString(field+".name", object["name"])
		if err != nil {
			return nil, err
		}
		description, err := p.requiredString(field+".description", object["description"])
		if err != nil {
			return nil, err
		}
		skillRef, err := p.requiredString(field+".skill_ref", object["skill_ref"])
		if err != nil {
			return nil, err
		}
		sourceType, err := p.requiredString(field+".source_type", object["source_type"])
		if err != nil {
			return nil, err
		}
		sourceID, err := p.requiredString(field+".source_id", object["source_id"])
		if err != nil {
			return nil, err
		}
		pluginName, err := p.optionalString(field+".plugin_name", object["plugin_name"])
		if err != nil {
			return nil, err
		}
		switch sourceType {
		case "managed":
			if pluginName != "" {
				return nil, p.fail(field+".plugin_name", "managed Skill 不应携带 Plugin provenance")
			}
		case "plugin":
			if strings.TrimSpace(pluginName) == "" {
				return nil, p.fail(field+".plugin_name", "Plugin Skill 必须携带 owning Plugin name")
			}
		default:
			return nil, p.fail(field+".source_type", fmt.Sprintf("不支持的 Skill 来源类型 %q", sourceType))
		}
		contentDigest, err := p.requiredString(field+".content_digest", object["content_digest"])
		if err != nil {
			return nil, err
		}
		fileCount, err := p.optionalInt(field+".file_count", object["file_count"])
		if err != nil {
			return nil, err
		}
		skills = append(skills, RuntimeSkillSummary{
			Skill: skill, Name: name, Description: description,
			SkillRef: skillRef, SourceType: sourceType, SourceID: sourceID, PluginName: pluginName,
			ContentDigest: contentDigest, FileCount: fileCount,
		})
	}
	return skills, nil
}

// parseRuntimeSkillDetail 解析 GET /internal/runtime/skills/{skillID} 响应。
// 详情只描述当前 managed Skill 内容，不再解析版本、激活态或渠道历史。
func parseRuntimeSkillDetail(node, skillID string, payload map[string]any) (RuntimeSkillDetail, error) {
	operation := "GET /internal/runtime/skills/" + skillID
	p := runtimeParser{node: node, operation: operation}

	skill, err := p.requiredString("skill", payload["skill"])
	if err != nil {
		return RuntimeSkillDetail{}, err
	}
	if skill != skillID {
		return RuntimeSkillDetail{}, p.fail("skill", fmt.Sprintf("与请求 Skill %q 不一致", skillID))
	}
	name, err := p.requiredString("name", payload["name"])
	if err != nil {
		return RuntimeSkillDetail{}, err
	}
	description, err := p.requiredString("description", payload["description"])
	if err != nil {
		return RuntimeSkillDetail{}, err
	}
	skillRef, err := p.requiredString("skill_ref", payload["skill_ref"])
	if err != nil {
		return RuntimeSkillDetail{}, err
	}
	sourceType, err := p.requiredString("source_type", payload["source_type"])
	if err != nil {
		return RuntimeSkillDetail{}, err
	}
	sourceID, err := p.requiredString("source_id", payload["source_id"])
	if err != nil {
		return RuntimeSkillDetail{}, err
	}
	pluginName, err := p.optionalString("plugin_name", payload["plugin_name"])
	if err != nil {
		return RuntimeSkillDetail{}, err
	}
	switch sourceType {
	case "managed":
		if pluginName != "" {
			return RuntimeSkillDetail{}, p.fail("plugin_name", "managed Skill 不应携带 Plugin provenance")
		}
	case "plugin":
		if strings.TrimSpace(pluginName) == "" {
			return RuntimeSkillDetail{}, p.fail("plugin_name", "Plugin Skill 必须携带 owning Plugin name")
		}
	default:
		return RuntimeSkillDetail{}, p.fail("source_type", fmt.Sprintf("不支持的 Skill 来源类型 %q", sourceType))
	}
	contentDigest, err := p.requiredString("content_digest", payload["content_digest"])
	if err != nil {
		return RuntimeSkillDetail{}, err
	}
	if _, err := p.object("document", payload["document"], false); err != nil {
		return RuntimeSkillDetail{}, err
	}

	filesRaw, err := p.array("files", payload["files"], false)
	if err != nil {
		return RuntimeSkillDetail{}, err
	}
	files := make([]RuntimeSkillFile, 0, len(filesRaw))
	for i, item := range filesRaw {
		field := fieldIndex("files", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return RuntimeSkillDetail{}, err
		}
		path, err := p.requiredString(field+".path", object["path"])
		if err != nil {
			return RuntimeSkillDetail{}, err
		}
		kind, err := p.requiredString(field+".kind", object["kind"])
		if err != nil {
			return RuntimeSkillDetail{}, err
		}
		sizeBytes, err := p.optionalInt64(field+".size_bytes", object["size_bytes"])
		if err != nil {
			return RuntimeSkillDetail{}, err
		}
		updatedAt, err := p.requiredString(field+".updated_at", object["updated_at"])
		if err != nil {
			return RuntimeSkillDetail{}, err
		}
		files = append(files, RuntimeSkillFile{Path: path, Kind: kind, SizeBytes: sizeBytes, UpdatedAt: updatedAt})
	}

	return RuntimeSkillDetail{
		Skill: skill, Name: name, Description: description,
		SkillRef: skillRef, SourceType: sourceType, SourceID: sourceID, PluginName: pluginName,
		ContentDigest: contentDigest, Files: files,
	}, nil
}

// parseRuntimeSkillFile 解析 GET /internal/runtime/skills/{skillID}/files/{path} 响应。
func parseRuntimeSkillFile(node string, payload map[string]any) (RuntimeSkillFileContent, error) {
	p := runtimeParser{node: node, operation: "GET /internal/runtime/skills/.../files"}
	file, err := p.object("file", payload["file"], false)
	if err != nil {
		return RuntimeSkillFileContent{}, err
	}
	path, err := p.requiredString("file.path", file["path"])
	if err != nil {
		return RuntimeSkillFileContent{}, err
	}
	kind, err := p.requiredString("file.kind", file["kind"])
	if err != nil {
		return RuntimeSkillFileContent{}, err
	}
	sizeBytes, err := p.optionalInt64("file.size_bytes", file["size_bytes"])
	if err != nil {
		return RuntimeSkillFileContent{}, err
	}
	updatedAt, err := p.requiredString("file.updated_at", file["updated_at"])
	if err != nil {
		return RuntimeSkillFileContent{}, err
	}
	// 文件内容可能为空串（空文件），但字段本身必须存在且是字符串。
	content, err := p.presentString("file.content", file["content"])
	if err != nil {
		return RuntimeSkillFileContent{}, err
	}
	truncated, err := p.optionalBool("file.truncated", file["truncated"])
	if err != nil {
		return RuntimeSkillFileContent{}, err
	}
	return RuntimeSkillFileContent{
		Path: path, Kind: kind, SizeBytes: sizeBytes, UpdatedAt: updatedAt, Content: content, Truncated: truncated,
	}, nil
}

// parseRuntimeMCPServerSummary 解析动态 MCP 服务摘要对象。
func (p runtimeParser) parseRuntimeMCPServerSummary(field string, server map[string]any) (RuntimeMCPServerSummary, error) {
	name, err := p.requiredString(field+".name", server["name"])
	if err != nil {
		return RuntimeMCPServerSummary{}, err
	}
	transport, err := p.requiredString(field+".transport", server["transport"])
	if err != nil {
		return RuntimeMCPServerSummary{}, err
	}
	status, err := p.requiredString(field+".status", server["status"])
	if err != nil {
		return RuntimeMCPServerSummary{}, err
	}
	enabled, err := p.optionalBool(field+".enabled", server["enabled"])
	if err != nil {
		return RuntimeMCPServerSummary{}, err
	}
	toolCount, err := p.optionalInt(field+".tool_count", server["tool_count"])
	if err != nil {
		return RuntimeMCPServerSummary{}, err
	}
	description, err := p.optionalString(field+".description", server["description"])
	if err != nil {
		return RuntimeMCPServerSummary{}, err
	}
	lastError, err := p.optionalString(field+".last_error", server["last_error"])
	if err != nil {
		return RuntimeMCPServerSummary{}, err
	}
	lastErrorCode, err := p.optionalString(field+".last_error_code", server["last_error_code"])
	if err != nil {
		return RuntimeMCPServerSummary{}, err
	}
	refreshedAt, err := p.optionalString(field+".refreshed_at", server["refreshed_at"])
	if err != nil {
		return RuntimeMCPServerSummary{}, err
	}
	return RuntimeMCPServerSummary{
		Name: name, Description: description, Transport: transport, Enabled: enabled, Status: status,
		ToolCount: toolCount, LastError: lastError, LastErrorCode: lastErrorCode, RefreshedAt: refreshedAt,
	}, nil
}

// parseRuntimeMCPServers 解析 GET /internal/runtime/mcp 响应（action=list）。
func parseRuntimeMCPServers(node string, payload map[string]any) ([]RuntimeMCPServerSummary, error) {
	p := runtimeParser{node: node, operation: "GET /internal/runtime/mcp"}
	raw, err := p.array("servers", payload["servers"], false)
	if err != nil {
		return nil, err
	}
	servers := make([]RuntimeMCPServerSummary, 0, len(raw))
	for i, item := range raw {
		field := fieldIndex("servers", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return nil, err
		}
		server, err := p.parseRuntimeMCPServerSummary(field, object)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, nil
}

// parseRuntimeMCPServerDetail 解析 GET /internal/runtime/mcp/{name} 响应（action=inspect）。
func parseRuntimeMCPServerDetail(node, name string, payload map[string]any) (RuntimeMCPServerDetail, error) {
	operation := "GET /internal/runtime/mcp/" + name
	p := runtimeParser{node: node, operation: operation}

	serverObject, err := p.object("server", payload["server"], false)
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	server, err := p.parseRuntimeMCPServerSummary("server", serverObject)
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}

	configObject, err := p.object("config", payload["config"], false)
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	configName, err := p.requiredString("config.name", configObject["name"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	configTransport, err := p.requiredString("config.transport", configObject["transport"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	enabled, err := p.optionalBool("config.enabled", configObject["enabled"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	description, err := p.optionalString("config.description", configObject["description"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	url, err := p.optionalString("config.url", configObject["url"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	command, err := p.optionalString("config.command", configObject["command"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	args, err := p.optionalStringArray("config.args", configObject["args"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	cwd, err := p.optionalString("config.cwd", configObject["cwd"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	headerEnv, err := p.optionalStringMap("config.header_env", configObject["header_env"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	envFromEnv, err := p.optionalStringMap("config.env_from_env", configObject["env_from_env"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	timeoutMS, err := p.optionalInt("config.timeout_ms", configObject["timeout_ms"])
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}

	return RuntimeMCPServerDetail{
		Server: server,
		Config: RuntimeMCPServerConfig{
			Name: configName, Description: description, Transport: configTransport, URL: url, Command: command,
			Args: args, Cwd: cwd, HeaderEnv: headerEnv, EnvFromEnv: envFromEnv, Enabled: enabled, TimeoutMS: timeoutMS,
		},
	}, nil
}

// parseRuntimeMCPEnvironment 解析 MCP env_list 响应中的变量条目。
func parseRuntimeMCPEnvironment(node, name string, payload map[string]any) ([]RuntimeMCPEnvEntry, error) {
	p := runtimeParser{node: node, operation: "POST /internal/runtime/mcp env_list " + name}
	raw, err := p.array("items", payload["items"], false)
	if err != nil {
		return nil, err
	}
	items := make([]RuntimeMCPEnvEntry, 0, len(raw))
	for i, item := range raw {
		field := fieldIndex("items", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return nil, err
		}
		key, err := p.requiredString(field+".key", object["key"])
		if err != nil {
			return nil, err
		}
		configured, err := p.optionalBool(field+".configured", object["configured"])
		if err != nil {
			return nil, err
		}
		items = append(items, RuntimeMCPEnvEntry{Key: key, Configured: configured})
	}
	return items, nil
}

// parseRuntimeTaskDeleteResult 解析 DELETE /internal/runtime/tasks/{taskID} 响应。
func parseRuntimeTaskDeleteResult(node, taskID string, payload map[string]any) (RuntimeTaskDeleteResult, error) {
	p := runtimeParser{node: node, operation: "DELETE /internal/runtime/tasks/" + taskID}
	deletedTaskID, err := p.requiredString("task_id", payload["task_id"])
	if err != nil {
		return RuntimeTaskDeleteResult{}, err
	}
	deletedTask := json.RawMessage(nil)
	if raw, ok := payload["deleted_task"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return RuntimeTaskDeleteResult{}, p.fail("deleted_task", fmt.Sprintf("无法编码删除回显: %v", err))
		}
		deletedTask = encoded
	}
	return RuntimeTaskDeleteResult{TaskID: deletedTaskID, DeletedTask: deletedTask}, nil
}
