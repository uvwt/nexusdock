package agentdock

import (
	"errors"
	"testing"
)

func TestParseRuntimePluginList(t *testing.T) {
	payload := map[string]any{
		"plugins": []any{
			map[string]any{
				"name": "cloudflare", "version": "1.2.3", "enabled": true,
				"package_digest": "sha256:abc", "skill_count": float64(2), "mcp_count": float64(1),
				"source": map[string]any{
					"type": "git", "ref": "https://github.com/openai/plugins",
					"revision": "deadbeef", "adapter": "openai",
				},
			},
		},
	}
	plugins, err := parseRuntimePluginList("mini", payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 {
		t.Fatalf("plugin count = %d, want 1", len(plugins))
	}
	got := plugins[0]
	if got.Name != "cloudflare" || got.Version != "1.2.3" || !got.Enabled {
		t.Fatalf("plugin summary = %#v", got)
	}
	if got.SkillCount != 2 || got.MCPCount != 1 {
		t.Fatalf("component counts = skills %d mcp %d", got.SkillCount, got.MCPCount)
	}
	if got.Source.Type != "git" || got.Source.Adapter != "openai" {
		t.Fatalf("plugin source = %#v", got.Source)
	}
}

func TestParseRuntimePluginDetailDerivesComponentCounts(t *testing.T) {
	payload := map[string]any{
		"plugin": map[string]any{
			"name": "cloudflare", "version": "1.2.3", "description": "Cloudflare tools", "enabled": true,
			"package_digest": "sha256:abc", "installed_at": "2026-09-23T12:00:00Z",
			"source": map[string]any{
				"type": "git", "ref": "https://github.com/openai/plugins",
				"resolved_type": "git", "resolved_ref": "https://github.com/openai/plugins",
			},
			"skills": []any{
				map[string]any{
					"name": "cloudflare", "description": "Manage Cloudflare",
					"path": "skills/cloudflare", "content_digest": "sha256:skill",
				},
			},
			"mcp": []any{
				map[string]any{
					"name": "cloudflare-mcp", "transport": "stdio",
					"command": "node", "runtime_name": "plugin.cloudflare.cloudflare-mcp",
				},
			},
			"executables": []any{"bin/cloudflare"},
			"warnings":    []any{"review environment bindings"},
			"unsupported": []any{},
			"compatibility": map[string]any{
				"detected_format": "openai", "adapter": "openai",
				"supported": []any{"skills", "mcp"}, "unsupported": []any{}, "warnings": []any{},
			},
		},
	}
	detail, err := parseRuntimePluginDetail("mini", "cloudflare", payload)
	if err != nil {
		t.Fatal(err)
	}
	if detail.SkillCount != 1 || detail.MCPCount != 1 {
		t.Fatalf("derived component counts = skills %d mcp %d", detail.SkillCount, detail.MCPCount)
	}
	if len(detail.Skills) != 1 || detail.Skills[0].Name != "cloudflare" {
		t.Fatalf("skills = %#v", detail.Skills)
	}
	if len(detail.MCP) != 1 || detail.MCP[0].RuntimeName != "plugin.cloudflare.cloudflare-mcp" {
		t.Fatalf("mcp = %#v", detail.MCP)
	}
	if detail.Compatibility.DetectedFormat != "openai" || detail.Compatibility.Adapter != "openai" {
		t.Fatalf("compatibility = %#v", detail.Compatibility)
	}
}

func TestParseRuntimePluginListRejectsMissingSourceType(t *testing.T) {
	payload := map[string]any{
		"plugins": []any{
			map[string]any{
				"name": "demo", "version": "1.0.0", "enabled": true,
				"package_digest": "sha256:abc", "source": map[string]any{},
			},
		},
	}
	_, err := parseRuntimePluginList("mini", payload)
	var contractErr *ContractError
	if !errors.As(err, &contractErr) {
		t.Fatalf("error = %#v, want ContractError", err)
	}
	if contractErr.Field != "plugins[0].source.type" {
		t.Fatalf("contract field = %q", contractErr.Field)
	}
}
