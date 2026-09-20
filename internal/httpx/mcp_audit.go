package httpx

import (
	"context"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/nexusdock/internal/audit"
	"github.com/uvwt/nexusdock/internal/core"
)

type mcpActorContextKey struct{}

func withMCPActorContext(ctx context.Context, actor core.Actor) context.Context {
	if !actor.Valid() {
		return ctx
	}
	return context.WithValue(ctx, mcpActorContextKey{}, actor)
}

func auditActorFromContext(ctx context.Context) core.Actor {
	if actor, ok := ctx.Value(mcpActorContextKey{}).(core.Actor); ok && actor.Valid() {
		return actor
	}
	if session, ok := webSessionFromContext(ctx); ok && strings.TrimSpace(session.UserID) != "" {
		return core.Actor{Type: core.ActorUser, ID: session.UserID}
	}
	return core.Actor{Type: core.ActorSystem, ID: "mcp_gateway"}
}

func (s *Server) beginNodeToolAudit(ctx context.Context, nodeID, workspaceID, tool string, arguments map[string]any) func(*mcpsdk.CallToolResult, error) {
	startedAt := time.Now()
	return func(response *mcpsdk.CallToolResult, callErr error) {
		s.recordNodeToolAudit(ctx, nodeID, workspaceID, tool, arguments, response, callErr, startedAt)
	}
}

func (s *Server) recordNodeToolAudit(ctx context.Context, nodeID, workspaceID, tool string, arguments map[string]any, response *mcpsdk.CallToolResult, callErr error, startedAt time.Time) {
	if s == nil || s.auditService == nil {
		return
	}
	result := "succeeded"
	if callErr != nil || response == nil || response.IsError {
		result = "failed"
	}
	duration := time.Since(startedAt).Milliseconds()
	objectID := strings.TrimSpace(nodeID)
	if objectID == "" {
		objectID = "unresolved"
	}
	objectID += ":" + strings.TrimSpace(tool)
	_, _ = s.auditService.Record(ctx, audit.Event{
		Actor:      auditActorFromContext(ctx),
		Action:     "agentdock.tool.invoke",
		ObjectType: "agentdock_tool",
		ObjectID:   objectID,
		Result:     result,
		Risk:       audit.ClassifyToolRisk(tool, arguments),
		Approval:   "not_required",
		RequestID:  requestIDFromContext(ctx),
		Metadata:   audit.ToolMetadata(nodeID, workspaceID, tool, arguments, duration),
	})
}
