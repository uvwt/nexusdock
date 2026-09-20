package httpx

import (
	"net/http"
	"time"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/capability"
)

func (s *Server) runtimeNodeStatus(w http.ResponseWriter, r *http.Request) {
	if s.agentDock == nil {
		writeError(w, http.StatusServiceUnavailable, "AGENTDOCK_NODE_STORE_UNAVAILABLE", "AgentDock 节点存储不可用")
		return
	}
	node, err := s.agentDock.Get(r.Context(), r.PathValue("nodeID"))
	if err != nil {
		writeAgentDockNodeError(w, err)
		return
	}
	online := s.agentDockHub != nil && s.agentDockHub.Online(node.ID)
	node.Online = online
	compatibility, err := s.agentDock.Compatibility(r.Context(), node.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "AGENTDOCK_NODE_LOOKUP_FAILED", "无法读取 AgentDock 节点兼容状态")
		return
	}
	health := agentdock.EvaluateHealth(node, online, compatibility, time.Now().UTC())

	logical := make([]map[string]any, 0)
	workspaceSafe := make([]map[string]any, 0)
	if s.capabilities != nil {
		logical = capabilityResolutionViews(s.capabilities.Available(compatibility, false))
		workspaceSafe = capabilityResolutionViews(s.capabilities.Available(compatibility, true))
	}

	concurrency := map[string]any{
		"global": 0, "per_node": 0, "per_workspace": 0, "queue_timeout_seconds": 0,
	}
	if s.toolGate != nil {
		limits := s.toolGate.Limits()
		concurrency = map[string]any{
			"global": limits.Global, "per_node": limits.PerNode, "per_workspace": limits.PerWorkspace,
			"queue_timeout_seconds": int(limits.QueueTimeout / time.Second),
		}
	}
	payload := map[string]any{
		"ok":   true,
		"node": node,
		"health": map[string]any{
			"state": health.State, "online": health.Online, "enabled": health.Enabled,
			"compatible": health.Compatible, "last_seen_at": health.LastSeenAt,
			"last_seen_age_seconds": health.LastSeenAgeSeconds,
		},
		"compatibility":          compatibility,
		"logical_capabilities":   logical,
		"workspace_capabilities": workspaceSafe,
		"concurrency":            concurrency,
		"routing": map[string]any{
			"explicit_node_required":   true,
			"automatic_node_selection": false,
		},
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	writeJSON(w, http.StatusOK, payload)
}

func capabilityResolutionViews(values []capability.Resolution) []map[string]any {
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		result = append(result, map[string]any{
			"capability":     string(value.Capability),
			"tool":           value.Tool,
			"workspace_safe": value.WorkspaceSafe,
		})
	}
	return result
}
