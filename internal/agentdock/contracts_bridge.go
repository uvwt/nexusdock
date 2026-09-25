package agentdock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/uvwt/agentdock-protocol/mcpcontract"
)

// PublishedTool 是当前内存中的 fleet 公开契约状态：Descriptor 是对 MCP 客户端公开的 schema，
// ContractHash 是公开 descriptor 自身的哈希，AcceptedSemanticHashes 是这一代公开契约
// 允许调用的真实节点变体集合（节点现场哈希必须命中集合才放行调用）。
type PublishedTool struct {
	Descriptor             ToolDescriptor
	ContractHash           string
	AcceptedSemanticHashes []string
}

// PublishedToolBridge 把多台 AgentDock 节点上报的工具契约收敛为对 MCP 客户端稳定的公开契约：
// 首次公开先持久化再发布，Fleet 分歧时以合并结果为准，最后一个 provider 撤销能力后退休工具。
// Bridge 只维护契约业务状态与持久化；MCP SDK 的工具注册/下架由 HTTP 层通过
// SetPublishHandlers 绑定的回调完成映射，Bridge 自身不感知任何 MCP 协议实现。
type PublishedToolBridge struct {
	store   *Store
	logger  *slog.Logger
	publish func(descriptor ToolDescriptor)
	retire  func(toolName string)

	// reconcileMu 串行化"fleet 快照 → 持久化 → 发布"全过程，
	// 避免旧快照晚于新快照覆盖 published generation。
	reconcileMu sync.Mutex
	mu          sync.RWMutex
	published   map[string]PublishedTool
}

func NewPublishedToolBridge(store *Store, logger *slog.Logger) *PublishedToolBridge {
	return &PublishedToolBridge{store: store, logger: logger, published: make(map[string]PublishedTool)}
}

// isNexusOwnedCanonicalTool 区分“共享 canonical 契约”和“Nexus 运行时所有权”。
// workspace_context 的 schema 由 mcpcontract 统一定义，但执行仍属于具体 AgentDock 节点；
// 其他 canonical 工具由 Nexus 自己提供，不能再次从节点 fleet 发布。
func isNexusOwnedCanonicalTool(name string) bool {
	return mcpcontract.IsCanonicalTool(name) && name != mcpcontract.ToolWorkspaceContext
}

// SetPublishHandlers 绑定公开契约变化的协议层回调：publish 在工具首次公开或公开
// descriptor 变化时收到完整描述符，retire 在工具彻底下架时收到工具名。
// 必须在触发任何契约变化（LoadPublished、ObserveNodeHello 等）之前调用一次。
func (b *PublishedToolBridge) SetPublishHandlers(publish func(descriptor ToolDescriptor), retire func(toolName string)) {
	b.publish = publish
	b.retire = retire
}

