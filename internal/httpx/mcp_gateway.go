package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	pathpkg "path"
	"regexp"
	"strings"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/observability"
	"github.com/uvwt/nexusdock/internal/privatenotes"
	"github.com/uvwt/nexusdock/internal/recall"
	"go.opentelemetry.io/otel/codes"
)

const nexusServerInstructions = "NexusDock 可以连接并统一操作多台 AgentDock 设备。" +
	"优先调用 `agentdock_context` 获取可用设备、节点标识以及各设备的核心能力、Skill、动态 MCP、Workflow 模板、重要上下文和长期记忆索引。" +
	"需要操作具体设备时，根据 `agentdock_context` 返回的节点信息选择目标 `node_id`。" +
	"操作具体项目、切换工作区或工作区规则可能变化时，调用 `workspace_context` 并传入目标 `node_id` 获取该节点的工作区上下文。" +
	"需要查找或读取长期记忆时使用 `recall_*`；需要查找或使用 Workflow 模板时使用 `workflow_template_manage`；" +
	"处理多步骤任务时使用 `task_manage` 记录和维护任务进度。根据用户需求选择合适的设备和能力，检查、操作并验证设备状态。"

type mcpGateway struct {
	server      *mcpsdk.Server
	handler     http.Handler
	resourcesMu sync.RWMutex
	resources   map[string]struct{}
}

func newMCPGateway() *mcpGateway {
	gateway := &mcpGateway{
		server: mcpsdk.NewServer(
			&mcpsdk.Implementation{Name: "nexusdock", Version: "1"},
			&mcpsdk.ServerOptions{
				Capabilities: &mcpsdk.ServerCapabilities{},
				Instructions: nexusServerInstructions,
			},
		),
		resources: make(map[string]struct{}),
	}
	gateway.handler = mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return gateway.server },
		&mcpsdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, PropagateRequestCancellation: true},
	)
	return gateway
}

func (s *Server) initializeMCPGateway() {
	if s == nil {
		return
	}
	s.mcp = newMCPGateway()
	s.bindMCPGateway()
}

// bindMCPGateway 把已经创建好的 MCP 协议运行态接到 Nexus 业务能力。
// mcpGateway 只持有 MCP SDK server、HTTP handler 与 resource registry 的协议状态；
// 业务依赖和工具编排仍由 Server 负责，避免制造 Server -> gateway -> Server 的假抽象。
func (s *Server) bindMCPGateway() {
	if s == nil || s.mcp == nil {
		return
	}
	s.registerCentralTools()
	s.bindPublishedToolBridge()
	if s.agentDockHub != nil {
		s.agentDockHub.SetHelloHandler(s.registerNodeTools)
	}
	if s.agentDock != nil && s.publishedToolBridge != nil {
		ctx := context.Background()
		if err := s.publishedToolBridge.LoadPublished(ctx); err != nil && s.logger != nil {
			s.logger.Warn("恢复 AgentDock 公开工具契约失败", "error", err)
		}
		if nodes, err := s.agentDock.List(ctx); err == nil {
			for _, node := range nodes {
				descriptors, descriptorErr := s.agentDock.ToolDescriptors(ctx, node.ID)
				if descriptorErr == nil {
					s.registerNodeTools(node, agentdock.Hello{Tools: descriptors})
				}
			}
		}
		// 启动时也核对一次已发布目录，清理旧版本遗留但 fleet 已不再提供的 stale tool。
		s.publishedToolBridge.ReconcilePublished()
	}
	// Nexus 自有的 Context / Recall / Workflow Apps 不依赖任何 AgentDock 节点，启动时始终注册。
	s.syncMCPAppResources()
}

// bindPublishedToolBridge 把契约 Bridge 的公开/退休回调映射为 MCP SDK 的工具注册与下架。
// Bridge 只维护契约业务状态；这里是它触碰 MCP 协议实现的唯一边界。
func (s *Server) bindPublishedToolBridge() {
	if s.publishedToolBridge == nil {
		return
	}
	s.publishedToolBridge.SetPublishHandlers(s.publishNodeTool, s.retireNodeTool)
}

