package workspace

import (
	"fmt"
	"net/url"
	pathpkg "path"
	"regexp"
	"strings"
)

type PolicyError struct {
	Boundary string
	Resource string
	Reason   string
}

func (e PolicyError) Error() string {
	if e.Resource == "" {
		return fmt.Sprintf("Workspace %s policy denied: %s", e.Boundary, e.Reason)
	}
	return fmt.Sprintf("Workspace %s policy denied %q: %s", e.Boundary, e.Resource, e.Reason)
}

var windowsDrivePattern = regexp.MustCompile(`^[A-Za-z]:/`)

func Enforce(item Workspace, nodeOS, tool string, arguments map[string]any) error {
	if strings.TrimSpace(item.ID) == "" {
		return PolicyError{Boundary: "workspace", Reason: "workspace id is empty"}
	}
	if err := enforceMCP(item, tool, arguments); err != nil {
		return err
	}
	if err := enforceFilesystem(item, nodeOS, tool, arguments); err != nil {
		return err
	}
	if err := enforceDomain(item, arguments); err != nil {
		return err
	}
	return nil
}

func enforceMCP(item Workspace, tool string, arguments map[string]any) error {
	var server string
	switch tool {
	case "mcp_tool_call", "mcp_tool_inspect":
		name, _ := arguments["name"].(string)
		server = qualifiedServer(name)
		if server == "" {
			return PolicyError{Boundary: "mcp", Resource: name, Reason: "qualified MCP tool name is required"}
		}
	case "mcp_tool_search":
		server, _ = arguments["server"].(string)
		server = strings.TrimSpace(server)
		if server == "" {
			return PolicyError{Boundary: "mcp", Reason: "workspace-scoped MCP search requires an explicit server"}
		}
	case "mcp_manage":
		action, _ := arguments["action"].(string)
		if strings.EqualFold(strings.TrimSpace(action), "list") {
			return PolicyError{Boundary: "mcp", Reason: "workspace-scoped MCP listing is disabled; use the workspace allowlist"}
		}
		server, _ = arguments["name"].(string)
		server = strings.TrimSpace(server)
		if server == "" {
			return PolicyError{Boundary: "mcp", Reason: "workspace-scoped MCP management requires an explicit server"}
		}
	default:
		return nil
	}
	if !contains(item.AllowedMCP, server) {
		return PolicyError{Boundary: "mcp", Resource: server, Reason: "server is not allowed by this workspace"}
	}
	return nil
}

func qualifiedServer(name string) string {
	name = strings.TrimSpace(name)
	index := strings.IndexByte(name, ':')
	if index <= 0 {
		return ""
	}
	return strings.TrimSpace(name[:index])
}

func enforceFilesystem(item Workspace, nodeOS, tool string, arguments map[string]any) error {
	switch tool {
	case "exec_command":
		return PolicyError{Boundary: "filesystem", Resource: tool, Reason: "exec_command cannot be safely filesystem-scoped and is disabled in strict workspace mode"}
	case "read_file":
		path, _ := arguments["path"].(string)
		if strings.HasPrefix(strings.TrimSpace(path), "skill://") {
			return nil
		}
		return requireReadPath(item, nodeOS, path)
	case "list_dir", "search_text", "file_publish":
		path, _ := arguments["path"].(string)
		return requireReadPath(item, nodeOS, path)
	case "view_image":
		path, _ := arguments["path"].(string)
		if strings.TrimSpace(path) == "" {
			return nil
		}
		return requireReadPath(item, nodeOS, path)
	case "file_edit":
		return enforceFileEdit(item, nodeOS, arguments)
	default:
		return nil
	}
}

