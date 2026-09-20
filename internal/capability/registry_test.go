package capability

import (
	"errors"
	"testing"

	"github.com/uvwt/nexusdock/internal/agentdock"
)

func TestResolverUsesProtocolAndCapabilitiesNotRuntimeVersion(t *testing.T) {
	registry := DefaultRegistry()
	compatibility := agentdock.NewCompatibility(agentdock.Node{
		ID: "node_future", Version: "99.7.0-future",
		ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Capabilities:    []string{"read_file", "file_edit", "exec_command", "mcp_tool_call"},
	}, nil)

	read, err := registry.Resolve(FilesystemRead, compatibility, true)
	if err != nil || read.Tool != "read_file" {
		t.Fatalf("read resolution=%#v err=%v", read, err)
	}
	write, err := registry.Resolve(FilesystemWrite, compatibility, true)
	if err != nil || write.Tool != "file_edit" {
		t.Fatalf("write resolution=%#v err=%v", write, err)
	}
	if _, err := registry.Resolve(ShellExec, compatibility, true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("strict workspace shell resolution err=%v", err)
	}
	shell, err := registry.Resolve(ShellExec, compatibility, false)
	if err != nil || shell.Tool != "exec_command" {
		t.Fatalf("legacy shell resolution=%#v err=%v", shell, err)
	}
}

func TestResolverRejectsProtocolMismatch(t *testing.T) {
	registry := DefaultRegistry()
	compatibility := agentdock.NewCompatibility(agentdock.Node{
		ID: "node_old", ProtocolVersion: "incompatible", Capabilities: []string{"read_file"},
	}, nil)
	if _, err := registry.Resolve(FilesystemRead, compatibility, false); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("incompatible protocol err=%v", err)
	}
	if got := registry.Available(compatibility, false); len(got) != 0 {
		t.Fatalf("available=%#v", got)
	}
}

func TestRegistrySupportsFutureProviderAliases(t *testing.T) {
	registry := NewRegistry([]Definition{
		{Name: FilesystemRead, Candidates: []Candidate{
			{Tool: "read_file_v2", WorkspaceSafe: true},
			{Tool: "read_file", WorkspaceSafe: true},
		}},
	})
	compatibility := agentdock.NewCompatibility(agentdock.Node{
		ID: "node_alias", ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Capabilities: []string{"read_file"},
	}, nil)
	resolution, err := registry.Resolve(FilesystemRead, compatibility, true)
	if err != nil || resolution.Tool != "read_file" {
		t.Fatalf("resolution=%#v err=%v", resolution, err)
	}
	if capability, ok := registry.CapabilityForTool("read_file_v2"); !ok || capability != FilesystemRead {
		t.Fatalf("reverse lookup=%q ok=%v", capability, ok)
	}
}