// publishNodeTool 把 Bridge 公开的 fleet 契约映射为 MCP 工具注册（含 MCP Apps 展示元数据过滤）。
func (s *Server) publishNodeTool(descriptor agentdock.ToolDescriptor) {
	if s.mcp == nil || s.mcp.server == nil {
		return
	}
	s.mcp.server.AddTool(nodeMCPToolWithApps(descriptor, s.mcpAppsEnabled()), s.nodeToolHandler(descriptor.Name))
}

// retireNodeTool 把 Bridge 的工具下架映射为 MCP SDK 的工具移除。
func (s *Server) retireNodeTool(toolName string) {
	if s.mcp == nil || s.mcp.server == nil {
		return
	}
	s.mcp.server.RemoveTools(toolName)
}

func (s *Server) registerCentralTools() {
	if s == nil || s.mcp == nil || s.mcp.server == nil {
		return
	}
	for _, definition := range nexusToolDefinitionsWithApps(s.mcpAppsEnabled()) {
		definition := definition
		// Nexus 自有工具使用 SDK 的 typed AddTool 入口，让协议仓库声明的 InputSchema /
		// OutputSchema 在真实调用时执行校验；工具内部仍按各自明确输入结构解码，避免 map
		// 成为业务数据模型。节点透传工具契约由远端 AgentDock 决定，继续走动态边界。
		mcpsdk.AddTool[map[string]any, map[string]any](
			s.mcp.server,
			definition,
			func(ctx context.Context, request *mcpsdk.CallToolRequest, _ map[string]any) (*mcpsdk.CallToolResult, map[string]any, error) {
				arguments := json.RawMessage(nil)
				if request != nil && request.Params != nil {
					arguments = request.Params.Arguments
				}
				result, err := s.callNexusTool(ctx, definition.Name, arguments)
				meta := centralToolResultMetaWithApps(definition.Name, arguments, s.mcpAppsEnabled())
				if err == nil {
					// 让 SDK 直接序列化并校验成功结果，避免 map -> JSON -> CallToolResult 的二次往返。
					return &mcpsdk.CallToolResult{Meta: meta}, result, nil
				}
				response, responseErr := s.gatewayToolResult(definition.Name, result, err)
				if responseErr == nil && response != nil {
					response.Meta = meta
				}
				return response, nil, responseErr
			},
		)
	}
}

func (s *Server) mcpAppsEnabled() bool {
	if s == nil {
		return false
	}
	s.mcpAppsMu.RLock()
	defer s.mcpAppsMu.RUnlock()
	return s.mcpAppsEnabledState
}

func (s *Server) setMCPAppsEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.mcpAppsMu.Lock()
	changed := s.mcpAppsEnabledState != enabled
	s.mcpAppsEnabledState = enabled
	s.mcpAppsMu.Unlock()
	if !changed || s.mcp == nil || s.mcp.server == nil {
		return
	}

	// MCP Apps UI 只属于展示层；刷新对外工具和资源，不改节点持久化 descriptor。
	s.registerCentralTools()
	if s.publishedToolBridge != nil {
		for _, published := range s.publishedToolBridge.PublishedTools() {
			s.mcp.server.AddTool(nodeMCPToolWithApps(published.Descriptor, enabled), s.nodeToolHandler(published.Descriptor.Name))
		}
	}
	s.syncMCPAppResources()
}

// registerNodeTools 处理节点 Hello 快照：契约收敛业务由 Bridge 负责，
// 这里只在工具注册集合可能变化后同步一次 MCP Apps 资源目录。
func (s *Server) registerNodeTools(node agentdock.Node, hello agentdock.Hello) {
	if s.publishedToolBridge != nil {
		s.publishedToolBridge.ObserveNodeHello(node, hello)
	}
	s.syncMCPAppResources()
}

// reconcileNodeTools 在节点启停、删除或能力变化后触发 fleet 工具契约重算，
// 并在工具注册集合可能变化后同步 MCP Apps 资源目录。
func (s *Server) reconcileNodeTools(descriptors []agentdock.ToolDescriptor) {
	if s.publishedToolBridge != nil {
		s.publishedToolBridge.ReconcileNodeTools(descriptors)
	}
	s.syncMCPAppResources()
}

func nodeMCPTool(descriptor agentdock.ToolDescriptor) *mcpsdk.Tool {
	return nodeMCPToolWithApps(descriptor, true)
}

