package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/nexusdock/internal/agentdock"
)

const maxToolContractDifferences = 5

var errIncompatibleToolContract = errors.New("AgentDock 工具契约不可安全合并")

type publishedNodeTool struct {
	Descriptor             agentdock.ToolDescriptor
	ContractHash           string
	AcceptedSemanticHashes []string
}

type toolContractDifference struct {
	Path      string `json:"path"`
	Published any    `json:"published"`
	Node      any    `json:"node"`
}

type toolContractMismatch struct {
	Code          string                   `json:"code"`
	Message       string                   `json:"message"`
	Tool          string                   `json:"tool"`
	NodeID        string                   `json:"node_id"`
	NodeName      string                   `json:"node_name,omitempty"`
	NodeVersion   string                   `json:"node_version,omitempty"`
	PublishedHash string                   `json:"published_hash"`
	NodeHash      string                   `json:"node_hash"`
	Differences   []toolContractDifference `json:"differences,omitempty"`
}

type comparableToolContract struct {
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Meta         map[string]any `json:"_meta,omitempty"`
}

func toolContractHash(descriptor agentdock.ToolDescriptor) (string, error) {
	contract, err := comparableContract(descriptor)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		return "", fmt.Errorf("编码工具 %s 契约: %w", descriptor.Name, err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

func comparableContract(descriptor agentdock.ToolDescriptor) (comparableToolContract, error) {
	inputSchema, err := semanticSchemaMap(descriptor.InputSchema)
	if err != nil {
		return comparableToolContract{}, fmt.Errorf("规范化工具 %s 输入契约: %w", descriptor.Name, err)
	}
	outputSchema, err := semanticSchemaMap(descriptor.OutputSchema)
	if err != nil {
		return comparableToolContract{}, fmt.Errorf("规范化工具 %s 输出契约: %w", descriptor.Name, err)
	}
	return comparableToolContract{
		InputSchema: inputSchema, OutputSchema: outputSchema,
		Meta: executionToolMeta(descriptor.Meta),
	}, nil
}

func executionToolMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	execution := make(map[string]any, len(meta))
	for key, value := range meta {
		if key == "ui" {
			continue
		}
		execution[key] = value
	}
	if len(execution) == 0 {
		return nil
	}
	return execution
}

// semanticSchemaMap 只去掉不影响校验结果的展示字段，并规范 JSON Schema 中本身无顺序语义的集合。
// 其他关键字保持原样，避免把真实的版本或实现差异误判成兼容。
func semanticSchemaMap(schema map[string]any) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}
	normalized, err := normalizeJSONValue(schema)
	if err != nil {
		return nil, err
	}
	value, ok := normalized.(map[string]any)
	if !ok {
		return nil, errors.New("schema 不是 JSON object")
	}
	return semanticSchemaObject(value), nil
}

func normalizeJSONValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func semanticSchemaObject(schema map[string]any) map[string]any {
	result := make(map[string]any, len(schema))
	for key, value := range schema {
		if key == "description" || key == "title" {
			continue
		}
		normalized := semanticSchemaKeywordValue(key, value)
		if key == "required" {
			if items, ok := normalized.([]any); ok && len(items) == 0 {
				continue
			}
		}
		result[key] = normalized
	}
	return result
}

func semanticSchemaKeywordValue(key string, value any) any {
	switch key {
	case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
		mapping, ok := value.(map[string]any)
		if !ok {
			return value
		}
		result := make(map[string]any, len(mapping))
		for name, child := range mapping {
			if schema, ok := child.(map[string]any); ok {
				result[name] = semanticSchemaObject(schema)
			} else {
				result[name] = child
			}
		}
		return result
	case "items", "contains", "not", "if", "then", "else", "propertyNames", "additionalProperties", "unevaluatedProperties":
		if schema, ok := value.(map[string]any); ok {
			return semanticSchemaObject(schema)
		}
		return value
	case "allOf", "anyOf", "oneOf", "prefixItems":
		items, ok := value.([]any)
		if !ok {
			return value
		}
		result := make([]any, len(items))
		for index, child := range items {
			if schema, ok := child.(map[string]any); ok {
				result[index] = semanticSchemaObject(schema)
			} else {
				result[index] = child
			}
		}
		return result
	case "required", "enum":
		items, ok := value.([]any)
		if !ok {
			return value
		}
		result := append([]any(nil), items...)
		sort.Slice(result, func(i, j int) bool {
			left, _ := json.Marshal(result[i])
			right, _ := json.Marshal(result[j])
			return bytes.Compare(left, right) < 0
		})
		return result
	default:
		return value
	}
}

