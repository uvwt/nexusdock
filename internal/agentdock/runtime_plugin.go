package agentdock

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// RuntimePluginSource 是 AgentDock 持久化的 Plugin 来源身份；Nexus 只展示，不解释或改写。
type RuntimePluginSource struct {
	Type             string `json:"type"`
	Ref              string `json:"ref,omitempty"`
	Revision         string `json:"revision,omitempty"`
	Adapter          string `json:"adapter,omitempty"`
	Catalog          string `json:"catalog,omitempty"`
	CatalogItem      string `json:"catalog_item,omitempty"`
	ResolvedType     string `json:"resolved_type,omitempty"`
	ResolvedRef      string `json:"resolved_ref,omitempty"`
	ResolvedRevision string `json:"resolved_revision,omitempty"`
}

type RuntimePluginSummary struct {
	Name          string              `json:"name"`
	Version       string              `json:"version"`
	Enabled       bool                `json:"enabled"`
	PackageDigest string              `json:"package_digest"`
	Source        RuntimePluginSource `json:"source"`
	SkillCount    int                 `json:"skill_count"`
	MCPCount      int                 `json:"mcp_count"`
}

type RuntimePluginSkill struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Path          string `json:"path"`
	ContentDigest string `json:"content_digest"`
}

type RuntimePluginMCP struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Transport   string `json:"transport"`
	URL         string `json:"url,omitempty"`
	Command     string `json:"command,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	RuntimeName string `json:"runtime_name,omitempty"`
	StorageKey  string `json:"storage_key,omitempty"`
}

type RuntimePluginCompatibility struct {
	DetectedFormat string   `json:"detected_format,omitempty"`
	Adapter        string   `json:"adapter,omitempty"`
	Supported      []string `json:"supported,omitempty"`
	Unsupported    []string `json:"unsupported,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
}

type RuntimePluginDetail struct {
	RuntimePluginSummary
	Description   string                     `json:"description,omitempty"`
	InstalledAt   string                     `json:"installed_at"`
	Skills        []RuntimePluginSkill       `json:"skills"`
	MCP           []RuntimePluginMCP         `json:"mcp"`
	Executables   []string                   `json:"executables"`
	Warnings      []string                   `json:"warnings"`
	Unsupported   []string                   `json:"unsupported"`
	Compatibility RuntimePluginCompatibility `json:"compatibility"`
}

// RuntimePlugins 读取 AgentDock 的已安装 Plugin 快照；生命周期真相仍只存在 AgentDock。
func (h *Hub) RuntimePlugins(ctx context.Context, nodeID string) ([]RuntimePluginSummary, error) {
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodGet, "/internal/runtime/plugins", nil, nil)
	if err != nil {
		return nil, err
	}
	return parseRuntimePluginList(nodeID, payload)
}

// RuntimePlugin 读取单个已安装 Plugin 的静态详情与组件清单。
func (h *Hub) RuntimePlugin(ctx context.Context, nodeID, name string) (RuntimePluginDetail, error) {
	payload, err := h.invokeRuntime(ctx, nodeID, http.MethodGet, "/internal/runtime/plugins/"+url.PathEscape(name), nil, nil)
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	return parseRuntimePluginDetail(nodeID, name, payload)
}

func parseRuntimePluginList(node string, payload map[string]any) ([]RuntimePluginSummary, error) {
	p := runtimeParser{node: node, operation: "GET /internal/runtime/plugins"}
	raw, err := p.array("plugins", payload["plugins"], false)
	if err != nil {
		return nil, err
	}
	plugins := make([]RuntimePluginSummary, 0, len(raw))
	for i, item := range raw {
		field := fieldIndex("plugins", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return nil, err
		}
		plugin, err := parseRuntimePluginSummary(p, field, object)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, plugin)
	}
	return plugins, nil
}

func parseRuntimePluginDetail(node, requestedName string, payload map[string]any) (RuntimePluginDetail, error) {
	p := runtimeParser{node: node, operation: "GET /internal/runtime/plugins/" + requestedName}
	object, err := p.object("plugin", payload["plugin"], false)
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	summary, err := parseRuntimePluginSummary(p, "plugin", object)
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	if summary.Name != requestedName {
		return RuntimePluginDetail{}, p.fail("plugin.name", fmt.Sprintf("与请求 Plugin %q 不一致", requestedName))
	}
	description, err := p.optionalString("plugin.description", object["description"])
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	installedAt, err := p.requiredString("plugin.installed_at", object["installed_at"])
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	skills, err := parseRuntimePluginSkills(p, object["skills"])
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	mcp, err := parseRuntimePluginMCP(p, object["mcp"])
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	executables, err := p.optionalStringArray("plugin.executables", object["executables"])
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	warnings, err := p.optionalStringArray("plugin.warnings", object["warnings"])
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	unsupported, err := p.optionalStringArray("plugin.unsupported", object["unsupported"])
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	compatibility, err := parseRuntimePluginCompatibility(p, object["compatibility"])
	if err != nil {
		return RuntimePluginDetail{}, err
	}
	summary.SkillCount = len(skills)
	summary.MCPCount = len(mcp)
	return RuntimePluginDetail{
		RuntimePluginSummary: summary,
		Description:          description, InstalledAt: installedAt, Skills: skills, MCP: mcp,
		Executables: executables, Warnings: warnings, Unsupported: unsupported, Compatibility: compatibility,
	}, nil
}

