package httpx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/recall"
)

const (
	agentDockContextToolName   = "agentdock_context"
	agentDockNodeInvokeTimeout = 8 * time.Second
)

var nexusSharedAgentDockRules = []string{
	"涉及多步骤开发、部署、排障、迁移、Docker、VPS 或 Git 提交推送时，先 workflow_template_manage match；无合适模板时创建普通可恢复任务。",
	"当多个工作流模板同时适合当前任务时，调用 workflow_template_manage get_many 读取详情；模型必须结合用户目标裁剪、去重、排序并生成最终 steps 和 completion_conditions，再用 source_template_ids 创建任务，服务端不会自动拼接模板。",
	"普通项目记忆走 recall_*；private_note_manage 只在用户明确要求私密笔记，或内容明显包含 secret、凭据、个人敏感信息时使用。私密检索只返回名称、简介、标签、分类和路径等元数据；正文必须显式 read，Git 只备份 age 密文。",
	"记忆启动索引只提供紧凑背景与资料入口；索引已给出具体 path 时优先 recall_read 该条目，只有索引未覆盖且任务依赖具体历史事实时才 recall_search，索引信息已足够时不要机械检索。",
}

type agentDockContext struct {
	Skills            []agentDockContextSkill       `json:"skills"`
	CommonSkills      *agentDockContextCommonSkills `json:"common_skills,omitempty"`
	DynamicMCP        []agentDockContextItem        `json:"dynamic_mcp"`
	ACP               *agentDockContextACP          `json:"acp,omitempty"`
	WorkflowTemplates []agentDockContextItem        `json:"workflow_templates"`
	Recall            *agentDockContextRecall       `json:"recall,omitempty"`
	Rules             []string                      `json:"rules"`
	Warnings          []agentDockContextWarning     `json:"warnings,omitempty"`
}

type agentDockContextSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	File        string `json:"file"`
	Bundled     bool   `json:"bundled,omitempty"`
}

type agentDockContextCommonSkills struct {
	Root      string                        `json:"root"`
	Total     int                           `json:"total"`
	Truncated bool                          `json:"truncated"`
	Items     []agentDockContextCommonSkill `json:"items"`
}

type agentDockContextCommonSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	File        string `json:"file"`
}

type agentDockContextItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type agentDockContextACP struct {
	Enabled     bool   `json:"enabled"`
	Agent       string `json:"agent"`
	Description string `json:"description"`
}

type agentDockContextRecall struct {
	Enabled bool                   `json:"enabled"`
	Items   []agentDockContextItem `json:"items"`
}

type agentDockContextWarning struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type fleetAgentDockContext struct {
	Nodes  []fleetAgentDockContextNode `json:"nodes"`
	Shared fleetAgentDockSharedContext `json:"shared"`
}

type fleetAgentDockContextNode struct {
	NodeID       string                     `json:"node_id"`
	Name         string                     `json:"name"`
	Online       bool                       `json:"online"`
	Version      string                     `json:"version,omitempty"`
	OS           string                     `json:"os,omitempty"`
	Arch         string                     `json:"arch,omitempty"`
	Capabilities []string                   `json:"capabilities"`
	Context      *fleetAgentDockNodeContext `json:"context,omitempty"`
	Error        string                     `json:"error,omitempty"`
}

type fleetAgentDockNodeContext struct {
	Skills       []agentDockContextSkill       `json:"skills"`
	CommonSkills *agentDockContextCommonSkills `json:"common_skills,omitempty"`
	DynamicMCP   []agentDockContextItem        `json:"dynamic_mcp"`
	ACP          *agentDockContextACP          `json:"acp,omitempty"`
	Rules        []string                      `json:"rules"`
	Warnings     []agentDockContextWarning     `json:"warnings,omitempty"`
}

type fleetAgentDockSharedContext struct {
	WorkflowTemplates []agentDockContextItem    `json:"workflow_templates"`
	Recall            *agentDockContextRecall   `json:"recall,omitempty"`
	Rules             []string                  `json:"rules"`
	Warnings          []agentDockContextWarning `json:"warnings,omitempty"`
}