// LoadPublished 从持久化层恢复已公开契约并重新发布。由 Nexus 自己提供的 canonical 工具
// 不再属于 node fleet 发布状态，启动时直接清掉历史持久化残留。
func (b *PublishedToolBridge) LoadPublished(ctx context.Context) error {
	contracts, err := b.store.ListPublishedToolContracts(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, contract := range contracts {
		if isNexusOwnedCanonicalTool(contract.ToolName) {
			if err := b.store.DeletePublishedToolContract(ctx, contract.ToolName); err != nil {
				return err
			}
			continue
		}
		if strings.TrimSpace(contract.ToolName) == "" {
			continue
		}
		hash, err := ToolContractHash(contract.Descriptor)
		if err != nil {
			return err
		}
		acceptedHashes := normalizeSemanticHashes(contract.AcceptedSemanticHashes)
		if len(acceptedHashes) == 0 {
			// 旧版数据库没有 variant 子表数据时，至少保留原来公开 descriptor 对应的真实契约。
			acceptedHashes = []string{hash}
		}
		b.publish(contract.Descriptor)
		b.published[contract.ToolName] = PublishedTool{
			Descriptor: contract.Descriptor, ContractHash: hash, AcceptedSemanticHashes: acceptedHashes,
		}
	}
	return nil
}

// ObserveNodeHello 处理节点握手后的完整能力快照：首次出现的契约先持久化再公开；
// 已公开契约发生变化时交给 Fleet 合并器重算；Hello 中缺失的已公开工具也要重新核对，
// 这样最后一个 provider 真正移除能力时才会退休工具，而不是永久留下 stale schema。
func (b *PublishedToolBridge) ObserveNodeHello(node Node, hello Hello) {
	helloToolNames := make(map[string]struct{}, len(hello.Tools))
	for _, descriptor := range hello.Tools {
		if isNexusOwnedCanonicalTool(descriptor.Name) || strings.TrimSpace(descriptor.Name) == "" {
			continue
		}
		helloToolNames[descriptor.Name] = struct{}{}
		contractHash, err := ToolContractHash(descriptor)
		if err != nil {
			if b.logger != nil {
				b.logger.Warn("计算 AgentDock 工具契约失败", "node_id", node.ID, "tool", descriptor.Name, "error", err)
			}
			continue
		}

		name := descriptor.Name
		candidate := PublishedTool{
			Descriptor: descriptor, ContractHash: contractHash,
			AcceptedSemanticHashes: []string{contractHash},
		}
		b.mu.Lock()
		published, exists := b.published[name]
		if !exists {
			// 首次出现的契约先持久化再公开，确保 Nexus 重启后仍沿用同一个 schema。
			if err := b.persist(context.Background(), candidate); err != nil {
				b.mu.Unlock()
				if b.logger != nil {
					b.logger.Warn("保存 AgentDock 公开工具契约失败", "node_id", node.ID, "tool", name, "error", err)
				}
				continue
			}
			b.publish(descriptor)
			b.published[name] = candidate
		}
		b.mu.Unlock()
		if exists && (published.ContractHash != contractHash ||
			!containsToolContractHash(published.AcceptedSemanticHashes, contractHash) ||
			!jsonValuesEqual(published.Descriptor.Meta, descriptor.Meta) ||
			!jsonValuesEqual(published.Descriptor.Annotations, descriptor.Annotations)) {
			// schema 不同不等于不兼容；由 Fleet 合并器决定能否安全形成同一代公开契约。
			if err := b.reconcile(name); err != nil && b.logger != nil {
				b.logger.Warn("检查 AgentDock 工具契约兼容性失败", "tool", name, "error", err)
			}
		}
	}

	missingPublished := make([]string, 0)
	for _, name := range b.PublishedNames() {
		if _, present := helloToolNames[name]; !present {
			missingPublished = append(missingPublished, name)
		}
	}
	b.ReconcileNames(missingPublished)
}

// ReconcileNodeTools 在节点启停、删除或能力变化后按其当前工具名单触发 fleet 重算。
func (b *PublishedToolBridge) ReconcileNodeTools(descriptors []ToolDescriptor) {
	b.ReconcileNames(toolDescriptorNames(descriptors))
}

// ReconcileNames 对给定工具名逐个执行 fleet 契约重算；工具名列表来自节点能力快照。
func (b *PublishedToolBridge) ReconcileNames(names []string) {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		// 节点启停或删除后的 fleet 重算也会收到完整 descriptor 名单；Nexus-owned canonical
		// 工具不能在这条旁路中被重新发布为要求 node_id 的节点工具。
		if name == "" || isNexusOwnedCanonicalTool(name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if err := b.reconcile(name); err != nil && b.logger != nil {
			b.logger.Warn("检查 AgentDock 工具契约收敛失败", "tool", name, "error", err)
		}
	}
}

// ReconcilePublished 重算当前所有已公开契约；启动时用于清理旧版本遗留但
// fleet 已不再提供的 stale tool。
func (b *PublishedToolBridge) ReconcilePublished() {
	b.ReconcileNames(b.PublishedNames())
}

// reconcile 重新读取 fleet 中该工具的全部 provider，合并出新一代公开契约；
// 没有任何 provider（含被禁用节点）时才真正下架公开工具。
func (b *PublishedToolBridge) reconcile(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || isNexusOwnedCanonicalTool(name) {
		return nil
	}
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()

	ctx := context.Background()
	nodes, err := b.store.List(ctx)
	if err != nil {
		return err
	}
	descriptors := make([]ToolDescriptor, 0)
	hasKnownProvider := false
	for _, node := range nodes {
		if !containsCapability(node.Capabilities, name) {
			continue
		}
		hasKnownProvider = true
		if !node.Enabled {
			continue
		}
		nodeDescriptors, err := b.store.ToolDescriptors(ctx, node.ID)
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
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, exists := b.published[name]; !exists {
			return nil
		}
		if err := b.store.DeletePublishedToolContract(ctx, name); err != nil {
			return err
		}
		if b.retire != nil {
			b.retire(name)
		}
		delete(b.published, name)
		return nil
	}

	descriptor, acceptedHashes, err := mergeFleetToolDescriptors(descriptors)
	if err != nil {
		if errors.Is(err, errIncompatibleToolContract) {
			return nil
		}
		return err
	}
	contractHash, err := ToolContractHash(descriptor)
	if err != nil {
		return err
	}
	candidate := PublishedTool{
		Descriptor: descriptor, ContractHash: contractHash, AcceptedSemanticHashes: acceptedHashes,
	}

	b.mu.Lock()
	published, exists := b.published[name]
	descriptorChanged := !exists || !reflect.DeepEqual(published.Descriptor, candidate.Descriptor)
	if exists && published.ContractHash == candidate.ContractHash &&
		reflect.DeepEqual(published.AcceptedSemanticHashes, candidate.AcceptedSemanticHashes) && !descriptorChanged {
		b.mu.Unlock()
		return nil
	}
	if err := b.persist(ctx, candidate); err != nil {
		b.mu.Unlock()
		return err
	}
	if b.publish != nil && descriptorChanged {
		b.publish(candidate.Descriptor)
	}
	b.published[name] = candidate
	b.mu.Unlock()
	return nil
}

// ToolContractMismatch 在调用前校验目标节点的现场契约是否属于当前公开契约的兼容集合；
// 未命中时返回结构化差异，供 MCP 层把可操作信息回显给调用方。
func (b *PublishedToolBridge) ToolContractMismatch(ctx context.Context, node Node, toolName string) (*ToolContractMismatch, error) {
	published, ok := b.Published(toolName)
	if !ok {
		return nil, fmt.Errorf("Nexus 公开工具契约不存在: %s", toolName)
	}
	descriptors, err := b.store.ToolDescriptors(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	target, ok := findToolDescriptor(descriptors, toolName)
	if !ok {
		return nil, fmt.Errorf("AgentDock node %s does not provide tool descriptor %s", node.ID, toolName)
	}
	nodeHash, err := ToolContractHash(target)
	if err != nil {
		return nil, err
	}
	if containsToolContractHash(published.AcceptedSemanticHashes, nodeHash) {
		return nil, nil
	}

	return &ToolContractMismatch{
		Code:          "TOOL_CONTRACT_MISMATCH",
		Message:       "目标 AgentDock 的工具契约不在 Nexus 当前已发布的兼容集合中，请刷新 GPT 工具；若仍不一致，请检查相关设备的 AgentDock 版本或工具契约。",
		Tool:          toolName,
		NodeID:        node.ID,
		NodeName:      node.Name,
		NodeVersion:   node.Version,
		PublishedHash: published.ContractHash,
		NodeHash:      nodeHash,
		Differences:   contractDifferences(published.Descriptor, target),
	}, nil
}

func (b *PublishedToolBridge) Published(name string) (PublishedTool, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	tool, ok := b.published[name]
	return tool, ok
}

// PublishedNames 返回当前已公开契约的工具名（升序），供启动核对与展示层使用。
func (b *PublishedToolBridge) PublishedNames() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	names := make([]string, 0, len(b.published))
	for name := range b.published {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// PublishedTools 返回当前全部已公开契约（按工具名升序），供 MCP Apps 开关重建工具注册。
func (b *PublishedToolBridge) PublishedTools() []PublishedTool {
	b.mu.RLock()
	snapshot := make([]PublishedTool, 0, len(b.published))
	for _, tool := range b.published {
		snapshot = append(snapshot, tool)
	}
	b.mu.RUnlock()
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].Descriptor.Name < snapshot[j].Descriptor.Name })
	return snapshot
}

func (b *PublishedToolBridge) persist(ctx context.Context, published PublishedTool) error {
	return b.store.SavePublishedToolContract(ctx, PublishedToolContract{
		ToolName: published.Descriptor.Name, Descriptor: published.Descriptor,
		AcceptedSemanticHashes: published.AcceptedSemanticHashes,
	})
}

// containsCapability 判断节点能力名单是否包含指定工具名。
func containsCapability(capabilities []string, name string) bool {
	for _, capability := range capabilities {
		if capability == name {
			return true
		}
	}
	return false
}
