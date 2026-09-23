package agentdock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	protocol "github.com/uvwt/agentdock-protocol"
)

// runtimeRequestTimeout 与 AgentDock direct Runtime API 保持同样的 8 秒边界；
// 调用方已有更短 deadline 时不会被延长。
const runtimeRequestTimeout = 8 * time.Second

// ErrBridgeUnavailable 表示 Nexus 没有装配 AgentDock 连接服务（未启用节点功能），而不是某个节点离线。
var ErrBridgeUnavailable = errors.New("AgentDock 节点连接服务不可用")

// NodeLookupError 表示读取 Nexus 节点库失败（区别于节点不存在）。
type NodeLookupError struct{ Err error }

func (e *NodeLookupError) Error() string { return "查询 AgentDock 节点失败: " + e.Err.Error() }
func (e *NodeLookupError) Unwrap() error { return e.Err }

// invokeRuntime 执行一次 Runtime API 调用并返回上游原始 JSON 对象。
// 所有类型化 Runtime 方法都经过这里，统一处理节点存在性、超时与 Bridge 消息封装。
// Hub 为 nil 或节点表为 nil 时返回 ErrBridgeUnavailable，保持与"未启用节点功能"的服务器行为一致。
func (h *Hub) invokeRuntime(ctx context.Context, nodeID, method, path string, query url.Values, body []byte) (map[string]any, error) {
	if h == nil || h.store == nil {
		return nil, ErrBridgeUnavailable
	}
	requestCtx, cancel := context.WithTimeout(ctx, runtimeRequestTimeout)
	defer cancel()
	if _, err := h.store.Get(requestCtx, nodeID); err != nil {
		if errors.Is(err, ErrNodeNotFound) {
			return nil, err
		}
		return nil, &NodeLookupError{Err: err}
	}
	arguments := map[string]any{"method": method, "path": path}
	if len(query) > 0 {
		arguments["query"] = query
	}
	if len(body) > 0 {
		arguments["body"] = json.RawMessage(body)
	}
	return h.Invoke(requestCtx, nodeID, protocol.OperationRuntimeRequest, arguments)
}

// RuntimeTasks 拉取节点任务列表并解析为 DTO；limit 由 Nexus 侧 UI 约束（最大 200）。
func (h *Hub) RuntimeTasks(ctx context.Context, nodeID string, limit int) ([]RuntimeTaskSummary, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodGet, "/internal/runtime/tasks", query, nil)
	if err != nil {
		return nil, err
	}
	return parseRuntimeTaskList(nodeID, payload)
}

// RuntimeTask 读取单个任务的完整状态。
func (h *Hub) RuntimeTask(ctx context.Context, nodeID, taskID string) (RuntimeTaskDetail, error) {
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodGet, "/internal/runtime/tasks/"+url.PathEscape(taskID), nil, nil)
	if err != nil {
		return RuntimeTaskDetail{}, err
	}
	return parseRuntimeTaskDetail(nodeID, taskID, payload)
}

// RuntimeDeleteTask 删除指定任务并返回确认结果。
func (h *Hub) RuntimeDeleteTask(ctx context.Context, nodeID, taskID string) (RuntimeTaskDeleteResult, error) {
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodDelete, "/internal/runtime/tasks/"+url.PathEscape(taskID), nil, nil)
	if err != nil {
		return RuntimeTaskDeleteResult{}, err
	}
	return parseRuntimeTaskDeleteResult(nodeID, taskID, payload)
}

// RuntimeSkills 列出节点上已安装的 Skill。
func (h *Hub) RuntimeSkills(ctx context.Context, nodeID string) ([]RuntimeSkillSummary, error) {
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodGet, "/internal/runtime/skills", nil, nil)
	if err != nil {
		return nil, err
	}
	return parseRuntimeSkillList(nodeID, payload)
}

