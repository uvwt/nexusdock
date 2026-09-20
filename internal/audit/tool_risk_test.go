package audit

import (
	"reflect"
	"testing"
)

func TestClassifyToolRisk(t *testing.T) {
	tests := map[string]string{
		"read_file":     "low",
		"search_text":   "low",
		"task_manage":   "medium",
		"file_edit":     "high",
		"exec_command":  "high",
		"mcp_tool_call": "high",
	}
	for tool, want := range tests {
		if got := ClassifyToolRisk(tool, nil); got != want {
			t.Fatalf("%s risk=%s want=%s", tool, got, want)
		}
	}
}

func TestToolMetadataDoesNotPersistSensitiveValues(t *testing.T) {
	metadata := ToolMetadata("node_1", "site", "mcp_tool_call", map[string]any{
		"name":     "elementor-site:update_page",
		"content":  "SECRET PAGE BODY",
		"password": "secret",
		"token":    "secret-token",
		"page_url": "https://example.com/page",
	}, 17)
	if metadata["mcp_server"] != "elementor-site" {
		t.Fatalf("metadata=%#v", metadata)
	}
	keys, _ := metadata["argument_keys"].([]string)
	if !reflect.DeepEqual(keys, []string{"name", "page_url"}) {
		t.Fatalf("argument keys=%#v", keys)
	}
	for _, forbidden := range []string{"SECRET PAGE BODY", "secret", "secret-token"} {
		for _, value := range metadata {
			if value == forbidden {
				t.Fatalf("sensitive value leaked: %q in %#v", forbidden, metadata)
			}
		}
	}
}