func (s *Server) publishedNodeTool(name string) (publishedNodeTool, bool) {
	s.mcpToolsMu.RLock()
	defer s.mcpToolsMu.RUnlock()
	tool, ok := s.mcpTools[name]
	return tool, ok
}

func (s *Server) publishedNodeToolNames() []string {
	s.mcpToolsMu.RLock()
	defer s.mcpToolsMu.RUnlock()
	names := make([]string, 0, len(s.mcpTools))
	for name := range s.mcpTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *Server) loadPublishedNodeTools(ctx context.Context) error {
	contracts, err := s.agentDock.ListPublishedToolContracts(ctx)
	if err != nil {
		return err
	}
	for _, contract := range contracts {
		if mcpcontract.IsCanonicalTool(contract.ToolName) {
			// 已提升为 Nexus 中央工具的旧节点契约不再属于 fleet 发布状态，启动时直接清掉持久化残留。
			if err := s.agentDock.DeletePublishedToolContract(ctx, contract.ToolName); err != nil {
				return err
			}
			continue
		}
		if strings.TrimSpace(contract.ToolName) == "" {
			continue
		}
		hash, err := toolContractHash(contract.Descriptor)
		if err != nil {
			return err
		}
		acceptedHashes := normalizeToolContractHashes(contract.AcceptedSemanticHashes)
		if len(acceptedHashes) == 0 {
			// 旧版数据库没有 variant 子表数据时，至少保留原来公开 descriptor 对应的真实契约。
			acceptedHashes = []string{hash}
		}
		published := publishedNodeTool{
			Descriptor: contract.Descriptor, ContractHash: hash, AcceptedSemanticHashes: acceptedHashes,
		}
		s.mcpServer.AddTool(nodeMCPToolWithApps(contract.Descriptor, s.mcpAppsEnabled()), s.nodeToolHandler(contract.ToolName))
		s.mcpTools[contract.ToolName] = published
	}
	return nil
}

func (s *Server) persistPublishedNodeTool(ctx context.Context, published publishedNodeTool) error {
	if s.agentDock == nil {
		return nil
	}
	return s.agentDock.SavePublishedToolContract(ctx, agentdock.PublishedToolContract{
		ToolName: published.Descriptor.Name, Descriptor: published.Descriptor,
		AcceptedSemanticHashes: published.AcceptedSemanticHashes,
	})
}

func (s *Server) reconcileFleetNodeTool(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || mcpcontract.IsCanonicalTool(name) {
		return nil
	}
	if s.agentDock == nil {
		return nil
	}
	// 节点状态先落库再触发 reconcile；串行化整个快照到发布过程，避免旧快照晚于新快照覆盖 published generation。
	s.mcpReconcileMu.Lock()
	defer s.mcpReconcileMu.Unlock()

	ctx := context.Background()
	nodes, err := s.agentDock.List(ctx)
	if err != nil {
		return err
	}
	descriptors := make([]agentdock.ToolDescriptor, 0)
	hasKnownProvider := false
	for _, node := range nodes {
		if !containsString(node.Capabilities, name) {
			continue
		}
		hasKnownProvider = true
		if !node.Enabled {
			continue
		}
		nodeDescriptors, err := s.agentDock.ToolDescriptors(ctx, node.ID)
		if err != nil {
			return err
		}
		descriptor, ok := findToolDescriptor(nodeDescriptors, name)
		if !ok {
			return fmt.Errorf("AgentDock node %s does not provide tool descriptor %s", node.ID, name)
		}
		descriptors = append(descriptors, descriptor)
	}
	if len(descriptors) == 0 {
		// 被禁用的节点仍属于 fleet；没有任何 provider 时才真正下架公开工具。
		if hasKnownProvider {
			return nil
		}
		s.mcpToolsMu.Lock()
		defer s.mcpToolsMu.Unlock()
		if _, exists := s.mcpTools[name]; !exists {
			return nil
		}
		if err := s.agentDock.DeletePublishedToolContract(ctx, name); err != nil {
			return err
		}
		if s.mcpServer != nil {
			s.mcpServer.RemoveTools(name)
		}
		delete(s.mcpTools, name)
		return nil
	}

	descriptor, acceptedHashes, err := mergeFleetToolDescriptors(descriptors)
	if err != nil {
		if errors.Is(err, errIncompatibleToolContract) {
			return nil
		}
		return err
	}
	contractHash, err := toolContractHash(descriptor)
	if err != nil {
		return err
	}
	candidate := publishedNodeTool{
		Descriptor: descriptor, ContractHash: contractHash, AcceptedSemanticHashes: acceptedHashes,
	}

	s.mcpToolsMu.Lock()
	published, exists := s.mcpTools[name]
	descriptorChanged := !exists || !reflect.DeepEqual(published.Descriptor, candidate.Descriptor)
	if exists && published.ContractHash == candidate.ContractHash &&
		reflect.DeepEqual(published.AcceptedSemanticHashes, candidate.AcceptedSemanticHashes) && !descriptorChanged {
		s.mcpToolsMu.Unlock()
		return nil
	}
	if err := s.persistPublishedNodeTool(ctx, candidate); err != nil {
		s.mcpToolsMu.Unlock()
		return err
	}
	if s.mcpServer != nil && descriptorChanged {
		s.mcpServer.AddTool(nodeMCPToolWithApps(candidate.Descriptor, s.mcpAppsEnabled()), s.nodeToolHandler(name))
	}
	s.mcpTools[name] = candidate
	s.mcpToolsMu.Unlock()
	return nil
}

