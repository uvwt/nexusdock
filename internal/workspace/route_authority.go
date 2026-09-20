package workspace

import (
	"net/url"
	pathpkg "path"
	"regexp"
	"strings"
)

var (
	absoluteURLPattern = regexp.MustCompile(`https?://[^\s<>"')\]}]+`)
	routeLinePattern   = regexp.MustCompile(`(?m)(?:^|[\s|("'])((?:/[A-Za-z0-9._~!$&()*+,;=:@%\-]+)+(?:/)?)(?:$|[\s|)"'])`)
)

type RouteAuthority struct {
	Domain string
	Routes map[string]struct{}
}

func ParseRouteAuthority(content, domain string) RouteAuthority {
	authority := RouteAuthority{Domain: strings.ToLower(strings.TrimSpace(domain)), Routes: make(map[string]struct{})}
	for _, raw := range absoluteURLPattern.FindAllString(content, -1) {
		parsed, err := url.Parse(strings.TrimRight(raw, ".,;:"))
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if authority.Domain != "" {
			allowed := strings.ToLower(strings.TrimSuffix(strings.Split(authority.Domain, ":")[0], "."))
			if host != allowed && !strings.HasSuffix(host, "."+allowed) {
				continue
			}
		}
		authority.Routes[normalizeRoute(parsed.EscapedPath())] = struct{}{}
	}
	for _, match := range routeLinePattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			authority.Routes[normalizeRoute(match[1])] = struct{}{}
		}
	}
	return authority
}

func (a RouteAuthority) Allows(value string) bool {
	route, ok := routeValue(value, a.Domain)
	if !ok {
		return true
	}
	_, exists := a.Routes[route]
	return exists
}

func RouteCandidates(arguments map[string]any) []string {
	values := make([]string, 0)
	collectRouteCandidates(arguments, "", &values)
	return normalizeList(values)
}

func collectRouteCandidates(value any, key string, output *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			collectRouteCandidates(child, strings.ToLower(strings.TrimSpace(childKey)), output)
		}
	case []any:
		for _, child := range typed {
			collectRouteCandidates(child, key, output)
		}
	case string:
		if routeField(key) && strings.TrimSpace(typed) != "" {
			*output = append(*output, typed)
		}
	}
}

func routeField(key string) bool {
	switch key {
	case "url", "page_url", "post_url", "target_url", "permalink", "route", "path", "slug":
		return true
	default:
		return strings.HasSuffix(key, "_permalink") || strings.HasSuffix(key, "_route") || strings.HasSuffix(key, "_slug")
	}
}

func routeValue(value, domain string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if strings.HasPrefix(value, "#") || strings.HasPrefix(value, "mailto:") || strings.HasPrefix(value, "tel:") {
		return "", false
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Hostname() == "" {
			return "", false
		}
		if domain != "" {
			host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
			allowed := strings.ToLower(strings.TrimSuffix(strings.Split(domain, ":")[0], "."))
			if host != allowed && !strings.HasSuffix(host, "."+allowed) {
				return "", false
			}
		}
		return normalizeRoute(parsed.EscapedPath()), true
	}
	if strings.HasPrefix(value, "/") {
		return normalizeRoute(value), true
	}
	if strings.Contains(value, "\\") || windowsDrivePattern.MatchString(strings.ReplaceAll(value, "\\", "/")) {
		return "", false
	}
	if strings.Contains(value, "/") || !strings.Contains(value, ".") {
		return normalizeRoute("/" + strings.Trim(value, "/")), true
	}
	return "", false
}

func normalizeRoute(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/"
	}
	if parsed, err := url.PathUnescape(value); err == nil {
		value = parsed
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = "/" + strings.TrimPrefix(value, "/")
	value = pathpkg.Clean(value)
	if value == "." {
		value = "/"
	}
	if value != "/" {
		value = strings.TrimSuffix(value, "/")
	}
	return strings.ToLower(value)
}

func RouteAuthorityPath(item Workspace, nodeOS string) (string, bool) {
	value := strings.TrimSpace(item.RouteAuthority)
	if value == "" {
		return "", false
	}
	candidate, absolute := canonicalPath(value, nodeOS)
	if !absolute {
		root, ok := canonicalPath(item.ProjectRoot, nodeOS)
		if !ok {
			return "", false
		}
		value = strings.ReplaceAll(value, "\\", "/")
		if strings.HasPrefix(value, "../") || value == ".." {
			return "", false
		}
		candidate = pathpkg.Clean(strings.TrimSuffix(root, "/") + "/" + strings.TrimPrefix(value, "/"))
	}
	roots := append([]string{item.ProjectRoot}, item.ContextRoots...)
	for _, root := range roots {
		canonicalRoot, ok := canonicalPath(root, nodeOS)
		if ok && withinPath(candidate, canonicalRoot) {
			return candidate, true
		}
	}
	return "", false
}