func nodeMCPToolWithApps(descriptor agentdock.ToolDescriptor, mcpAppsEnabled bool) *mcpsdk.Tool {
	tool := &mcpsdk.Tool{
		Name: descriptor.Name, Title: descriptor.Title, Description: descriptor.Description,
		InputSchema: nodeInputSchema(descriptor.InputSchema), OutputSchema: descriptor.OutputSchema,
	}
	if len(descriptor.Annotations) > 0 {
		encoded, _ := json.Marshal(descriptor.Annotations)
		var annotations mcpsdk.ToolAnnotations
		if json.Unmarshal(encoded, &annotations) == nil {
			tool.Annotations = &annotations
		}
	}
	if len(descriptor.Meta) > 0 {
		meta := make(mcpsdk.Meta, len(descriptor.Meta))
		for key, value := range descriptor.Meta {
			if key == "ui" && !mcpAppsEnabled {
				continue
			}
			meta[key] = value
		}
		if len(meta) > 0 {
			tool.Meta = meta
		}
	}
	return tool
}

func (s *Server) nodeToolHandler(name string) mcpsdk.ToolHandler {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		arguments, err := dynamicToolArguments(request)
		if err != nil {
			return nil, err
		}
		return s.callNodeTool(ctx, name, arguments)
	}
}

func (s *Server) callNodeTool(ctx context.Context, name string, arguments map[string]any) (*mcpsdk.CallToolResult, error) {
	nodeID, _ := arguments["node_id"].(string)
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return s.gatewayToolResult(name, nil, errors.New("node_id is required"))
	}
	node, err := s.agentDock.Get(ctx, nodeID)
	if err != nil {
		return s.gatewayToolResult(name, nil, err)
	}
	if !containsString(node.Capabilities, name) {
		return s.gatewayToolResult(name, nil, fmt.Errorf("AgentDock node %s does not provide tool %s", nodeID, name))
	}

	if s.publishedToolBridge == nil {
		return s.gatewayToolResult(name, nil, fmt.Errorf("Nexus 公开工具契约不存在: %s", name))
	}
	mismatch, err := s.publishedToolBridge.ToolContractMismatch(ctx, node, name)
	if err != nil {
		return s.gatewayToolResult(name, nil, err)
	}
	if mismatch != nil {
		details, encodeErr := asBoundaryMap(mismatch)
		if encodeErr != nil {
			return nil, encodeErr
		}
		return s.gatewayToolResult(name, details, errors.New(mismatch.Message))
	}

	delete(arguments, "node_id")
	var result map[string]any
	func() {
		invokeCtx, span := s.tracing.StartAgentDockInvoke(ctx, name)
		defer span.End()
		result, err = s.agentDockHub.Invoke(invokeCtx, nodeID, protocol.OperationToolCall, map[string]any{"tool": name, "arguments": arguments})
		traceID, spanID := observability.TraceIdentifiers(ctx, invokeCtx)
		if err != nil && span.IsRecording() {
			// 只记录稳定状态，不调用 RecordError(err)，避免把原始错误正文写入 Trace。
			span.SetStatus(codes.Error, "agentdock invoke failed")
		}
		if s.logger != nil && traceID != "" {
			attributes := []any{"tool", name, "ok", err == nil, "trace_id", traceID}
			if spanID != "" {
				attributes = append(attributes, "span_id", spanID)
			}
			s.logger.Debug("AgentDock tool call finished", attributes...)
		}
	}()
	if err == nil {
		bridgeCapabilities, capabilityErr := s.agentDock.BridgeCapabilities(ctx, nodeID)
		if capabilityErr != nil {
			if s.logger != nil {
				s.logger.Warn("读取 AgentDock Bridge 能力失败，保留原始工具结果", "node_id", nodeID, "error", capabilityErr)
			}
		} else if containsString(bridgeCapabilities, protocol.ArtifactReadCapability) {
			if decorateErr := s.decorateArtifactToolResult(nodeID, result); decorateErr != nil && s.logger != nil {
				s.logger.Warn("生成 Nexus Artifact 下载地址失败，保留原始工具结果", "node_id", nodeID, "error", decorateErr)
			}
		}
	}
	return s.gatewayToolResult(name, result, err)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum]) + "…"
}