func enforceFileEdit(item Workspace, nodeOS string, arguments map[string]any) error {
	action, _ := arguments["action"].(string)
	action = strings.TrimSpace(action)
	if action == "patch" {
		workdir, _ := arguments["workdir"].(string)
		if err := requireWritePath(item, nodeOS, workdir); err != nil {
			return err
		}
		patch, _ := arguments["patch"].(string)
		if patchEscapesWorkspace(patch) {
			return PolicyError{Boundary: "filesystem", Resource: "patch", Reason: "patch contains an absolute or parent-traversal path"}
		}
		return nil
	}
	path, _ := arguments["path"].(string)
	if err := requireWritePath(item, nodeOS, path); err != nil {
		return err
	}
	if newPath, _ := arguments["new_path"].(string); strings.TrimSpace(newPath) != "" {
		if err := requireWritePath(item, nodeOS, newPath); err != nil {
			return err
		}
	}
	return nil
}

func patchEscapesWorkspace(patch string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "*** ") {
			continue
		}
		if strings.Contains(line, "../") || strings.Contains(line, `..\`) || windowsDrivePattern.MatchString(strings.TrimPrefix(line, "*** Add File: ")) {
			return true
		}
	}
	return false
}

func requireReadPath(item Workspace, nodeOS, value string) error {
	roots := append([]string{item.ProjectRoot}, item.ContextRoots...)
	return requireScopedPath("filesystem", nodeOS, value, roots)
}

func requireWritePath(item Workspace, nodeOS, value string) error {
	return requireScopedPath("filesystem", nodeOS, value, []string{item.ProjectRoot})
}

func requireScopedPath(boundary, nodeOS, value string, roots []string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return PolicyError{Boundary: boundary, Reason: "an explicit absolute path is required in workspace mode"}
	}
	candidate, ok := canonicalPath(value, nodeOS)
	if !ok {
		return PolicyError{Boundary: boundary, Resource: value, Reason: "path must be absolute and local to the workspace node"}
	}
	for _, root := range roots {
		canonicalRoot, rootOK := canonicalPath(root, nodeOS)
		if rootOK && withinPath(candidate, canonicalRoot) {
			return nil
		}
	}
	return PolicyError{Boundary: boundary, Resource: value, Reason: "path is outside workspace roots"}
}

func canonicalPath(value, nodeOS string) (string, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.Contains(value, "://") {
		return "", false
	}
	if len(value) >= 7 && strings.EqualFold(value[:5], "/mnt/") && value[6] == '/' && isAlpha(value[5]) {
		value = strings.ToLower(value[5:6]) + ":" + value[6:]
	}
	isWindows := windowsDrivePattern.MatchString(value)
	isPOSIX := strings.HasPrefix(value, "/")
	if !isWindows && !isPOSIX {
		return "", false
	}
	value = pathpkg.Clean(value)
	if isWindows || strings.EqualFold(strings.TrimSpace(nodeOS), "windows") {
		value = strings.ToLower(value)
	}
	return value, true
}

func withinPath(candidate, root string) bool {
	root = strings.TrimSuffix(root, "/")
	return candidate == root || strings.HasPrefix(candidate, root+"/")
}

func enforceDomain(item Workspace, arguments map[string]any) error {
	domain := strings.TrimSpace(item.Domain)
	if domain == "" {
		return nil
	}
	return walkDomain(arguments, domain)
}

func walkDomain(value any, domain string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(strings.TrimSpace(key))
			if domainKey(lower) {
				if text, ok := child.(string); ok {
					if err := requireDomain(text, domain); err != nil {
						return err
					}
				}
			}
			if err := walkDomain(child, domain); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := walkDomain(child, domain); err != nil {
				return err
			}
		}
	}
	return nil
}

func domainKey(key string) bool {
	return key == "url" || strings.HasSuffix(key, "_url") || key == "uri" || strings.HasSuffix(key, "_uri") ||
		key == "domain" || key == "host" || key == "hostname" || key == "endpoint"
}

func requireDomain(value, allowed string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "#") {
		return nil
	}
	candidate := value
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Hostname() == "" {
		return PolicyError{Boundary: "domain", Resource: value, Reason: "URL host cannot be validated"}
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	allowedHost := strings.ToLower(strings.TrimSuffix(strings.Split(allowed, ":")[0], "."))
	if host != allowedHost && !strings.HasSuffix(host, "."+allowedHost) {
		return PolicyError{Boundary: "domain", Resource: value, Reason: "host is outside workspace domain " + allowed}
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func isAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
