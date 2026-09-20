package httpx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/capability"
	"github.com/uvwt/nexusdock/internal/workspace"
)

func (s *Server) enforceWorkspaceRouteAuthority(ctx context.Context, node agentdock.Node, item workspace.Workspace, tool string, arguments map[string]any) error {
	if strings.TrimSpace(item.RouteAuthority) == "" || tool != "mcp_tool_call" {
		return nil
	}
	candidates := workspace.RouteCandidates(arguments)
	if len(candidates) == 0 {
		return nil
	}
	authorityReadTool := "read_file"
	if s.capabilities != nil {
		resolution, resolveErr := s.capabilities.Resolve(
			capability.FilesystemRead,
			agentdock.NewCompatibility(node, nil),
			true,
		)
		if resolveErr != nil {
			return workspace.PolicyError{
				Boundary: "route_authority",
				Resource: item.RouteAuthority,
				Reason:   "bound AgentDock node has no workspace-safe filesystem.read capability: " + resolveErr.Error(),
			}
		}
		authorityReadTool = resolution.Tool
	} else if !containsString(node.Capabilities, authorityReadTool) {
		return workspace.PolicyError{
			Boundary: "route_authority",
			Resource: item.RouteAuthority,
			Reason:   "bound AgentDock node cannot read the configured route authority",
		}
	}
	authorityPath, ok := workspace.RouteAuthorityPath(item, node.OS)
	if !ok {
		return workspace.PolicyError{
			Boundary: "route_authority",
			Resource: item.RouteAuthority,
			Reason:   "configured route authority path is invalid or escapes the workspace",
		}
	}
	result, err := s.agentDockHub.Invoke(ctx, node.ID, protocol.OperationToolCall, map[string]any{
		"tool":      authorityReadTool,
		"arguments": map[string]any{"path": authorityPath},
	})
	if err != nil {
		return workspace.PolicyError{
			Boundary: "route_authority",
			Resource: authorityPath,
			Reason:   "authority file could not be read: " + err.Error(),
		}
	}
	content, err := routeAuthorityContent(result)
	if err != nil {
		return workspace.PolicyError{
			Boundary: "route_authority",
			Resource: authorityPath,
			Reason:   err.Error(),
		}
	}
	authority := workspace.ParseRouteAuthority(content, item.Domain)
	for _, candidate := range candidates {
		if !authority.Allows(candidate) {
			return workspace.PolicyError{
				Boundary: "route_authority",
				Resource: candidate,
				Reason:   "route is not present in " + item.RouteAuthority,
			}
		}
	}
	return nil
}

func routeAuthorityContent(result map[string]any) (string, error) {
	if result == nil {
		return "", errors.New("authority read returned an empty result")
	}
	if isError, _ := result["isError"].(bool); isError {
		return "", fmt.Errorf("authority read failed: %s", toolResultErrorText(result))
	}
	if structured, ok := result["structuredContent"].(map[string]any); ok {
		for _, key := range []string{"content", "text", "data"} {
			if value, _ := structured[key].(string); strings.TrimSpace(value) != "" {
				return value, nil
			}
		}
	}
	if value, _ := result["content"].(string); strings.TrimSpace(value) != "" {
		return value, nil
	}
	if blocks, ok := result["content"].([]any); ok {
		var builder strings.Builder
		for _, block := range blocks {
			item, ok := block.(map[string]any)
			if !ok {
				continue
			}
			text, _ := item["text"].(string)
			if text == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(text)
		}
		if builder.Len() > 0 {
			return builder.String(), nil
		}
	}
	if blocks, ok := result["content"].([]map[string]any); ok {
		var builder strings.Builder
		for _, block := range blocks {
			text, _ := block["text"].(string)
			if text == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(text)
		}
		if builder.Len() > 0 {
			return builder.String(), nil
		}
	}
	return "", errors.New("authority read result does not contain text content")
}

func toolResultErrorText(result map[string]any) string {
	if structured, ok := result["structuredContent"].(map[string]any); ok {
		for _, key := range []string{"error", "message"} {
			if value, _ := structured[key].(string); strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	if value, _ := result["error"].(string); strings.TrimSpace(value) != "" {
		return value
	}
	return "unknown AgentDock read error"
}