func nodeInputSchema(schema map[string]any) map[string]any {
	encoded, _ := json.Marshal(schema)
	cloned := map[string]any{"type": "object"}
	_ = json.Unmarshal(encoded, &cloned)
	properties, _ := cloned["properties"].(map[string]any)
	if properties == nil {
		properties = make(map[string]any)
		cloned["properties"] = properties
	}
	properties["node_id"] = map[string]any{"type": "string", "description": "Target AgentDock node ID from agentdock_context."}
	required, _ := cloned["required"].([]any)
	for _, value := range required {
		if value == "node_id" {
			return cloned
		}
	}
	cloned["required"] = append(required, "node_id")
	return cloned
}

func dynamicToolArguments(request *mcpsdk.CallToolRequest) (map[string]any, error) {
	// 节点工具是 Nexus 不理解的开放协议载荷；这里保持动态 JSON 仅用于透明转发，
	// 不把它继续传入 Nexus 自有 Recall / Workflow / Private Note 编排。
	arguments := map[string]any{}
	if request == nil || request.Params == nil || len(request.Params.Arguments) == 0 || string(request.Params.Arguments) == "null" {
		return arguments, nil
	}
	if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
		return nil, errors.New("tool arguments must be a JSON object")
	}
	return arguments, nil
}

func gatewayToolResult(name string, result map[string]any, err error) (*mcpsdk.CallToolResult, error) {
	if err == nil && result != nil {
		if _, hasContent := result["content"]; hasContent {
			if _, hasErrorFlag := result["isError"]; hasErrorFlag {
				encoded, encodeErr := json.Marshal(result)
				if encodeErr != nil {
					return nil, encodeErr
				}
				var proxied mcpsdk.CallToolResult
				if decodeErr := json.Unmarshal(encoded, &proxied); decodeErr != nil {
					return nil, decodeErr
				}
				return &proxied, nil
			}
		}
	}
	if result == nil {
		result = map[string]any{}
	}
	if err != nil {
		// 保留调用方提供的结构化错误详情，避免契约差异等可操作信息被统一错误包装丢失。
		result["tool"] = name
		result["error"] = err.Error()
	}
	encoded, encodeErr := json.Marshal(map[string]any{
		"isError": err != nil, "structuredContent": result,
		"content": []map[string]any{{"type": "text", "text": prettyJSON(result)}},
	})
	if encodeErr != nil {
		return nil, encodeErr
	}
	var response mcpsdk.CallToolResult
	if decodeErr := json.Unmarshal(encoded, &response); decodeErr != nil {
		return nil, decodeErr
	}
	return &response, nil
}

func (s *Server) gatewayToolResult(name string, result map[string]any, err error) (*mcpsdk.CallToolResult, error) {
	response, responseErr := gatewayToolResult(name, result, err)
	if responseErr != nil || response == nil || s.mcpAppsEnabled() || response.Meta == nil {
		return response, responseErr
	}
	delete(response.Meta, "ui")
	if len(response.Meta) == 0 {
		response.Meta = nil
	}
	return response, nil
}

