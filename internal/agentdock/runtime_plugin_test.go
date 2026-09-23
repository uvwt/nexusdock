package agentdock

import (
	"errors"
	"testing"
)

func TestParseRuntimePluginListUsesPortableProvenance(t *testing.T) {
	payload := map[string]any{
		"plugins": []any{
			map[string]any{
				"name": "cloudflare", "version": "0.1.2", "enabled": true,
				"package_digest": "sha256:abc", "skill_count": float64(9), "mcp_count": float64(1),
				"provenance": map[string]any{
					"origin": "https://github.com/openai/plugins", "revision": "deadbeef",
					"subdir": "plugins/cloudflare", "format": "openai", "adapted": true,
				},
			},
			map[string]any{
				"name": "local-demo", "version": "local", "enabled": false,
				"package_digest": "sha256:def", "skill_count": float64(1), "mcp_count": float64(0),
			},
		},
	}
	plugins, err := parseRuntimePluginList("mini", payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 {
		t.Fatalf("plugin count = %d, want 2", len(plugins))
	}
	imported := plugins[0]
	if imported.Name != "cloudflare" || imported.Version != "0.1.2" || !imported.Enabled {
		t.Fatalf("imported plugin summary = %#v", imported)
	}
	if imported.SkillCount != 9 || imported.MCPCount != 1 {
		t.Fatalf("component counts = skills %d mcp %d", imported.SkillCount, imported.MCPCount)
	}
	if imported.Provenance == nil ||
		imported.Provenance.Origin != "https://github.com/openai/plugins" ||
		imported.Provenance.Subdir != "plugins/cloudflare" ||
		imported.Provenance.Format != "openai" ||
		!imported.Provenance.Adapted {
		t.Fatalf("plugin provenance = %#v", imported.Provenance)
	}
	if plugins[1].Provenance != nil {
		t.Fatalf("local Portable Plugin unexpectedly has provenance: %#v", plugins[1].Provenance)
	}
}

func TestParseRuntimePluginDetailDerivesComponentCounts(t *testing.T) {
	payload := map[string]any{
		"plugin": map[string]any{
			"name": "cloudflare", "version": "0.1.2", "description": "Cloudflare tools", "enabled": true,
			"package_digest": "sha256:abc", "installed_at": "2026-09-23T12:00:00Z",
			"provenance": map[string]any{
				"origin": "https://github.com/openai/plugins", "revision": "deadbeef",
				"subdir": "plugins/cloudflare", "format": "openai", "adapted": true,
			},
			"skills": []any{
				map[string]any{
					"name": "cloudflare", "description": "Manage Cloudflare",
					"path": "skills/cloudflare", "content_digest": "sha256:skill",
				},
			},
			"mcp": []any{
				map[string]any{
					"name": "cloudflare-api", "transport": "streamable-http",
					"url": "https://mcp.cloudflare.com/mcp", "runtime_name": "plugin.cloudflare.cloudflare-api",
				},
			},
			"executables": []any{},
			"warnings":    []any{},
			"unsupported": []any{},
			"compatibility": map[string]any{
				"format": "portable", "supported": []any{"skills", "mcp"},
				"unsupported": []any{}, "warnings": []any{},
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
	if len(detail.MCP) != 1 || detail.MCP[0].RuntimeName != "plugin.cloudflare.cloudflare-api" {
		t.Fatalf("mcp = %#v", detail.MCP)
	}
	if detail.Compatibility.Format != "portable" {
		t.Fatalf("compatibility = %#v", detail.Compatibility)
	}
	if detail.Provenance == nil || detail.Provenance.Format != "openai" {
		t.Fatalf("provenance = %#v", detail.Provenance)
	}
}

func TestParseRuntimePluginListRejectsMalformedProvenance(t *testing.T) {
	payload := map[string]any{
		"plugins": []any{
			map[string]any{
				"name": "demo", "version": "1.0.0", "enabled": true,
				"package_digest": "sha256:abc", "provenance": map[string]any{"format": "openai"},
			},
		},
	}
	_, err := parseRuntimePluginList("mini", payload)
	var contractErr *ContractError
	if !errors.As(err, &contractErr) {
		t.Fatalf("error = %#v, want ContractError", err)
	}
	if contractErr.Field != "plugins[0].provenance.origin" {
		t.Fatalf("contract field = %q", contractErr.Field)
	}
}
