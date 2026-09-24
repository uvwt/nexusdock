package agentdock

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAgentDockRuntimeRequestTimeoutIsEightSeconds(t *testing.T) {
	if runtimeRequestTimeout != 8*time.Second {
		t.Fatalf("runtime request timeout = %s", runtimeRequestTimeout)
	}
}

// fixturePayload 把贴近 AgentDock 上游真实形状的 JSON 字符串解码成解析函数的输入。
func fixturePayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("解码 fixture: %v", err)
	}
	return payload
}

func assertContractError(t *testing.T, err error, wantOperation, wantField string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望契约错误，实际成功")
	}
	var contractErr *ContractError
	if !asContractError(err, &contractErr) {
		t.Fatalf("期望 *ContractError，实际 %T: %v", err, err)
	}
	if contractErr.Operation != wantOperation {
		t.Fatalf("上游方法 = %q, want %q（错误: %v）", contractErr.Operation, wantOperation, err)
	}
	if contractErr.Field != wantField {
		t.Fatalf("字段路径 = %q, want %q（错误: %v）", contractErr.Field, wantField, err)
	}
}

func asContractError(err error, target **ContractError) bool {
	if contractErr, ok := err.(*ContractError); ok {
		*target = contractErr
		return true
	}
	return false
}

const currentTaskListFixture = `{
  "ok": true, "source": "agentdock-api", "action": "list", "count": 2,
  "tasks": [
    {
      "id": "task_01", "title": "修复登录超时", "goal": "登录不再超时",
      "status": "active", "phase": "execute", "review_status": "not_started",
      "completed_step_count": 1, "step_count": 3,
      "current_step": {"id": "s2", "title": "复现问题", "status": "in_progress"},
      "summary": "正在复现", "blocker": "", "updated_at": "2026-09-13T08:00:00Z",
      "created_at": "2026-09-13T07:00:00Z", "event_count": 4
    },
    {
      "id": "task_02", "title": "发布 1.2", "goal": "发布新版本",
      "status": "completed", "phase": "closeout", "review_status": "pass",
      "completed_step_count": 2, "step_count": 2,
      "updated_at": "2026-09-12T10:00:00Z", "created_at": "2026-09-12T09:00:00Z", "event_count": 9
    }
  ]
}`

func TestParseRuntimeTaskList_CurrentNodeResponse(t *testing.T) {
	tasks, err := parseRuntimeTaskList("node1", fixturePayload(t, currentTaskListFixture))
	if err != nil {
		t.Fatalf("解析当前节点任务列表失败: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("任务数 = %d, want 2", len(tasks))
	}
	first := tasks[0]
	if first.ID != "task_01" || first.Status != "active" || first.ReviewStatus != "not_started" {
		t.Fatalf("第一条任务字段错误: %#v", first)
	}
	if first.CurrentStep == nil || first.CurrentStep.Title != "复现问题" || first.CurrentStep.Status != "in_progress" {
		t.Fatalf("current_step 解析错误: %#v", first.CurrentStep)
	}
	if first.CompletedStepCount != 1 || first.StepCount != 3 || first.EventCount != 4 {
		t.Fatalf("计数错误: %#v", first)
	}
	if tasks[1].Status != "completed" || tasks[1].CurrentStep != nil {
		t.Fatalf("第二条任务解析错误: %#v", tasks[1])
	}
}

func TestParseRuntimeTaskList_LegacyShapeStillParses(t *testing.T) {
	// 旧版 AgentDock 用 task_id 作为任务标识、current_step 可能缺失；
	// 旧形状必须解析出真实值，而不是静默变成空值。
	payload := fixturePayload(t, `{
	  "tasks": [
	    {"task_id": "legacy_1", "status": "blocked", "review_status": "failed",
	     "step_count": 0, "completed_step_count": 0, "updated_at": "2026-01-01T00:00:00Z"}
	  ]
	}`)
	tasks, err := parseRuntimeTaskList("node1", payload)
	if err != nil {
		t.Fatalf("解析旧形状任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "legacy_1" {
		t.Fatalf("旧形状任务解析错误: %#v", tasks)
	}
	if tasks[0].Status != "blocked" || tasks[0].ReviewStatus != "failed" {
		t.Fatalf("旧形状枚举字段丢失: %#v", tasks[0])
	}
}

func TestParseRuntimeTaskList_MissingTaskIDReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"tasks": [{"title": "没有 ID 的任务", "status": "active", "review_status": "not_started", "step_count": 1, "completed_step_count": 0, "updated_at": "2026-09-13T08:00:00Z"}]}`)
	_, err := parseRuntimeTaskList("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks", "tasks[0].id")
}

func TestParseRuntimeTaskList_TasksWrongTypeReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"tasks": {"id": "task_01"}}`)
	_, err := parseRuntimeTaskList("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks", "tasks")
}