func prettyJSON(value any) string {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func (s *Server) decorateRecallSearchResults(results []recall.SearchResult) ([]map[string]any, error) {
	decorated := make([]map[string]any, 0, len(results))
	if len(results) == 0 {
		return decorated, nil
	}
	baseURL, err := url.Parse(strings.TrimSpace(s.cfg.PublicURL))
	if err != nil || baseURL == nil || !baseURL.IsAbs() || baseURL.Host == "" {
		return nil, errors.New("NEXUS_PUBLIC_URL is required to generate recall_search citation URLs")
	}
	for _, result := range results {
		item, err := asBoundaryMap(result)
		if err != nil {
			return nil, err
		}
		path := strings.TrimSpace(result.Path)
		if path == "" {
			continue
		}
		item["id"] = path
		if strings.TrimSpace(result.Title) == "" {
			name := pathpkg.Base(path)
			item["title"] = strings.TrimSuffix(name, pathpkg.Ext(name))
		}
		sourceURL := *baseURL
		query := sourceURL.Query()
		query.Set("path", path)
		sourceURL.RawQuery = query.Encode()
		sourceURL.Fragment = "recall/library"
		item["url"] = sourceURL.String()
		decorated = append(decorated, item)
	}
	return decorated, nil
}

func (s *Server) callRecallSearch(ctx context.Context, input recallSearchInput) (map[string]any, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	if kind == "" {
		kind = "all"
	}
	options := recall.SearchOptions{Query: query, MaxResults: normalizedPositive(input.MaxResults, 20)}
	switch kind {
	case "card":
		options.Prefix = "recall/managed/cards"
	case "markdown":
		options.ExcludePrefix = "recall/managed/cards"
	case "all":
	default:
		return nil, fmt.Errorf("unsupported recall_search kind: %s", kind)
	}
	results, err := s.executeRecallSearch(ctx, options)
	if err != nil {
		return nil, err
	}
	decorated, err := s.decorateRecallSearchResults(results)
	if err != nil {
		return nil, err
	}
	return asBoundaryMap(map[string]any{
		"query": query, "recall_kind": kind, "results": decorated, "count": len(decorated),
		"recall_store": "NexusDock Recall", "recall_endpoint": s.cfg.PublicURL,
	})
}

func (s *Server) callRecallRead(input recallReadInput) (map[string]any, error) {
	path := strings.TrimSpace(input.Path)
	if strings.HasPrefix(path, "private-notes/") {
		return nil, errors.New("private notes must be read through private_note_manage")
	}
	memory, err := s.store.Read(path)
	if err != nil {
		return nil, err
	}
	item, err := asBoundaryMap(memory)
	if err != nil {
		return nil, err
	}
	content, _ := item["content"].(string)
	delete(item, "content")
	if input.IncludeRaw {
		item["raw_content"] = content
	}
	return asBoundaryMap(map[string]any{
		"recall": item, "recall_store": "NexusDock Recall", "recall_endpoint": s.cfg.PublicURL,
	})
}

func (s *Server) callNexusTool(ctx context.Context, name string, raw json.RawMessage) (map[string]any, error) {
	switch name {
	case "agentdock_context":
		return s.callFleetAgentDockContext(ctx)
	case "workflow_template_manage":
		var input workflowTemplateManageInput
		if err := decodeToolInput(raw, &input); err != nil {
			return nil, err
		}
		return s.callWorkflowTemplateManage(ctx, input)
	case "recall_search":
		var input recallSearchInput
		if err := decodeToolInput(raw, &input); err != nil {
			return nil, err
		}
		return s.callRecallSearch(ctx, input)
	case "recall_read":
		var input recallReadInput
		if err := decodeToolInput(raw, &input); err != nil {
			return nil, err
		}
		return s.callRecallRead(input)
	case "recall_write":
		input, err := decodeRecallWriteInput(raw)
		if err != nil {
			return nil, err
		}
		return s.callRecallWrite(ctx, input)
	case "recall_maintain":
		var input recallMaintainInput
		if err := decodeToolInput(raw, &input); err != nil {
			return nil, err
		}
		return s.callRecallMaintain(ctx, input)
	case "private_note_manage":
		var input privateNoteManageInput
		if err := decodeToolInput(raw, &input); err != nil {
			return nil, err
		}
		return s.callPrivateNote(ctx, input)
	default:
		return nil, fmt.Errorf("unknown NexusDock tool: %s", name)
	}
}

func centralToolResultMeta(name string, raw json.RawMessage) mcpsdk.Meta {
	return centralToolResultMetaWithApps(name, raw, true)
}

func centralToolResultMetaWithApps(name string, raw json.RawMessage, mcpAppsEnabled bool) mcpsdk.Meta {
	if !mcpAppsEnabled || name != "workflow_template_manage" {
		return nil
	}
	var input workflowTemplateManageInput
	if decodeToolInput(raw, &input) == nil && strings.EqualFold(strings.TrimSpace(input.Action), "match") {
		return centralToolUIResourceMeta(protocol.WorkflowUIResourceURI)
	}
	return nil
}

func (s *Server) callRecallWrite(ctx context.Context, input recallWriteInput) (map[string]any, error) {
	target := strings.ToLower(strings.TrimSpace(input.Target))
	action := strings.ToLower(strings.TrimSpace(input.Action))
	result, err := s.callRecallWriteOperation(ctx, input, target, action)
	if result != nil {
		delete(result, "ok")
		result["recall_target"] = target
		result["recall_action"] = action
		result["recall_endpoint"] = s.cfg.PublicURL
	}
	return result, err
}

func (s *Server) callRecallWriteOperation(ctx context.Context, input recallWriteInput, target, action string) (map[string]any, error) {
	dryRun := input.DryRun
	confirmed := input.Confirmed
	if target == "card" {
		previewOnly := dryRun || !confirmed
		request := recall.CardRequest{
			Title: input.Title, Content: input.Content, Summary: input.Summary, Path: input.Path,
			Confirmed: input.Confirmed, Overwrite: input.Overwrite, AllowWarnings: input.AllowWarnings,
		}
		if action == "plan" || (action == "create" && previewOnly) {
			result, err := s.store.CaptureCard(request)
			mapped, mapErr := asBoundaryMap(result, err)
			if mapErr != nil {
				return nil, mapErr
			}
			mapped["dry_run"] = true
			return mapped, nil
		}
		if action != "create" {
			return nil, errors.New("card only supports plan and create")
		}
		result, err := s.store.WriteCard(request)
		return asBoundaryMap(result, err)
	}
	if target != "markdown" {
		return nil, errors.New("target must be card or markdown")
	}
	path := strings.TrimSpace(input.Path)
	if action == "delete" {
		if dryRun {
			current, err := s.store.Read(path)
			if err != nil {
				return nil, err
			}
			return asBoundaryMap(map[string]any{"path": path, "dry_run": true, "would_delete": true, "size_bytes": current.SizeBytes})
		}
		if !confirmed {
			return nil, recall.ErrConfirmationNeeded
		}
		err := s.store.Delete(path, true)
		return asBoundaryMap(map[string]any{"path": path, "deleted": err == nil}, err)
	}
	if action == "update_fact" {
		return s.updateRecallFacts(ctx, input)
	}
	request := recall.WriteRequest{
		Path: path, Content: input.Content, Confirmed: input.Confirmed, Overwrite: input.Overwrite,
	}
	var beforeEdit string
	hasBeforeEdit := false
	switch action {
	case "plan", "create":
		request.Overwrite = false
	case "replace":
		request.Overwrite = true
	case "append", "patch":
		current, err := s.store.Read(path)
		if err != nil {
			return nil, err
		}
		appendText := input.Append
		if action == "append" && strings.TrimSpace(appendText) == "" {
			appendText = input.Content
		}
		if action == "append" && strings.TrimSpace(appendText) == "" {
			return nil, errors.New("append or content is required")
		}
		old, replacement := "", ""
		section, sectionContent := "", ""
		if action == "patch" {
			old, replacement = input.Old, input.New
			section, sectionContent = input.Section, input.SectionContent
			if strings.TrimSpace(section) != "" && sectionContent == "" {
				sectionContent = input.Content
			}
		}
		content, _, err := recall.ApplyMarkdownPatch(current.Content, old, replacement, section, sectionContent, appendText)
		if err != nil {
			return nil, err
		}
		beforeEdit, hasBeforeEdit = current.Content, true
		request.Content, request.Overwrite = content, true
	case "diff":
		current, err := s.store.Read(path)
		if err != nil {
			return nil, err
		}
		proposed := input.Content
		changeCount := 0
		if proposed == "" {
			proposed, changeCount, err = recall.ApplyMarkdownPatch(
				current.Content, input.Old, input.New, input.Section, input.SectionContent, input.Append,
			)
			if err != nil {
				return nil, err
			}
		}
		maxBytes := normalizedPositive(input.MaxBytes, 60000)
		diff := recall.UnifiedDiff(path, current.Content, proposed, maxBytes)
		return asBoundaryMap(map[string]any{
			"path": path, "changed": current.Content != proposed, "diff": diff,
			"truncated": len(diff) >= maxBytes, "change_count": changeCount,
		})
	default:
		return nil, fmt.Errorf("unsupported markdown action: %s", action)
	}
	previewOnly := action == "plan" || dryRun
	if action == "replace" || action == "append" || action == "patch" {
		previewOnly = previewOnly || !confirmed
	}
	if previewOnly {
		preview, err := s.store.PreviewWrite(request)
		if err != nil {
			return nil, err
		}
		result := map[string]any{
			"dry_run": true, "confirmed": confirmed, "path": preview.Path,
			"proposed_content": preview.ProposedContent, "overwrite": preview.Overwrite,
		}
		if hasBeforeEdit {
			maxBytes := normalizedPositive(input.MaxBytes, 60000)
			diff := recall.UnifiedDiff(path, beforeEdit, preview.ProposedContent, maxBytes)
			result["changed"] = beforeEdit != preview.ProposedContent
			result["diff"] = diff
			result["truncated"] = len(diff) >= maxBytes
		}
		return asBoundaryMap(result)
	}
	result, err := s.store.Write(request)
	return asBoundaryMap(map[string]any{"recall": result, "recall_store": "NexusDock Recall"}, err)
}

func (s *Server) callRecallMaintain(ctx context.Context, input recallMaintainInput) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action == "" {
		action = "list"
	}
	result, err := s.callRecallMaintainOperation(ctx, input, action)
	if result != nil {
		delete(result, "ok")
		result["recall_action"] = action
		result["recall_endpoint"] = s.cfg.PublicURL
	}
	return result, err
}