// RuntimeSkill 读取单个 Skill 的详情与文件清单。
// 第二个返回值是上游原始响应 JSON，供 UI 的“原始响应”调试面板透传展示。
func (h *Hub) RuntimeSkill(ctx context.Context, nodeID, skillID, skillRef string) (RuntimeSkillDetail, json.RawMessage, error) {
	query := url.Values{}
	if strings.TrimSpace(skillRef) != "" {
		query.Set("skill_ref", strings.TrimSpace(skillRef))
	}
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodGet, "/internal/runtime/skills/"+url.PathEscape(skillID), query, nil)
	if err != nil {
		return RuntimeSkillDetail{}, nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return RuntimeSkillDetail{}, nil, fmt.Errorf("编码 AgentDock Skill 详情原始响应: %w", err)
	}
	detail, err := parseRuntimeSkillDetail(nodeID, skillID, payload)
	if err != nil {
		return RuntimeSkillDetail{}, nil, err
	}
	return detail, raw, nil
}

// RuntimeSkillFile 读取 Skill 包内的文本文件内容。
func (h *Hub) RuntimeSkillFile(ctx context.Context, nodeID, skillID, skillRef, filePath string) (RuntimeSkillFileContent, error) {
	query := url.Values{}
	if strings.TrimSpace(skillRef) != "" {
		query.Set("skill_ref", strings.TrimSpace(skillRef))
	}
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodGet, "/internal/runtime/skills/"+skillID+"/files/"+filePath, query, nil)
	if err != nil {
		return RuntimeSkillFileContent{}, err
	}
	return parseRuntimeSkillFile(nodeID, payload)
}

// RuntimeMCPServers 列出节点上注册的动态 MCP 服务。
func (h *Hub) RuntimeMCPServers(ctx context.Context, nodeID string) ([]RuntimeMCPServerSummary, error) {
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodGet, "/internal/runtime/mcp", nil, nil)
	if err != nil {
		return nil, err
	}
	return parseRuntimeMCPServers(nodeID, payload)
}

// RuntimeMCPServer 读取单个动态 MCP 服务的连接摘要与配置。
func (h *Hub) RuntimeMCPServer(ctx context.Context, nodeID, name string) (RuntimeMCPServerDetail, error) {
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodGet, "/internal/runtime/mcp/"+url.PathEscape(name), nil, nil)
	if err != nil {
		return RuntimeMCPServerDetail{}, err
	}
	return parseRuntimeMCPServerDetail(nodeID, name, payload)
}

// RuntimeMCPEnvironment 读取 MCP 隔离环境的变量名元数据。
func (h *Hub) RuntimeMCPEnvironment(ctx context.Context, nodeID, name string) ([]RuntimeMCPEnvEntry, error) {
	request, err := json.Marshal(map[string]any{"action": "env_list", "name": name})
	if err != nil {
		return nil, fmt.Errorf("编码 MCP env_list 请求: %w", err)
	}
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodPost, "/internal/runtime/mcp", nil, request)
	if err != nil {
		return nil, err
	}
	return parseRuntimeMCPEnvironment(nodeID, name, payload)
}

// RuntimeMCPManage 转发动态 MCP 管理操作。
// 响应形状随 action 不同而不同，Nexus 不解读其内容，因此以 map 形式透传给 UI；
// 仅强制删除顶层 value 字段——AgentDock 正常不会回显密钥值，这里兜底防止上游契约回归造成泄露。
func (h *Hub) RuntimeMCPManage(ctx context.Context, nodeID string, request json.RawMessage) (map[string]any, error) {
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodPost, "/internal/runtime/mcp", nil, request)
	if err != nil {
		return nil, err
	}
	delete(payload, "value")
	return payload, nil
}

// RuntimeEvolve 提交进化候选；Nexus 只关心上游是否接受，不解析响应内容。
func (h *Hub) RuntimeEvolve(ctx context.Context, nodeID string, payload json.RawMessage) error {
	_, err := h.invokeRuntime(ctx, nodeID, http.MethodPost, "/internal/runtime/evolve", nil, payload)
	return err
}