func TestParseRuntimeTaskList_InvalidStatusEnumReturnsContractError(t *testing.T) {
	// 上游把任务状态改名或扩展时，Nexus 必须显式报错，而不是把未知状态当 active 计数。
	payload := fixturePayload(t, `{"tasks": [{"id": "task_01", "status": "done", "review_status": "not_started", "step_count": 1, "completed_step_count": 0, "updated_at": "2026-09-13T08:00:00Z"}]}`)
	_, err := parseRuntimeTaskList("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks", "tasks[0].status")
	if !strings.Contains(err.Error(), "done") {
		t.Fatalf("错误应包含非法枚举值: %v", err)
	}
}

func TestParseRuntimeTaskList_InvalidReviewStatusEnumReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"tasks": [{"id": "task_01", "status": "active", "review_status": "approved", "step_count": 1, "completed_step_count": 0, "updated_at": "2026-09-13T08:00:00Z"}]}`)
	_, err := parseRuntimeTaskList("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks", "tasks[0].review_status")
}

func TestParseRuntimeTaskList_WrongNumberTypeReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"tasks": [{"id": "task_01", "status": "active", "review_status": "not_started", "step_count": "3", "completed_step_count": 0, "updated_at": "2026-09-13T08:00:00Z"}]}`)
	_, err := parseRuntimeTaskList("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks", "tasks[0].step_count")
}

const currentTaskDetailFixture = `{
  "ok": true, "source": "agentdock-api", "action": "get",
  "task": {
    "schema_version": 1, "id": "task_01", "title": "修复登录超时", "goal": "登录不再超时",
    "status": "blocked", "phase": "execute", "blocker": "等待运维确认",
    "summary": "已定位到会话过期配置",
    "steps": [
      {"id": "s1", "title": "复现问题", "phase": "execute", "status": "completed", "updated_at": "2026-09-13T07:30:00Z"},
      {"id": "s2", "title": "修复配置", "phase": "execute", "status": "in_progress", "updated_at": "2026-09-13T08:00:00Z"},
      {"id": "s3", "title": "验证", "phase": "verify", "status": "pending", "updated_at": "2026-09-13T08:01:00Z"}
    ],
    "conditions": [
      {"id": "c1", "text": "登录成功", "created_at": "2026-09-13T07:00:00Z"}
    ],
    "events": [
      {"type": "start", "summary": "任务开始", "created_at": "2026-09-13T07:00:00Z"}
    ],
    "final_review": {
      "status": "failed", "summary": "还没有修复完成",
      "verified_facts": ["超时阈值是 30s"], "open_risks": ["可能影响其他会话"],
      "missing_checks": ["回归测试"], "review_revision": "rev-2", "reviewed_at": "2026-09-13T08:05:00Z"
    },
    "template": {"id": "tpl_fix", "version": "3", "hash": "abc", "selected_reason": "match"},
    "created_at": "2026-09-13T07:00:00Z", "updated_at": "2026-09-13T08:00:00Z"
  }
}`