func (s *Server) callRecallMaintainOperation(ctx context.Context, input recallMaintainInput, action string) (map[string]any, error) {
	switch action {
	case "list":
		entries, err := s.store.List(strings.TrimSpace(input.Prefix), normalizedPositive(input.MaxEntries, 200))
		return asBoundaryMap(map[string]any{"entries": entries, "count": len(entries)}, err)
	case "lint":
		return s.lintRecall(input)
	case "embedding_status":
		embedding := s.runtimeAI.currentEmbedding()
		if embedding == nil {
			return map[string]any{"enabled": false}, nil
		}
		return embeddingStatusMCPResult(embedding.Status(ctx)), nil
	case "reindex", "reindex_cards":
		embedding := s.runtimeAI.currentEmbedding()
		if embedding == nil {
			return nil, errors.New("embedding service is not configured")
		}
		prefix := strings.TrimSpace(input.Prefix)
		if action == "reindex_cards" && prefix == "" {
			prefix = "recall/managed/cards"
		}
		result, err := embedding.Reindex(ctx, recall.EmbeddingReindexRequest{Prefix: prefix})
		return asBoundaryMap(result, err)
	default:
		return nil, fmt.Errorf("unsupported recall maintenance action: %s", action)
	}
}

func embeddingStatusMCPResult(status recall.EmbeddingStatus) map[string]any {
	result := map[string]any{
		"ok": status.OK, "enabled": status.Enabled, "model": status.Model, "configured": status.Configured,
	}
	if status.Endpoint != "" {
		result["endpoint"] = status.Endpoint
	}
	if status.IndexPath != "" {
		result["index_path"] = status.IndexPath
	}
	if status.Index != nil {
		result["index"] = *status.Index
	}
	if status.Reachable != nil {
		result["reachable"] = *status.Reachable
	}
	if status.Reason != "" {
		result["reason"] = status.Reason
	}
	if status.Error != "" {
		result["error"] = status.Error
	}
	return result
}

