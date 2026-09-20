package workspace

import (
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("Runtime Workspace 不存在")
	ErrExists   = errors.New("Runtime Workspace 已存在")
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
)

type ValidationError struct{ Message string }

func (e ValidationError) Error() string { return e.Message }

type Workspace struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	NodeID            string    `json:"node_id"`
	ProjectRoot       string    `json:"project_root"`
	Domain            string    `json:"domain,omitempty"`
	AllowedMCP        []string  `json:"allowed_mcp"`
	ContextRoots      []string  `json:"context_roots"`
	DesignAuthorities []string  `json:"design_authorities"`
	RouteAuthority    string    `json:"route_authority,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type CreateInput struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	NodeID            string   `json:"node_id"`
	ProjectRoot       string   `json:"project_root"`
	Domain            string   `json:"domain,omitempty"`
	AllowedMCP        []string `json:"allowed_mcp,omitempty"`
	ContextRoots      []string `json:"context_roots,omitempty"`
	DesignAuthorities []string `json:"design_authorities,omitempty"`
	RouteAuthority    string   `json:"route_authority,omitempty"`
}

type UpdateInput struct {
	Name              *string   `json:"name,omitempty"`
	NodeID            *string   `json:"node_id,omitempty"`
	ProjectRoot       *string   `json:"project_root,omitempty"`
	Domain            *string   `json:"domain,omitempty"`
	AllowedMCP        *[]string `json:"allowed_mcp,omitempty"`
	ContextRoots      *[]string `json:"context_roots,omitempty"`
	DesignAuthorities *[]string `json:"design_authorities,omitempty"`
	RouteAuthority    *string   `json:"route_authority,omitempty"`
}

func normalizeCreate(input CreateInput) (CreateInput, error) {
	input.ID = strings.ToLower(strings.TrimSpace(input.ID))
	input.Name = strings.TrimSpace(input.Name)
	input.NodeID = strings.TrimSpace(input.NodeID)
	input.ProjectRoot = strings.TrimSpace(input.ProjectRoot)
	input.RouteAuthority = strings.TrimSpace(input.RouteAuthority)
	if !idPattern.MatchString(input.ID) {
		return CreateInput{}, ValidationError{Message: "workspace id 必须为 2-63 位小写字母、数字或连字符"}
	}
	if input.Name == "" || len([]rune(input.Name)) > 100 {
		return CreateInput{}, ValidationError{Message: "workspace name 必须为 1-100 个字符"}
	}
	if input.NodeID == "" {
		return CreateInput{}, ValidationError{Message: "workspace node_id 不能为空"}
	}
	if input.ProjectRoot == "" || len([]rune(input.ProjectRoot)) > 2048 {
		return CreateInput{}, ValidationError{Message: "workspace project_root 必须为 1-2048 个字符"}
	}
	domain, err := normalizeDomain(input.Domain)
	if err != nil {
		return CreateInput{}, err
	}
	input.Domain = domain
	input.AllowedMCP = normalizeList(input.AllowedMCP)
	input.ContextRoots = normalizeList(input.ContextRoots)
	input.DesignAuthorities = normalizeList(input.DesignAuthorities)
	return input, nil
}

func normalizeDomain(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	candidate := value
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ValidationError{Message: "workspace domain 必须是有效域名"}
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", ValidationError{Message: "workspace domain 不能包含路径"}
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if port := parsed.Port(); port != "" {
		host += ":" + port
	}
	return host, nil
}

func normalizeList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
