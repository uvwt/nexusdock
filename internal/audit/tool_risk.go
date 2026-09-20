package audit

import (
	"sort"
	"strings"
)

func ClassifyToolRisk(tool string, arguments map[string]any) string {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "file_edit", "exec_command", "mcp_tool_call", "mcp_manage":
		return "high"
	case "file_publish", "task_manage", "workflow_template_manage":
		return "medium"
	default:
		return "low"
	}
}

func ToolMetadata(nodeID, workspaceID, tool string, arguments map[string]any, durationMS int64) map[string]any {
	metadata := map[string]any{
		"node_id":       strings.TrimSpace(nodeID),
		"tool":          strings.TrimSpace(tool),
		"duration_ms":   durationMS,
		"argument_keys": sortedKeys(arguments),
	}
	if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
		metadata["workspace_id"] = workspaceID
	}
	if taskID := safeString(arguments, "task_id"); taskID != "" {
		metadata["task_id"] = taskID
	}
	if path := safeString(arguments, "path"); path != "" {
		metadata["path"] = path
	}
	switch tool {
	case "mcp_tool_call", "mcp_tool_inspect":
		if qualified := safeString(arguments, "name"); qualified != "" {
			metadata["qualified_tool"] = qualified
			if index := strings.IndexByte(qualified, ':'); index > 0 {
				metadata["mcp_server"] = qualified[:index]
			}
		}
	case "mcp_manage":
		if server := safeString(arguments, "name"); server != "" {
			metadata["mcp_server"] = server
		}
		if action := safeString(arguments, "action"); action != "" {
			metadata["action"] = action
		}
	case "task_manage":
		if action := safeString(arguments, "action"); action != "" {
			metadata["action"] = action
		}
	}
	return metadata
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		switch key {
		case "password", "secret", "token", "api_key", "content", "patch":
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func safeString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