func (s *Server) updateRecallFacts(ctx context.Context, input recallWriteInput) (map[string]any, error) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return nil, errors.New("path is required")
	}
	facts := make(map[string]string, len(input.Facts)+1)
	if key := strings.TrimSpace(input.Key); key != "" {
		if input.Value == "" {
			return nil, errors.New("value is required when key is provided")
		}
		facts[key] = input.Value
	}
	for key, value := range input.Facts {
		if key = strings.TrimSpace(key); key != "" {
			facts[key] = value
		}
	}
	current, err := s.store.Read(path)
	if err != nil {
		return nil, err
	}
	updated, updates, err := recall.UpdateMarkdownFacts(
		current.Content, input.Section, facts, input.AppendIfMissing,
	)
	if err != nil {
		return nil, err
	}
	maxBytes := normalizedPositive(input.MaxBytes, 60000)
	diff := recall.UnifiedDiff(path, current.Content, updated, maxBytes)
	changed := updated != current.Content
	preview := map[string]any{
		"path": path, "changed": changed, "confirmed": input.Confirmed,
		"updates": updates, "diff": diff, "truncated": len(diff) >= maxBytes,
	}
	if input.DryRun || !input.Confirmed || !changed {
		preview["dry_run"] = true
		return asBoundaryMap(preview)
	}
	result, err := s.store.Write(recall.WriteRequest{Path: path, Content: updated, Confirmed: true, Overwrite: true})
	return asBoundaryMap(map[string]any{
		"path": path, "changed": true, "confirmed": true, "written": err == nil,
		"updates": updates, "diff": diff, "truncated": len(diff) >= maxBytes, "recall": result,
	}, err)
}