func TestParseRuntimeTaskDetail_CurrentNodeResponse(t *testing.T) {
	detail, err := parseRuntimeTaskDetail("node1", "task_01", fixturePayload(t, currentTaskDetailFixture))
	if err != nil {
		t.Fatalf("解析任务详情失败: %v", err)
	}
	summary := detail.Summary
	if summary.ID != "task_01" || summary.Status != "blocked" || summary.Blocker != "等待运维确认" {
		t.Fatalf("详情摘要字段错误: %#v", summary)
	}
	// 详情的进度按完整步骤推导：1 个完成、当前步骤是进行中的 s2。
	if summary.CompletedStepCount != 1 || summary.StepCount != 3 {
		t.Fatalf("详情进度计数错误: %#v", summary)
	}
	if summary.CurrentStep == nil || summary.CurrentStep.ID != "s2" {
		t.Fatalf("详情当前步骤错误: %#v", summary.CurrentStep)
	}
	// 详情对象没有顶层 review_status 时回退到 final_review.status。
	if summary.ReviewStatus != "failed" {
		t.Fatalf("review_status 应回退到 final_review.status: %q", summary.ReviewStatus)
	}
	if summary.TemplateID != "tpl_fix" || summary.TemplateVersion != "3" || summary.ConditionCount != 1 || summary.EventCount != 1 {
		t.Fatalf("模板与计数错误: %#v", summary)
	}
	if detail.FinalReview == nil || detail.FinalReview.ReviewRevision != "rev-2" {
		t.Fatalf("final_review 解析错误: %#v", detail.FinalReview)
	}
	if len(detail.FinalReview.VerifiedFacts) != 1 || len(detail.FinalReview.OpenRisks) != 1 || len(detail.FinalReview.MissingChecks) != 1 {
		t.Fatalf("final_review 事实清单解析错误: %#v", detail.FinalReview)
	}
	if len(detail.Steps) != 3 || detail.Steps[2].Status != "pending" || detail.Steps[0].Phase != "execute" {
		t.Fatalf("步骤解析错误: %#v", detail.Steps)
	}
}

func TestParseRuntimeTaskDetail_MissingFinalReviewFallsBackToNotStarted(t *testing.T) {
	payload := fixturePayload(t, `{
	  "task": {"id": "task_01", "title": "t", "goal": "g", "status": "active", "phase": "check", "updated_at": "2026-09-13T08:00:00Z"}
	}`)
	detail, err := parseRuntimeTaskDetail("node1", "task_01", payload)
	if err != nil {
		t.Fatalf("解析无评审任务失败: %v", err)
	}
	if detail.Summary.ReviewStatus != "not_started" || detail.FinalReview != nil {
		t.Fatalf("无 final_review 任务解析错误: %#v", detail.Summary)
	}
	if detail.Summary.CurrentStep != nil || detail.Summary.StepCount != 0 {
		t.Fatalf("无步骤任务摘要错误: %#v", detail.Summary)
	}
}

func TestParseRuntimeTaskDetail_MissingTaskObjectReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"ok": true}`)
	_, err := parseRuntimeTaskDetail("node1", "task_01", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks/task_01", "task")
}

func TestParseRuntimeTaskDetail_MissingTaskIDReturnsContractError(t *testing.T) {
	// 详情此前会把缺失的 task.id 静默回退成路径参数；现在必须显式暴露契约漂移。
	payload := fixturePayload(t, `{"task": {"title": "t", "status": "active", "updated_at": "2026-09-13T08:00:00Z"}}`)
	_, err := parseRuntimeTaskDetail("node1", "task_01", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks/task_01", "task.id")
}

func TestParseRuntimeTaskDetail_InvalidStepStatusReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{
	  "task": {"id": "task_01", "status": "active", "updated_at": "2026-09-13T08:00:00Z",
	           "steps": [{"id": "s1", "title": "x", "status": "doing"}]}
	}`)
	_, err := parseRuntimeTaskDetail("node1", "task_01", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks/task_01", "task.steps[0].status")
}

func TestParseRuntimeTaskDetail_InvalidFinalReviewStatusReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{
	  "task": {"id": "task_01", "status": "active", "updated_at": "2026-09-13T08:00:00Z",
	           "final_review": {"status": "pending"}}
	}`)
	_, err := parseRuntimeTaskDetail("node1", "task_01", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks/task_01", "task.final_review.status")
}

func TestParseRuntimeTaskDetail_UpdatedAtpWrongTypeReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"task": {"id": "task_01", "status": "active", "updated_at": 1726200000}}`)
	_, err := parseRuntimeTaskDetail("node1", "task_01", payload)
	assertContractError(t, err, "GET /internal/runtime/tasks/task_01", "task.updated_at")
}