func parseRuntimePluginSummary(p runtimeParser, field string, object map[string]any) (RuntimePluginSummary, error) {
	name, err := p.requiredString(field+".name", object["name"])
	if err != nil {
		return RuntimePluginSummary{}, err
	}
	version, err := p.requiredString(field+".version", object["version"])
	if err != nil {
		return RuntimePluginSummary{}, err
	}
	enabled, ok := object["enabled"].(bool)
	if !ok {
		return RuntimePluginSummary{}, p.fail(field+".enabled", fmt.Sprintf("应为布尔值，实际为 %T", object["enabled"]))
	}
	digest, err := p.requiredString(field+".package_digest", object["package_digest"])
	if err != nil {
		return RuntimePluginSummary{}, err
	}
	source, err := parseRuntimePluginSource(p, field+".source", object["source"])
	if err != nil {
		return RuntimePluginSummary{}, err
	}
	skillCount, err := p.optionalInt(field+".skill_count", object["skill_count"])
	if err != nil {
		return RuntimePluginSummary{}, err
	}
	mcpCount, err := p.optionalInt(field+".mcp_count", object["mcp_count"])
	if err != nil {
		return RuntimePluginSummary{}, err
	}
	return RuntimePluginSummary{
		Name: name, Version: version, Enabled: enabled, PackageDigest: digest, Source: source,
		SkillCount: skillCount, MCPCount: mcpCount,
	}, nil
}

func parseRuntimePluginSource(p runtimeParser, field string, value any) (RuntimePluginSource, error) {
	object, err := p.object(field, value, false)
	if err != nil {
		return RuntimePluginSource{}, err
	}
	source := RuntimePluginSource{}
	for key, target := range map[string]*string{
		"type": &source.Type, "ref": &source.Ref, "revision": &source.Revision, "adapter": &source.Adapter,
		"catalog": &source.Catalog, "catalog_item": &source.CatalogItem, "resolved_type": &source.ResolvedType,
		"resolved_ref": &source.ResolvedRef, "resolved_revision": &source.ResolvedRevision,
	} {
		var text string
		if key == "type" {
			text, err = p.requiredString(field+"."+key, object[key])
		} else {
			text, err = p.optionalString(field+"."+key, object[key])
		}
		if err != nil {
			return RuntimePluginSource{}, err
		}
		*target = text
	}
	return source, nil
}

func parseRuntimePluginSkills(p runtimeParser, value any) ([]RuntimePluginSkill, error) {
	raw, err := p.array("plugin.skills", value, true)
	if err != nil {
		return nil, err
	}
	items := make([]RuntimePluginSkill, 0, len(raw))
	for i, item := range raw {
		field := fieldIndex("plugin.skills", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return nil, err
		}
		name, err := p.requiredString(field+".name", object["name"])
		if err != nil {
			return nil, err
		}
		description, err := p.optionalString(field+".description", object["description"])
		if err != nil {
			return nil, err
		}
		path, err := p.requiredString(field+".path", object["path"])
		if err != nil {
			return nil, err
		}
		digest, err := p.requiredString(field+".content_digest", object["content_digest"])
		if err != nil {
			return nil, err
		}
		items = append(items, RuntimePluginSkill{Name: name, Description: description, Path: path, ContentDigest: digest})
	}
	return items, nil
}

func parseRuntimePluginMCP(p runtimeParser, value any) ([]RuntimePluginMCP, error) {
	raw, err := p.array("plugin.mcp", value, true)
	if err != nil {
		return nil, err
	}
	items := make([]RuntimePluginMCP, 0, len(raw))
	for i, item := range raw {
		field := fieldIndex("plugin.mcp", i)
		object, err := p.object(field, item, false)
		if err != nil {
			return nil, err
		}
		name, err := p.requiredString(field+".name", object["name"])
		if err != nil {
			return nil, err
		}
		transport, err := p.requiredString(field+".transport", object["transport"])
		if err != nil {
			return nil, err
		}
		entry := RuntimePluginMCP{Name: name, Transport: transport}
		for key, target := range map[string]*string{
			"description": &entry.Description, "url": &entry.URL, "command": &entry.Command,
			"cwd": &entry.Cwd, "runtime_name": &entry.RuntimeName, "storage_key": &entry.StorageKey,
		} {
			*target, err = p.optionalString(field+"."+key, object[key])
			if err != nil {
				return nil, err
			}
		}
		items = append(items, entry)
	}
	return items, nil
}

func parseRuntimePluginCompatibility(p runtimeParser, value any) (RuntimePluginCompatibility, error) {
	object, err := p.object("plugin.compatibility", value, true)
	if err != nil || object == nil {
		return RuntimePluginCompatibility{}, err
	}
	result := RuntimePluginCompatibility{}
	for key, target := range map[string]*string{
		"detected_format": &result.DetectedFormat, "adapter": &result.Adapter,
	} {
		*target, err = p.optionalString("plugin.compatibility."+key, object[key])
		if err != nil {
			return RuntimePluginCompatibility{}, err
		}
	}
	if result.Supported, err = p.optionalStringArray("plugin.compatibility.supported", object["supported"]); err != nil {
		return RuntimePluginCompatibility{}, err
	}
	if result.Unsupported, err = p.optionalStringArray("plugin.compatibility.unsupported", object["unsupported"]); err != nil {
		return RuntimePluginCompatibility{}, err
	}
	if result.Warnings, err = p.optionalStringArray("plugin.compatibility.warnings", object["warnings"]); err != nil {
		return RuntimePluginCompatibility{}, err
	}
	return result, nil
}