func (s *Server) lintRecall(input recallMaintainInput) (map[string]any, error) {
	terms := input.Terms
	if len(terms) == 0 {
		terms = []string{"Connector", "connector", "CONNECTOR", "connectors", "connector_"}
	}
	entries, err := s.store.List(strings.TrimSpace(input.Prefix), normalizedPositive(input.MaxEntries, 200))
	if err != nil {
		return nil, err
	}
	maximum := normalizedPositive(input.MaxFindings, 200)
	findings := make([]map[string]any, 0)
	filesScanned := 0
	var expressions []*regexp.Regexp
	if input.Regex {
		expressions = make([]*regexp.Regexp, len(terms))
		for i, term := range terms {
			expression, compileErr := regexp.Compile(term)
			if compileErr != nil {
				return nil, fmt.Errorf("invalid lint regular expression %q: %w", term, compileErr)
			}
			expressions[i] = expression
		}
	}
	for _, entry := range entries {
		if len(findings) >= maximum || !strings.HasSuffix(strings.ToLower(entry.Path), ".md") {
			continue
		}
		memory, readErr := s.store.Read(entry.Path)
		if readErr != nil {
			continue
		}
		filesScanned++
		for lineIndex, line := range strings.Split(memory.Content, "\n") {
			for termIndex, term := range terms {
				matched := strings.Contains(line, term)
				if input.Regex {
					matched = expressions[termIndex].MatchString(line)
				}
				if matched {
					findings = append(findings, map[string]any{"path": entry.Path, "line": lineIndex + 1, "term": term, "text": line})
				}
				if len(findings) >= maximum {
					break
				}
			}
		}
	}
	return asBoundaryMap(map[string]any{
		"terms": terms, "regex": input.Regex, "files_scanned": filesScanned,
		"finding_count": len(findings), "findings": findings, "truncated": len(findings) >= maximum,
	})
}

func (s *Server) callPrivateNote(ctx context.Context, input privateNoteManageInput) (map[string]any, error) {
	if s.privateNotes == nil {
		return nil, errors.New("private notes are not configured")
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	result, err := s.callPrivateNoteOperation(ctx, input, action)
	if result != nil {
		delete(result, "ok")
		result["action"] = action
		result["private_note_store"] = "NexusDock Private Notes"
		result["recall_endpoint"] = s.cfg.PublicURL
	}
	return result, err
}

func (s *Server) callPrivateNoteOperation(ctx context.Context, input privateNoteManageInput, action string) (map[string]any, error) {
	switch action {
	case "search":
		result, err := s.executePrivateNoteSearch(ctx, privateNoteSearchRequest{
			Query: input.Query, MaxResults: input.MaxResults,
		})
		return asBoundaryMap(result, err)
	case "read":
		result, err := s.privateNotes.Read(input.Path, input.MaxBytes)
		return asBoundaryMap(result, err)
	case "write":
		result, err := s.privateNotes.Write(privatenotes.WriteRequest{
			Path: input.Path, Category: input.Category, Title: input.Title, Summary: input.Summary,
			Tags: input.Tags, Content: input.Content, Confirmed: input.Confirmed, Overwrite: input.Overwrite,
		})
		return asBoundaryMap(result, err)
	case "delete":
		result, err := s.privateNotes.Delete(input.Path, input.Confirmed)
		return asBoundaryMap(result, err)
	case "status":
		result, err := s.privateNotes.Status(ctx, input.StatusAction)
		return asBoundaryMap(result, err)
	case "maintain":
		result, err := s.privateNotes.Maintain(ctx, input.MaintenanceAction)
		return asBoundaryMap(result, err)
	default:
		return nil, fmt.Errorf("unsupported private note action: %s", action)
	}
}

// asBoundaryMap 只用于把已完成编排的明确结果转换成 MCP JSON 输出形状。
func asBoundaryMap(value any, optionalErr ...error) (map[string]any, error) {
	if len(optionalErr) > 0 && optionalErr[0] != nil {
		return nil, optionalErr[0]
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// decodeBoundaryMap 只用于解码 AgentDock/Runtime 返回的开放协议对象。
func decodeBoundaryMap(value map[string]any, destination any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, destination)
}