func mergeFleetToolDescriptors(descriptors []agentdock.ToolDescriptor) (agentdock.ToolDescriptor, []string, error) {
	if len(descriptors) == 0 {
		return agentdock.ToolDescriptor{}, nil, fmt.Errorf("%w: 没有 provider", errIncompatibleToolContract)
	}
	merged, err := cloneToolDescriptor(descriptors[0])
	if err != nil {
		return agentdock.ToolDescriptor{}, nil, err
	}
	acceptedHashes := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.Name != merged.Name {
			return agentdock.ToolDescriptor{}, nil, fmt.Errorf("%w: tool name %q != %q", errIncompatibleToolContract, descriptor.Name, merged.Name)
		}
		hash, err := toolContractHash(descriptor)
		if err != nil {
			return agentdock.ToolDescriptor{}, nil, err
		}
		acceptedHashes = append(acceptedHashes, hash)
	}
	for _, descriptor := range descriptors[1:] {
		if !jsonValuesEqual(executionToolMeta(merged.Meta), executionToolMeta(descriptor.Meta)) {
			return agentdock.ToolDescriptor{}, nil, fmt.Errorf("%w: %s execution meta", errIncompatibleToolContract, merged.Name)
		}
		merged.InputSchema, err = mergeSchemaMaps("inputSchema", merged.InputSchema, descriptor.InputSchema)
		if err != nil {
			return agentdock.ToolDescriptor{}, nil, err
		}
		merged.OutputSchema, err = mergeSchemaMaps("outputSchema", merged.OutputSchema, descriptor.OutputSchema)
		if err != nil {
			return agentdock.ToolDescriptor{}, nil, err
		}
	}
	// _meta.ui 是展示绑定，不属于节点 resource provider 能力；只有所有 provider 展示元数据一致时才保留。
	// resource.read provider 由 Hello.ui_resources 独立决定，安全提示仍按 MCP 默认语义保守合并。
	merged.Meta = mergeFleetToolMeta(descriptors)
	merged.Annotations = mergeFleetToolAnnotations(descriptors)
	return merged, normalizeToolContractHashes(acceptedHashes), nil
}

func mergeFleetToolMeta(descriptors []agentdock.ToolDescriptor) map[string]any {
	if len(descriptors) == 0 || len(descriptors[0].Meta) == 0 {
		return nil
	}
	common := make(map[string]any, len(descriptors[0].Meta))
	for key, value := range descriptors[0].Meta {
		common[key] = value
	}
	for _, descriptor := range descriptors[1:] {
		for key, value := range common {
			other, ok := descriptor.Meta[key]
			if !ok || !jsonValuesEqual(value, other) {
				delete(common, key)
			}
		}
	}
	if len(common) == 0 {
		return nil
	}
	return common
}

