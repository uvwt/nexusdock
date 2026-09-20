package httpx

import (
	"reflect"
	"testing"
)

func TestNodeToolResourceKeysScopesWriteResources(t *testing.T) {
	got := nodeToolResourceKeys("node_1", "site", "file_edit", map[string]any{
		"action":   "move",
		"path":     `D:\website\ServoCylMotion\a.html`,
		"new_path": `D:\website\ServoCylMotion\b.html`,
	})
	want := []string{
		"node_1:ws:site:file:d:/website/servocylmotion/a.html",
		"node_1:ws:site:file:d:/website/servocylmotion/b.html",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys=%#v want=%#v", got, want)
	}
}

func TestNodeToolResourceKeysExtractsDynamicMCPRouteAndID(t *testing.T) {
	got := nodeToolResourceKeys("node_1", "site", "mcp_tool_call", map[string]any{
		"name": "elementor-site:update_page",
		"arguments": map[string]any{
			"page_url": "https://example.com/products/demo/",
			"page_id":  42,
		},
	})
	want := []string{
		"node_1:ws:site:remote-id:elementor-site:update_page:42",
		"node_1:ws:site:route:https://example.com/products/demo",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys=%#v want=%#v", got, want)
	}
}