func TestParseRuntimeSkillList_CurrentNodeResponse(t *testing.T) {
	payload := fixturePayload(t, `{
	  "action": "list", "count": 1, "source": "agentdock-api",
	  "skills": [
	    {"skill": "browser-use", "name": "browser-use", "description": "控制浏览器",
	     "skill_ref": "skill://managed/browser-use", "source_type": "managed",
	     "content_digest": "sha256:abc123", "file_count": 8}
	  ]
	}`)
	skills, err := parseRuntimeSkillList("node1", payload)
	if err != nil {
		t.Fatalf("解析 Skill 列表失败: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("Skill 数 = %d, want 1", len(skills))
	}
	skill := skills[0]
	if skill.Skill != "browser-use" || skill.Name != "browser-use" || skill.FileCount != 8 {
		t.Fatalf("Skill 摘要解析错误: %#v", skill)
	}
	if skill.SkillRef != "skill://managed/browser-use" || skill.SourceType != "managed" ||
		skill.ContentDigest != "sha256:abc123" {
		t.Fatalf("Skill provenance 解析错误: %#v", skill)
	}
}

func TestParseRuntimeSkillList_MissingSkillRefReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"skills": [{
	  "skill": "s", "name": "s", "description": "demo",
	  "source_type": "managed", "content_digest": "sha256:abc", "file_count": 1
	}]}`)
	_, err := parseRuntimeSkillList("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/skills", "skills[0].skill_ref")
}

func TestParseRuntimeSkillList_WrongDigestTypeReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"skills": [{
	  "skill": "s", "name": "s", "description": "demo",
	  "skill_ref": "skill://managed/s", "source_type": "managed",
	  "content_digest": 3, "file_count": 1
	}]}`)
	_, err := parseRuntimeSkillList("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/skills", "skills[0].content_digest")
}

func TestParseRuntimeSkillDetail_CurrentNodeResponse(t *testing.T) {
	payload := fixturePayload(t, `{
	  "action": "inspect", "skill": "browser-use", "name": "browser-use", "description": "控制浏览器",
	  "skill_ref": "skill://managed/browser-use", "source_type": "managed",
	  "content_digest": "sha256:def456",
	  "document": {"name": "browser-use", "description": "控制浏览器", "body": "# Browser Use"},
	  "files": [
	    {"path": "SKILL.md", "kind": "doc", "size_bytes": 1024, "updated_at": "2026-09-10T00:00:00Z"},
	    {"path": "scripts/run.ts", "kind": "code", "size_bytes": 2048, "updated_at": "2026-09-10T00:00:00Z"}
	  ],
	  "file_count": 2, "source": "agentdock-api"
	}`)
	detail, err := parseRuntimeSkillDetail("node1", "browser-use", payload)
	if err != nil {
		t.Fatalf("解析 Skill 详情失败: %v", err)
	}
	if detail.Skill != "browser-use" || detail.Name != "browser-use" || detail.Description != "控制浏览器" {
		t.Fatalf("Skill 详情字段错误: %#v", detail)
	}
	if detail.SkillRef != "skill://managed/browser-use" || detail.SourceType != "managed" ||
		detail.ContentDigest != "sha256:def456" {
		t.Fatalf("Skill 详情 provenance 错误: %#v", detail)
	}
	if len(detail.Files) != 2 || detail.Files[1].Path != "scripts/run.ts" || detail.Files[1].SizeBytes != 2048 {
		t.Fatalf("文件清单解析错误: %#v", detail.Files)
	}
}

func TestParseRuntimeSkillDetail_MismatchedSkillReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{
	  "skill": "other", "name": "other", "description": "demo",
	  "skill_ref": "skill://managed/other", "source_type": "managed",
	  "content_digest": "sha256:abc", "document": {}, "files": []
	}`)
	_, err := parseRuntimeSkillDetail("node1", "s", payload)
	assertContractError(t, err, "GET /internal/runtime/skills/s", "skill")
}

func TestParseRuntimeSkillDetail_MissingContentDigestReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{
	  "skill": "s", "name": "s", "description": "demo",
	  "skill_ref": "skill://managed/s", "source_type": "managed",
	  "document": {}, "files": []
	}`)
	_, err := parseRuntimeSkillDetail("node1", "s", payload)
	assertContractError(t, err, "GET /internal/runtime/skills/s", "content_digest")
}