func mergeFleetToolAnnotations(descriptors []agentdock.ToolDescriptor) map[string]any {
	hasAnnotations := false
	for _, descriptor := range descriptors {
		if len(descriptor.Annotations) > 0 {
			hasAnnotations = true
			break
		}
	}
	if !hasAnnotations {
		return nil
	}

	// MCP defaults: readOnly=false, destructive=true, idempotent=false, openWorld=true.
	// 只在所有 provider 都给出更安全的保证时收紧提示；任一 provider 可能产生副作用或访问开放世界时保持保守值。
	readOnly := true
	idempotent := true
	destructive := false
	openWorld := false
	for _, descriptor := range descriptors {
		annotations := descriptor.Annotations
		readOnly = readOnly && annotationBool(annotations, "readOnlyHint", false)
		idempotent = idempotent && annotationBool(annotations, "idempotentHint", false)
		destructive = destructive || annotationBool(annotations, "destructiveHint", true)
		openWorld = openWorld || annotationBool(annotations, "openWorldHint", true)
	}

	merged := mergeFleetToolAnnotationCommonValues(descriptors)
	if merged == nil {
		merged = make(map[string]any, 4)
	}
	merged["readOnlyHint"] = readOnly
	merged["destructiveHint"] = destructive
	merged["idempotentHint"] = idempotent
	merged["openWorldHint"] = openWorld
	return merged
}

func mergeFleetToolAnnotationCommonValues(descriptors []agentdock.ToolDescriptor) map[string]any {
	if len(descriptors) == 0 || len(descriptors[0].Annotations) == 0 {
		return nil
	}
	common := make(map[string]any, len(descriptors[0].Annotations))
	for key, value := range descriptors[0].Annotations {
		common[key] = value
	}
	for _, descriptor := range descriptors[1:] {
		for key, value := range common {
			other, ok := descriptor.Annotations[key]
			if !ok || !jsonValuesEqual(value, other) {
				delete(common, key)
			}
		}
	}
	if len(common) == 0 {
		return nil
	}
	return common
}

func annotationBool(annotations map[string]any, key string, defaultValue bool) bool {
	value, ok := annotations[key]
	if !ok || value == nil {
		return defaultValue
	}
	parsed, ok := value.(bool)
	if !ok {
		return defaultValue
	}
	return parsed
}

func cloneToolDescriptor(descriptor agentdock.ToolDescriptor) (agentdock.ToolDescriptor, error) {
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return agentdock.ToolDescriptor{}, fmt.Errorf("复制工具 %s 契约: %w", descriptor.Name, err)
	}
	var cloned agentdock.ToolDescriptor
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return agentdock.ToolDescriptor{}, fmt.Errorf("复制工具 %s 契约: %w", descriptor.Name, err)
	}
	return cloned, nil
}

func mergeSchemaMaps(path string, left, right map[string]any) (map[string]any, error) {
	if left == nil || right == nil {
		if left == nil && right == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s schema presence differs", errIncompatibleToolContract, path)
	}
	leftValue, err := normalizeJSONValue(left)
	if err != nil {
		return nil, err
	}
	rightValue, err := normalizeJSONValue(right)
	if err != nil {
		return nil, err
	}
	return mergeSchemaObjects(path, leftValue.(map[string]any), rightValue.(map[string]any))
}

func mergeSchemaObjects(path string, left, right map[string]any) (map[string]any, error) {
	leftRequired, err := schemaRequiredSet(left)
	if err != nil {
		return nil, fmt.Errorf("%w: %s.required: %v", errIncompatibleToolContract, path, err)
	}
	rightRequired, err := schemaRequiredSet(right)
	if err != nil {
		return nil, fmt.Errorf("%w: %s.required: %v", errIncompatibleToolContract, path, err)
	}
	if !reflect.DeepEqual(leftRequired, rightRequired) {
		return nil, fmt.Errorf("%w: %s.required differs", errIncompatibleToolContract, path)
	}

	result := make(map[string]any, len(left)+len(right))
	keys := unionMapKeys(left, right)
	for _, key := range keys {
		leftValue, leftOK := left[key]
		rightValue, rightOK := right[key]
		switch key {
		case "title", "description":
			if leftOK {
				result[key] = leftValue
			} else if rightOK {
				result[key] = rightValue
			}
		case "required":
			if len(leftRequired) > 0 {
				required := make([]string, 0, len(leftRequired))
				for item := range leftRequired {
					required = append(required, item)
				}
				sort.Strings(required)
				result[key] = required
			} else if leftOK {
				result[key] = leftValue
			} else if rightOK {
				result[key] = rightValue
			}
		case "properties":
			properties, err := mergeSchemaProperties(path+".properties", leftValue, leftOK, rightValue, rightOK, leftRequired, rightRequired)
			if err != nil {
				return nil, err
			}
			if properties != nil {
				result[key] = properties
			}
		default:
			if !leftOK || !rightOK || !semanticSchemaValuesEqual(leftValue, rightValue, key) {
				return nil, fmt.Errorf("%w: %s.%s differs", errIncompatibleToolContract, path, key)
			}
			result[key] = leftValue
		}
	}
	return result, nil
}