func (s *Server) callFleetAgentDockContext(ctx context.Context) (map[string]any, error) {
	return s.callFleetAgentDockContextWithTimeout(ctx, agentDockNodeInvokeTimeout)
}

func (s *Server) callFleetAgentDockContextWithTimeout(ctx context.Context, leafTimeout time.Duration) (map[string]any, error) {
	if s.agentDock == nil || s.agentDockHub == nil {
		return nil, errors.New("AgentDock 节点运行时不可用")
	}
	nodes, err := s.agentDock.List(ctx)
	if err != nil {
		return nil, err
	}
	enabled := make([]agentdock.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.Enabled {
			enabled = append(enabled, node)
		}
	}

	fleet := fleetAgentDockContext{
		Nodes:  make([]fleetAgentDockContextNode, len(enabled)),
		Shared: s.buildFleetAgentDockSharedContext(),
	}
	var wait sync.WaitGroup
	for index, node := range enabled {
		index, node := index, node
		fleet.Nodes[index] = fleetAgentDockContextNode{
			NodeID: node.ID, Name: node.Name, Version: node.Version, OS: node.OS, Arch: node.Arch,
			Online: s.agentDockHub.Online(node.ID), Capabilities: deviceNodeCapabilities(node.Capabilities),
		}
		if !containsString(node.Capabilities, agentDockContextToolName) {
			fleet.Nodes[index].Error = "节点未提供 agentdock_context"
			continue
		}
		if !fleet.Nodes[index].Online {
			fleet.Nodes[index].Error = agentdock.ErrNodeOffline.Error()
			continue
		}

		wait.Add(1)
		go func() {
			defer wait.Done()
			leafCtx, cancel := context.WithTimeout(ctx, leafTimeout)
			defer cancel()
			remote, invokeErr := s.agentDockHub.Invoke(leafCtx, node.ID, protocol.OperationContextLocal, map[string]any{})
			if invokeErr != nil {
				if errors.Is(invokeErr, context.DeadlineExceeded) {
					fleet.Nodes[index].Error = "context timeout"
				} else {
					fleet.Nodes[index].Error = invokeErr.Error()
				}
				return
			}
			providerContext, decodeErr := decodeAgentDockContextResult(remote)
			if decodeErr != nil {
				fleet.Nodes[index].Error = decodeErr.Error()
				return
			}
			fleet.Nodes[index].Context = localAgentDockContext(providerContext)
		}()
	}
	wait.Wait()
	return asMap(fleet)
}

func decodeAgentDockContextResult(result map[string]any) (agentDockContext, error) {
	if result == nil {
		return agentDockContext{}, errors.New("agentdock_context 返回空结果")
	}
	if isError, _ := result["isError"].(bool); isError {
		return agentDockContext{}, errors.New(agentDockContextResultError(result))
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		return agentDockContext{}, errors.New("agentdock_context 缺少 structuredContent")
	}
	var decoded agentDockContext
	if err := decodeMap(structured, &decoded); err != nil {
		return agentDockContext{}, fmt.Errorf("解析 agentdock_context: %w", err)
	}
	if decoded.Skills == nil || decoded.DynamicMCP == nil || decoded.WorkflowTemplates == nil || decoded.Rules == nil {
		return agentDockContext{}, errors.New("agentdock_context structuredContent 不符合当前结构化契约")
	}
	if decoded.CommonSkills != nil && decoded.CommonSkills.Items == nil {
		return agentDockContext{}, errors.New("agentdock_context common_skills 不符合当前结构化契约")
	}
	return decoded, nil
}

