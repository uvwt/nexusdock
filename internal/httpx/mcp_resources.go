package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
)

func (s *Server) syncMCPAppResources() {
	if s == nil || s.mcpServer == nil {
		return
	}

	desired, err := s.publishedMCPAppResourceURIs(context.Background())
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("同步 AgentDock MCP App resource 目录失败", "error", err)
		}
		return
	}
	s.mcpResourcesMu.Lock()
	defer s.mcpResourcesMu.Unlock()
	if s.mcpResources == nil {
		s.mcpResources = make(map[string]struct{})
	}

	for uri := range s.mcpResources {
		if _, ok := desired[uri]; ok {
			continue
		}
		s.mcpServer.RemoveResources(uri)
		delete(s.mcpResources, uri)
	}
	for _, uri := range sortedMCPAppResourceURIs(desired) {
		if _, ok := s.mcpResources[uri]; ok {
			continue
		}
		uri := uri
		s.mcpServer.AddResource(&mcpsdk.Resource{
			URI:         uri,
			Name:        mcpAppResourceName(uri),
			Title:       "AgentDock MCP App",
			Description: "MCP App resource relayed from a compatible AgentDock node.",
			MIMEType:    protocol.MCPAppMIMEType,
			Meta:        nexusMCPAppResourceMeta(s.cfg.PublicURL),
		}, func(ctx context.Context, request *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
			if request == nil || request.Params == nil || request.Params.URI != uri {
				return nil, mcpsdk.ResourceNotFoundError(uri)
			}
			return s.readPublishedMCPAppResource(ctx, uri)
		})
		s.mcpResources[uri] = struct{}{}
	}
}

func (s *Server) publishedMCPAppResourceURIs(ctx context.Context) (map[string]struct{}, error) {
	uris := make(map[string]struct{})
	if s == nil || s.agentDock == nil || !s.mcpAppsEnabled() {
		return uris, nil
	}
	nodes, err := s.agentDock.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		if !node.Enabled {
			continue
		}
		resources, err := s.agentDock.UIResources(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		for _, resource := range resources {
			expectedContract, known := protocol.UIResourceContract(resource.URI)
			if !known || resource.Contract != expectedContract || resource.MIMEType != protocol.MCPAppMIMEType {
				continue
			}
			uris[resource.URI] = struct{}{}
		}
	}
	return uris, nil
}

func (s *Server) readPublishedMCPAppResource(ctx context.Context, uri string) (*mcpsdk.ReadResourceResult, error) {
	if s.agentDock == nil || s.agentDockHub == nil || !s.mcpAppsEnabled() {
		return nil, mcpsdk.ResourceNotFoundError(uri)
	}
	nodes, err := s.agentDock.List(ctx)
	if err != nil {
		return nil, err
	}
	return s.readMCPAppResourceWithTimeout(ctx, nodes, uri, agentDockNodeInvokeTimeout)
}

func (s *Server) readMCPAppResourceWithTimeout(ctx context.Context, nodes []agentdock.Node, uri string, leafTimeout time.Duration) (*mcpsdk.ReadResourceResult, error) {
	expectedContract, known := protocol.UIResourceContract(uri)
	if !known {
		return nil, mcpsdk.ResourceNotFoundError(uri)
	}
	foundCompatibleProvider := false
	var lastErr error
	for _, node := range nodes {
		if !node.Enabled {
			continue
		}
		resources, resourceErr := s.agentDock.UIResources(ctx, node.ID)
		if resourceErr != nil {
			lastErr = resourceErr
			continue
		}
		if !nodeProvidesUIResource(resources, uri, expectedContract) {
			continue
		}
		foundCompatibleProvider = true
		if !s.agentDockHub.Online(node.ID) {
			continue
		}

		leafCtx, cancel := context.WithTimeout(ctx, leafTimeout)
		result, invokeErr := s.agentDockHub.Invoke(leafCtx, node.ID, protocol.OperationResourceRead, map[string]any{"uri": uri})
		cancel()
		if invokeErr != nil {
			if errors.Is(invokeErr, context.DeadlineExceeded) {
				lastErr = errors.New("resource timeout")
			} else {
				lastErr = invokeErr
			}
			continue
		}
		read, decodeErr := decodeNodeMCPAppResource(uri, result, s.cfg.PublicURL)
		if decodeErr != nil {
			lastErr = decodeErr
			continue
		}
		return read, nil
	}
	if !foundCompatibleProvider {
		return nil, fmt.Errorf("MCP App resource %s 没有兼容 contract %s 的 AgentDock provider", uri, expectedContract)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("读取 MCP App resource %s: %w", uri, lastErr)
	}
	return nil, fmt.Errorf("MCP App resource %s 当前没有在线兼容 AgentDock provider", uri)
}

func nodeProvidesUIResource(resources []agentdock.UIResourceCapability, uri, expectedContract string) bool {
	for _, resource := range resources {
		if resource.URI == uri && resource.Contract == expectedContract && resource.MIMEType == protocol.MCPAppMIMEType {
			return true
		}
	}
	return false
}

func decodeNodeMCPAppResource(uri string, result map[string]any, publicURL string) (*mcpsdk.ReadResourceResult, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("编码节点 MCP App resource: %w", err)
	}
	var read mcpsdk.ReadResourceResult
	if err := json.Unmarshal(encoded, &read); err != nil {
		return nil, fmt.Errorf("解析节点 MCP App resource: %w", err)
	}
	if len(read.Contents) == 0 {
		return nil, fmt.Errorf("节点 MCP App resource %s 内容为空", uri)
	}
	for _, content := range read.Contents {
		if content == nil || content.URI != uri || content.MIMEType != protocol.MCPAppMIMEType || content.Text == "" {
			return nil, fmt.Errorf("节点 MCP App resource %s 返回了无效内容", uri)
		}
		// Resource 由 Nexus 对外提供，不能沿用节点域；组件必须使用 Nexus 自己的唯一公网 origin。
		content.Meta = nexusMCPAppResourceMeta(publicURL)
	}
	return &read, nil
}

// toolBoundUIResourceURI reads only presentation binding from a published tool descriptor.
// Provider capability is never inferred here; resource.read routing uses persisted ui_resources instead.
func toolBoundUIResourceURI(descriptor agentdock.ToolDescriptor) string {
	ui, ok := descriptor.Meta["ui"].(map[string]any)
	if !ok {
		return ""
	}
	uri, _ := ui["resourceUri"].(string)
	uri = strings.TrimSpace(uri)
	if _, known := protocol.UIResourceContract(uri); !known {
		return ""
	}
	return uri
}

func mcpAppResourceName(uri string) string {
	name := strings.TrimPrefix(uri, protocol.AgentDockUIResourcePrefix)
	name = strings.Trim(strings.ReplaceAll(name, "/", "-"), "-")
	if name == "" {
		return "agentdock-mcp-app"
	}
	return "agentdock-" + name
}

func nexusMCPAppResourceMeta(publicURL string) mcpsdk.Meta {
	ui := map[string]any{
		"csp": map[string]any{
			"connectDomains":  []string{},
			"resourceDomains": []string{},
		},
		"prefersBorder": true,
	}
	if domain := strings.TrimRight(strings.TrimSpace(publicURL), "/"); domain != "" {
		ui["domain"] = domain
	}
	return mcpsdk.Meta{"ui": ui}
}

func sortedMCPAppResourceURIs(resources map[string]struct{}) []string {
	uris := make([]string, 0, len(resources))
	for uri := range resources {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	return uris
}