func mergeSchemaProperties(path string, leftValue any, leftOK bool, rightValue any, rightOK bool, leftRequired, rightRequired map[string]struct{}) (map[string]any, error) {
	left, err := schemaProperties(leftValue, leftOK)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", errIncompatibleToolContract, path, err)
	}
	right, err := schemaProperties(rightValue, rightOK)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", errIncompatibleToolContract, path, err)
	}
	if !leftOK && !rightOK {
		return nil, nil
	}

	result := make(map[string]any, len(left)+len(right))
	for _, name := range unionMapKeys(left, right) {
		leftSchema, inLeft := left[name]
		rightSchema, inRight := right[name]
		switch {
		case inLeft && inRight:
			leftMap, leftIsMap := leftSchema.(map[string]any)
			rightMap, rightIsMap := rightSchema.(map[string]any)
			if leftIsMap && rightIsMap {
				merged, err := mergeSchemaObjects(path+"."+name, leftMap, rightMap)
				if err != nil {
					return nil, err
				}
				result[name] = merged
				continue
			}
			if !semanticSchemaValuesEqual(leftSchema, rightSchema, name) {
				return nil, fmt.Errorf("%w: %s.%s differs", errIncompatibleToolContract, path, name)
			}
			result[name] = leftSchema
		case inLeft:
			if _, required := leftRequired[name]; required {
				return nil, fmt.Errorf("%w: %s.%s only exists on one provider but is required", errIncompatibleToolContract, path, name)
			}
			result[name] = leftSchema
		case inRight:
			if _, required := rightRequired[name]; required {
				return nil, fmt.Errorf("%w: %s.%s only exists on one provider but is required", errIncompatibleToolContract, path, name)
			}
			result[name] = rightSchema
		}
	}
	return result, nil
}

func schemaProperties(value any, exists bool) (map[string]any, error) {
	if !exists {
		return map[string]any{}, nil
	}
	properties, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("properties 不是 JSON object")
	}
	return properties, nil
}

func schemaRequiredSet(schema map[string]any) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	value, ok := schema["required"]
	if !ok {
		return set, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("required 不是数组")
	}
	for _, item := range items {
		name, ok := item.(string)
		if !ok {
			return nil, errors.New("required 包含非字符串值")
		}
		set[name] = struct{}{}
	}
	return set, nil
}

func semanticSchemaValuesEqual(left, right any, parentKey string) bool {
	leftNormalized := semanticSchemaKeywordValue(parentKey, left)
	rightNormalized := semanticSchemaKeywordValue(parentKey, right)
	return reflect.DeepEqual(leftNormalized, rightNormalized)
}

