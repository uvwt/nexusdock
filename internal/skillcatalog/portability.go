package skillcatalog

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

type Usage struct {
	Environment  string `json:"environment"`
	Setup        string `json:"setup"`
	Example      string `json:"example"`
	Verification string `json:"verification"`
}

var hostDependency = regexp.MustCompile(`(?i)(/Users/|/home/|/opt/|/root/|[A-Z]:\\|localhost|\b\d{1,3}(\.\d{1,3}){3}\b|\b(ssh|systemctl|launchctl|win32|powershell|codex|claude|hermes|orca|comfyui|cuda|nvidia|python|python3|nodejs|npm|npx|pip|brew|apt|docker|windows|macos|linux|tool_call)\b|\bmcp__[a-z]|functions\.exec)`)

func portabilityConstraints(files map[string][]byte) []string {
	code, host := false, false
	for name, b := range files {
		switch strings.ToLower(path.Ext(name)) {
		case ".md", ".txt", ".rst":
		default:
			code = true
		}
		if hostDependency.Match(b) {
			host = true
		}
	}
	out := []string{}
	if code {
		out = append(out, "Package includes code or non-document assets; runtime-specific review is required.")
	}
	if host {
		out = append(out, "Package references a client, host path, operating-system command or machine service.")
	}
	return out
}

func validatePortability(e *Entry) error {
	m := &e.Metadata
	if m.Portability == "" {
		m.Portability = "unreviewed"
	}
	switch m.Portability {
	case "unreviewed":
		return nil
	case "general":
		if strings.TrimSpace(m.ReviewNote) == "" || len(m.ReviewNote) > 8192 {
			return errors.New("general classification requires a review note covering all instructions and references")
		}
		// “通用”仅用于无执行代码、无主机/客户端绑定的文档流程，不能靠上传者声明覆盖检查。
		if len(e.Constraints) > 0 || len(m.Platforms) > 0 || strings.TrimSpace(m.Dependencies) != "" {
			return errors.New("general Skills must be dependency-free documents without client/platform bindings; classify this package as environment_bound and provide usage")
		}
	case "environment_bound":
		u := m.Usage
		for _, v := range []string{u.Environment, u.Setup, u.Example, u.Verification} {
			if strings.TrimSpace(v) == "" || len(v) > 8192 {
				return errors.New("non-general Skills require environment, setup, example and verification instructions (each at most 8192 characters)")
			}
		}
	default:
		return errors.New("portability must be unreviewed, general or environment_bound")
	}
	return nil
}
