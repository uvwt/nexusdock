package capability

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/uvwt/nexusdock/internal/agentdock"
)

type Name string

const (
	FilesystemRead    Name = "filesystem.read"
	FilesystemList    Name = "filesystem.list"
	FilesystemSearch  Name = "filesystem.search"
	FilesystemWrite   Name = "filesystem.write"
	FilesystemPublish Name = "filesystem.publish"
	ImageView         Name = "image.view"
	ShellExec         Name = "shell.exec"
	DynamicMCPSearch  Name = "mcp.dynamic.search"
	DynamicMCPInspect Name = "mcp.dynamic.inspect"
	DynamicMCPCall    Name = "mcp.dynamic.call"
	DynamicMCPManage  Name = "mcp.dynamic.manage"
	TaskManage        Name = "task.manage"
	SessionObserve    Name = "session.observe"
)

var ErrUnavailable = errors.New("logical capability is unavailable")

type Candidate struct {
	Tool          string
	WorkspaceSafe bool
}

type Definition struct {
	Name       Name
	Candidates []Candidate
}

type Resolution struct {
	Capability    Name
	Tool          string
	WorkspaceSafe bool
}

type Registry struct {
	definitions map[Name]Definition
	byTool      map[string]Name
}

func DefaultRegistry() *Registry {
	return NewRegistry([]Definition{
		{Name: FilesystemRead, Candidates: []Candidate{{Tool: "read_file", WorkspaceSafe: true}}},
		{Name: FilesystemList, Candidates: []Candidate{{Tool: "list_dir", WorkspaceSafe: true}}},
		{Name: FilesystemSearch, Candidates: []Candidate{{Tool: "search_text", WorkspaceSafe: true}}},
		{Name: FilesystemWrite, Candidates: []Candidate{{Tool: "file_edit", WorkspaceSafe: true}}},
		{Name: FilesystemPublish, Candidates: []Candidate{{Tool: "file_publish", WorkspaceSafe: true}}},
		{Name: ImageView, Candidates: []Candidate{{Tool: "view_image", WorkspaceSafe: true}}},
		{Name: ShellExec, Candidates: []Candidate{{Tool: "exec_command", WorkspaceSafe: false}}},
		{Name: DynamicMCPSearch, Candidates: []Candidate{{Tool: "mcp_tool_search", WorkspaceSafe: true}}},
		{Name: DynamicMCPInspect, Candidates: []Candidate{{Tool: "mcp_tool_inspect", WorkspaceSafe: true}}},
		{Name: DynamicMCPCall, Candidates: []Candidate{{Tool: "mcp_tool_call", WorkspaceSafe: true}}},
		{Name: DynamicMCPManage, Candidates: []Candidate{{Tool: "mcp_manage", WorkspaceSafe: true}}},
		{Name: TaskManage, Candidates: []Candidate{{Tool: "task_manage", WorkspaceSafe: true}}},
		{Name: SessionObserve, Candidates: []Candidate{{Tool: "session_observe", WorkspaceSafe: true}}},
	})
}

func NewRegistry(definitions []Definition) *Registry {
	registry := &Registry{
		definitions: make(map[Name]Definition, len(definitions)),
		byTool:      make(map[string]Name),
	}
	for _, definition := range definitions {
		name := Name(strings.TrimSpace(string(definition.Name)))
		if name == "" {
			continue
		}
		candidates := make([]Candidate, 0, len(definition.Candidates))
		for _, candidate := range definition.Candidates {
			candidate.Tool = strings.TrimSpace(candidate.Tool)
			if candidate.Tool == "" {
				continue
			}
			candidates = append(candidates, candidate)
			if _, exists := registry.byTool[candidate.Tool]; !exists {
				registry.byTool[candidate.Tool] = name
			}
		}
		registry.definitions[name] = Definition{Name: name, Candidates: candidates}
	}
	return registry
}

func (r *Registry) CapabilityForTool(tool string) (Name, bool) {
	if r == nil {
		return "", false
	}
	name, ok := r.byTool[strings.TrimSpace(tool)]
	return name, ok
}

func (r *Registry) ResolveTool(tool string, compatibility agentdock.Compatibility, strictWorkspace bool) (Resolution, error) {
	tool = strings.TrimSpace(tool)
	name, ok := r.CapabilityForTool(tool)
	if !ok {
		return Resolution{}, fmt.Errorf("%w: tool %s is not registered", ErrUnavailable, tool)
	}
	if !compatibility.Compatible {
		return Resolution{}, fmt.Errorf("%w: AgentDock protocol %q is not compatible", ErrUnavailable, compatibility.ProtocolVersion)
	}
	definition := r.definitions[name]
	for _, candidate := range definition.Candidates {
		if candidate.Tool != tool {
			continue
		}
		if strictWorkspace && !candidate.WorkspaceSafe {
			return Resolution{}, fmt.Errorf("%w: tool %s is not workspace-safe", ErrUnavailable, tool)
		}
		if !compatibility.SupportsCapability(tool) {
			return Resolution{}, fmt.Errorf("%w: node %s does not provide %s", ErrUnavailable, compatibility.NodeID, tool)
		}
		return Resolution{Capability: name, Tool: tool, WorkspaceSafe: candidate.WorkspaceSafe}, nil
	}
	return Resolution{}, fmt.Errorf("%w: tool %s has no provider definition", ErrUnavailable, tool)
}

func (r *Registry) Resolve(name Name, compatibility agentdock.Compatibility, strictWorkspace bool) (Resolution, error) {
	if r == nil {
		return Resolution{}, fmt.Errorf("%w: registry is not configured", ErrUnavailable)
	}
	definition, ok := r.definitions[name]
	if !ok {
		return Resolution{}, fmt.Errorf("%w: %s is not registered", ErrUnavailable, name)
	}
	if !compatibility.Compatible {
		return Resolution{}, fmt.Errorf("%w: AgentDock protocol %q is not compatible", ErrUnavailable, compatibility.ProtocolVersion)
	}
	for _, candidate := range definition.Candidates {
		if strictWorkspace && !candidate.WorkspaceSafe {
			continue
		}
		if compatibility.SupportsCapability(candidate.Tool) {
			return Resolution{Capability: name, Tool: candidate.Tool, WorkspaceSafe: candidate.WorkspaceSafe}, nil
		}
	}
	if strictWorkspace {
		return Resolution{}, fmt.Errorf("%w: %s has no workspace-safe provider on node %s", ErrUnavailable, name, compatibility.NodeID)
	}
	return Resolution{}, fmt.Errorf("%w: %s has no provider on node %s", ErrUnavailable, name, compatibility.NodeID)
}

func (r *Registry) Available(compatibility agentdock.Compatibility, strictWorkspace bool) []Resolution {
	if r == nil || !compatibility.Compatible {
		return []Resolution{}
	}
	names := make([]string, 0, len(r.definitions))
	for name := range r.definitions {
		names = append(names, string(name))
	}
	sort.Strings(names)
	result := make([]Resolution, 0, len(names))
	for _, raw := range names {
		if resolution, err := r.Resolve(Name(raw), compatibility, strictWorkspace); err == nil {
			result = append(result, resolution)
		}
	}
	return result
}