func TestParseRuntimeSkillDetail_FileWithoutPathReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{
	  "skill": "s", "name": "s", "description": "demo",
	  "skill_ref": "skill://managed/s", "source_type": "managed",
	  "content_digest": "sha256:abc", "document": {},
	  "files": [{"kind": "doc", "size_bytes": 1, "updated_at": "2026-01-01T00:00:00Z"}]
	}`)
	_, err := parseRuntimeSkillDetail("node1", "s", payload)
	assertContractError(t, err, "GET /internal/runtime/skills/s", "files[0].path")
}

func TestParseRuntimeSkillFile_CurrentNodeResponse(t *testing.T) {
	payload := fixturePayload(t, `{
	  "action": "file", "skill": "s", "skill_ref": "skill://managed/s",
	  "content_digest": "sha256:abc", "source": "agentdock-api",
	  "file": {"path": "SKILL.md", "kind": "doc", "size_bytes": 12,
	           "updated_at": "2026-09-10T00:00:00Z", "content": "hello skill", "truncated": false}
	}`)
	file, err := parseRuntimeSkillFile("node1", payload)
	if err != nil {
		t.Fatalf("解析 Skill 文件失败: %v", err)
	}
	if file.Path != "SKILL.md" || file.Content != "hello skill" || file.Truncated || file.SizeBytes != 12 {
		t.Fatalf("文件内容解析错误: %#v", file)
	}
}

func TestParseRuntimeSkillFile_MissingContentReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"file": {"path": "SKILL.md", "kind": "doc", "size_bytes": 0, "updated_at": "2026-01-01T00:00:00Z", "truncated": false}}`)
	_, err := parseRuntimeSkillFile("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/skills/.../files", "file.content")
}

func TestParseRuntimeSkillFile_WrongTruncatedTypeReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"file": {"path": "f", "kind": "doc", "size_bytes": 0, "updated_at": "2026-01-01T00:00:00Z", "content": "", "truncated": "yes"}}`)
	_, err := parseRuntimeSkillFile("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/skills/.../files", "file.truncated")
}

func TestParseRuntimeMCPServers_CurrentNodeResponse(t *testing.T) {
	payload := fixturePayload(t, `{
	  "action": "list", "ok": true, "source": "agentdock-api", "count": 1,
	  "servers": [
	    {"name": "github", "description": "GitHub MCP", "transport": "streamable_http",
	     "enabled": true, "status": "ready", "tool_count": 12,
	     "last_error": "上次连接失败", "last_error_code": "MCP_AUTH_REQUIRED", "refreshed_at": "2026-09-13T00:00:00Z"}
	  ]
	}`)
	servers, err := parseRuntimeMCPServers("node1", payload)
	if err != nil {
		t.Fatalf("解析 MCP 列表失败: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("MCP 数 = %d, want 1", len(servers))
	}
	server := servers[0]
	if server.Name != "github" || !server.Enabled || server.Status != "ready" || server.ToolCount != 12 {
		t.Fatalf("MCP 摘要解析错误: %#v", server)
	}
	if server.LastErrorCode != "MCP_AUTH_REQUIRED" {
		t.Fatalf("MCP 错误码解析错误: %#v", server)
	}
}

func TestParseRuntimeMCPServers_MissingServersArrayReturnsContractError(t *testing.T) {
	// 上游把 servers 改名成 items 时，Nexus 必须显式报错，而不是渲染成空列表。
	payload := fixturePayload(t, `{"action": "list", "ok": true, "count": 0, "items": []}`)
	_, err := parseRuntimeMCPServers("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/mcp", "servers")
}

func TestParseRuntimeMCPServers_ServerWithoutNameReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"servers": [{"transport": "stdio", "status": "ready", "enabled": true, "tool_count": 0}]}`)
	_, err := parseRuntimeMCPServers("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/mcp", "servers[0].name")
}

