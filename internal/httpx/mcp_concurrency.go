package httpx

import (
	"fmt"
	"sort"
	"strings"

	"github.com/uvwt/nexusdock/internal/workspace"
)

func nodeToolResourceKeys(nodeID, workspaceID, tool string, arguments map[string]any) []string {
	prefix := strings.TrimSpace(nodeID)
	if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
		prefix += ":ws:" + workspaceID
	}
	keys := make([]string, 0, 4)

	add := func(kind, value string) {
		value = normalizeResourceValue(value)
		if value == "" {
			return
		}
		keys = append(keys, prefix+":"+kind+":"+value)
	}

	switch tool {
	case "file_edit":
		action := stringArgument(arguments, "action")
		if action == "patch" {
			add("file", stringArgument(arguments, "workdir"))
		} else {
			add("file", stringArgument(arguments, "path"))
			add("file", stringArgument(arguments, "new_path"))
		}
	case "file_publish":
		add("file", stringArgument(arguments, "path"))
	case "mcp_manage":
		add("mcp", stringArgument(arguments, "name"))
	case "task_manage":
		add("task", stringArgument(arguments, "task_id"))
	case "workflow_template_manage":
		add("workflow", stringArgument(arguments, "template_id"))
		add("workflow", stringArgument(arguments, "id"))
	case "mcp_tool_call":
		qualified := stringArgument(arguments, "name")
		specificBefore := len(keys)
		if nested, ok := arguments["arguments"].(map[string]any); ok {
			for _, route := range workspace.RouteCandidates(nested) {
				add("route", route)
			}
			for _, idKey := range []string{"page_id", "post_id", "resource_id", "id"} {
				if value := fmt.Sprint(nested[idKey]); value != "<nil>" && value != "" {
					add("remote-id", qualified+":"+value)
				}
			}
		}
		if len(keys) == specificBefore && qualified != "" {
			add("mcp-tool", qualified)
		}
	}
	sort.Strings(keys)
	if len(keys) < 2 {
		return keys
	}
	out := keys[:1]
	for _, key := range keys[1:] {
		if key != out[len(out)-1] {
			out = append(out, key)
		}
	}
	return out
}

func normalizeResourceValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.ToLower(strings.TrimRight(value, "/"))
	if value == "" {
		return "/"
	}
	return value
}