func jsonValuesEqual(left, right any) bool {
	leftNormalized, leftErr := normalizeJSONValue(left)
	rightNormalized, rightErr := normalizeJSONValue(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(leftNormalized, rightNormalized)
}

func unionMapKeys(left, right map[string]any) []string {
	keys := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keys[key] = struct{}{}
	}
	for key := range right {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	return ordered
}

func normalizeToolContractHashes(hashes []string) []string {
	unique := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		hash = strings.TrimSpace(hash)
		if hash != "" {
			unique[hash] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(unique))
	for hash := range unique {
		normalized = append(normalized, hash)
	}
	sort.Strings(normalized)
	return normalized
}

func containsToolContractHash(hashes []string, target string) bool {
	index := sort.SearchStrings(hashes, target)
	return index < len(hashes) && hashes[index] == target
}

func (s *Server) reconcileNodeToolContracts(names []string) {
	defer s.syncMCPAppResources()
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		// 节点启停或删除后的 fleet 重算也会收到完整 descriptor 名单；中央工具始终由
		// Nexus 唯一持有，不能在这条旁路中被重新发布为要求 node_id 的节点工具。
		if name == "" || mcpcontract.IsCanonicalTool(name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if err := s.reconcileFleetNodeTool(name); err != nil && s.logger != nil {
			s.logger.Warn("检查 AgentDock 工具契约收敛失败", "tool", name, "error", err)
		}
	}
}

func toolDescriptorNames(descriptors []agentdock.ToolDescriptor) []string {
	names := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if name := strings.TrimSpace(descriptor.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func findToolDescriptor(descriptors []agentdock.ToolDescriptor, name string) (agentdock.ToolDescriptor, bool) {
	for _, descriptor := range descriptors {
		if descriptor.Name == name {
			return descriptor, true
		}
	}
	return agentdock.ToolDescriptor{}, false
}

func (s *Server) nodeToolContractMismatch(ctx context.Context, node agentdock.Node, name string) (*toolContractMismatch, error) {
	published, ok := s.publishedNodeTool(name)
	if !ok {
		return nil, fmt.Errorf("Nexus 公开工具契约不存在: %s", name)
	}
	descriptors, err := s.agentDock.ToolDescriptors(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	target, ok := findToolDescriptor(descriptors, name)
	if !ok {
		return nil, fmt.Errorf("AgentDock node %s does not provide tool descriptor %s", node.ID, name)
	}
	nodeHash, err := toolContractHash(target)
	if err != nil {
		return nil, err
	}
	if containsToolContractHash(published.AcceptedSemanticHashes, nodeHash) {
		return nil, nil
	}

	return &toolContractMismatch{
		Code:          "TOOL_CONTRACT_MISMATCH",
		Message:       "目标 AgentDock 的工具契约不在 Nexus 当前已发布的兼容集合中，请刷新 GPT 工具；若仍不一致，请检查相关设备的 AgentDock 版本或工具契约。",
		Tool:          name,
		NodeID:        node.ID,
		NodeName:      node.Name,
		NodeVersion:   node.Version,
		PublishedHash: published.ContractHash,
		NodeHash:      nodeHash,
		Differences:   toolContractDifferences(published.Descriptor, target),
	}, nil
}

func toolContractDifferences(published, node agentdock.ToolDescriptor) []toolContractDifference {
	publishedValue, publishedOK := normalizedContractValue(published)
	nodeValue, nodeOK := normalizedContractValue(node)
	if !publishedOK || !nodeOK {
		return nil
	}
	differences := make([]toolContractDifference, 0, maxToolContractDifferences)
	collectToolContractDifferences("", publishedValue, nodeValue, &differences)
	return differences
}

func normalizedContractValue(descriptor agentdock.ToolDescriptor) (map[string]any, bool) {
	contract, err := comparableContract(descriptor)
	if err != nil {
		return nil, false
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		return nil, false
	}
	var value map[string]any
	if json.Unmarshal(encoded, &value) != nil {
		return nil, false
	}
	return value, true
}

func collectToolContractDifferences(path string, published, node any, differences *[]toolContractDifference) {
	if len(*differences) >= maxToolContractDifferences || reflect.DeepEqual(published, node) {
		return
	}
	publishedMap, publishedIsMap := published.(map[string]any)
	nodeMap, nodeIsMap := node.(map[string]any)
	if publishedIsMap && nodeIsMap {
		keys := unionMapKeys(publishedMap, nodeMap)
		for _, key := range keys {
			nextPath := key
			if path != "" {
				nextPath = path + "." + key
			}
			publishedValue, publishedExists := publishedMap[key]
			nodeValue, nodeExists := nodeMap[key]
			if !publishedExists || !nodeExists {
				*differences = append(*differences, toolContractDifference{Path: nextPath, Published: publishedValue, Node: nodeValue})
			} else {
				collectToolContractDifferences(nextPath, publishedValue, nodeValue, differences)
			}
			if len(*differences) >= maxToolContractDifferences {
				return
			}
		}
		return
	}
	*differences = append(*differences, toolContractDifference{Path: path, Published: published, Node: node})
}