func agentDockContextResultError(result map[string]any) string {
	if structured, ok := result["structuredContent"].(map[string]any); ok {
		for _, key := range []string{"message", "error"} {
			if value, _ := structured[key].(string); strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return "agentdock_context 调用失败"
}

func localAgentDockContext(context agentDockContext) *fleetAgentDockNodeContext {
	warnings := make([]agentDockContextWarning, 0, len(context.Warnings))
	for _, warning := range context.Warnings {
		if warning.Source == "workflow_templates" || warning.Source == "recall" {
			continue
		}
		warnings = append(warnings, warning)
	}
	localRules := make([]string, 0, len(context.Rules))
	for _, rule := range context.Rules {
		if !containsString(nexusSharedAgentDockRules, strings.TrimSpace(rule)) {
			localRules = append(localRules, rule)
		}
	}
	return &fleetAgentDockNodeContext{
		Skills: context.Skills, CommonSkills: context.CommonSkills, DynamicMCP: context.DynamicMCP, ACP: context.ACP,
		Rules: localRules, Warnings: warnings,
	}
}

func (s *Server) buildFleetAgentDockSharedContext() fleetAgentDockSharedContext {
	shared := fleetAgentDockSharedContext{
		WorkflowTemplates: []agentDockContextItem{},
		Recall:            &agentDockContextRecall{Enabled: true, Items: []agentDockContextItem{}},
		Rules:             append([]string(nil), nexusSharedAgentDockRules...),
	}

	templates, err := s.listWorkflowTemplates(workflowTemplateActive)
	if err != nil {
		shared.Warnings = append(shared.Warnings, agentDockContextWarning{Source: "workflow_templates", Message: "工作流模板索引暂不可用；需要时仍可调用 workflow_template_manage 精确确认。"})
	} else {
		for _, template := range latestWorkflowTemplateVersions(templates) {
			shared.WorkflowTemplates = append(shared.WorkflowTemplates, agentDockContextItem{Name: template.ID, Description: firstNonEmptyString(template.Title, template.ID)})
		}
	}

	if s.store == nil {
		shared.Recall.Enabled = false
		shared.Warnings = append(shared.Warnings, agentDockContextWarning{Source: "recall", Message: "NexusDock Recall 存储暂不可用。"})
		return shared
	}
	index, err := s.store.BuildContextIndex(recall.ContextIndexRequest{Project: "agentdock", MaxBytes: recall.ContextIndexDefaultMaxBytes})
	if err != nil {
		shared.Warnings = append(shared.Warnings, agentDockContextWarning{Source: "recall", Message: "记忆精简索引暂不可用；需要项目事实时调用 recall_search/recall_read 精确确认。"})
		return shared
	}
	seen := make(map[string]struct{}, len(index.Items))
	for _, item := range index.Items {
		name := strings.TrimSpace(item.Path)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		shared.Recall.Items = append(shared.Recall.Items, agentDockContextItem{Name: name, Description: contextIndexDescription(item)})
	}
	if index.Truncated {
		shared.Warnings = append(shared.Warnings, agentDockContextWarning{
			Source:  "recall",
			Message: "记忆启动索引受预算或可读性限制，只包含部分候选；需要具体历史事实时使用 recall_search/recall_read 精确确认。",
		})
	}
	return shared
}

func contextIndexDescription(item recall.ContextIndexItem) string {
	if summary := strings.TrimSpace(item.Summary); summary != "" {
		if title := strings.TrimSpace(item.Title); title != "" {
			return truncateRunes(title+" — "+summary, 360)
		}
		return truncateRunes(summary, 360)
	}
	parts := []string{}
	if title := strings.TrimSpace(item.Title); title != "" {
		parts = append(parts, title)
	}
	if kind := strings.TrimSpace(item.Kind); kind != "" {
		parts = append(parts, kind)
	}
	if item.CardType != "" {
		parts = append(parts, item.CardType)
	}
	labels := append(append(append([]string{}, item.Keywords...), item.Aliases...), item.Tags...)
	if len(labels) > 0 {
		parts = append(parts, strings.Join(labels, ", "))
	}
	return truncateRunes(strings.Join(parts, " · "), 360)
}

func deviceNodeCapabilities(capabilities []string) []string {
	out := make([]string, 0, len(capabilities))
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			continue
		}
		if mcpcontract.IsCanonicalTool(capability) {
			continue
		}
		if _, exists := seen[capability]; exists {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
}