func TestParseRuntimeMCPServers_WrongToolCountTypeReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"servers": [{"name": "github", "transport": "stdio", "status": "ready", "enabled": true, "tool_count": "12"}]}`)
	_, err := parseRuntimeMCPServers("node1", payload)
	assertContractError(t, err, "GET /internal/runtime/mcp", "servers[0].tool_count")
}

func TestParseRuntimeMCPServerDetail_CurrentNodeResponse(t *testing.T) {
	payload := fixturePayload(t, `{
	  "action": "inspect", "ok": true, "source": "agentdock-api",
	  "server": {"name": "github", "description": "GitHub MCP", "transport": "streamable_http",
	             "enabled": true, "status": "ready", "tool_count": 3},
	  "config": {"name": "github", "description": "GitHub MCP", "transport": "streamable_http",
	             "url": "https://example.com/mcp", "enabled": true, "timeout_ms": 30000,
	             "header_env": {"GITHUB_TOKEN": ""}, "env_from_env": {"GITHUB_TOKEN": "GITHUB_TOKEN"}}
	}`)
	detail, err := parseRuntimeMCPServerDetail("node1", "github", payload)
	if err != nil {
		t.Fatalf("解析 MCP 详情失败: %v", err)
	}
	if detail.Server.Name != "github" || detail.Config.URL != "https://example.com/mcp" || detail.Config.TimeoutMS != 30000 {
		t.Fatalf("MCP 详情解析错误: %#v", detail)
	}
	if _, ok := detail.Config.HeaderEnv["GITHUB_TOKEN"]; !ok {
		t.Fatalf("header_env 解析错误: %#v", detail.Config.HeaderEnv)
	}
}

func TestParseRuntimeMCPServerDetail_MissingConfigReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"server": {"name": "github", "transport": "stdio", "status": "ready", "enabled": true, "tool_count": 0}}`)
	_, err := parseRuntimeMCPServerDetail("node1", "github", payload)
	assertContractError(t, err, "GET /internal/runtime/mcp/github", "config")
}

func TestParseRuntimeMCPEnvironment_CurrentNodeResponse(t *testing.T) {
	payload := fixturePayload(t, `{
	  "action": "env_list", "name": "github", "ok": true, "source": "agentdock-api", "count": 2,
	  "items": [{"key": "API_TOKEN", "configured": true}, {"key": "EMPTY_VAR", "configured": false}]
	}`)
	items, err := parseRuntimeMCPEnvironment("node1", "github", payload)
	if err != nil {
		t.Fatalf("解析 MCP 环境元数据失败: %v", err)
	}
	if len(items) != 2 || items[0].Key != "API_TOKEN" || !items[0].Configured || items[1].Configured {
		t.Fatalf("环境条目解析错误: %#v", items)
	}
}

func TestParseRuntimeMCPEnvironment_EntryWithoutKeyReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"items": [{"configured": true}]}`)
	_, err := parseRuntimeMCPEnvironment("node1", "github", payload)
	assertContractError(t, err, "POST /internal/runtime/mcp env_list github", "items[0].key")
}

func TestParseRuntimeTaskDeleteResult_CurrentNodeResponse(t *testing.T) {
	payload := fixturePayload(t, `{
	  "ok": true, "source": "agentdock-api", "action": "delete", "task_id": "task_01",
	  "deleted_task": {"id": "task_01", "title": "发布 1.2", "status": "completed"}
	}`)
	result, err := parseRuntimeTaskDeleteResult("node1", "task_01", payload)
	if err != nil {
		t.Fatalf("解析删除结果失败: %v", err)
	}
	if result.TaskID != "task_01" || len(result.DeletedTask) == 0 {
		t.Fatalf("删除结果解析错误: %#v", result)
	}
}

func TestParseRuntimeTaskDeleteResult_MissingTaskIDReturnsContractError(t *testing.T) {
	payload := fixturePayload(t, `{"ok": true, "action": "delete"}`)
	_, err := parseRuntimeTaskDeleteResult("node1", "task_01", payload)
	assertContractError(t, err, "DELETE /internal/runtime/tasks/task_01", "task_id")
}

func TestContractErrorAcrossNodesCarriesContext(t *testing.T) {
	// 同样的契约错误在不同节点上必须能区分定位。
	_, errA := parseRuntimeMCPServers("nodeA", fixturePayload(t, `{"servers": {}}`))
	_, errB := parseRuntimeMCPServers("nodeB", fixturePayload(t, `{"servers": {}}`))
	if errA == nil || errB == nil {
		t.Fatalf("期望两个节点都返回契约错误")
	}
	if !strings.Contains(errA.Error(), "nodeA") || !strings.Contains(errB.Error(), "nodeB") {
		t.Fatalf("错误缺少节点上下文: %v / %v", errA, errB)
	}
}
