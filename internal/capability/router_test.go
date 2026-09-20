package capability

import (
	"reflect"
	"testing"

	"github.com/uvwt/nexusdock/internal/agentdock"
)

func TestRouteCandidatesReturnsOnlyReadyCompatibleProviders(t *testing.T) {
	registry := DefaultRegistry()
	makeCandidate := func(id, name string, enabled, online bool, protocol string, tools ...string) NodeCandidate {
		node := agentdock.Node{
			ID: id, Name: name, Enabled: enabled,
			ProtocolVersion: protocol, Capabilities: tools,
		}
		return NodeCandidate{Node: node, Online: online, Compatibility: agentdock.NewCompatibility(node, nil)}
	}
	nodes := []NodeCandidate{
		makeCandidate("node_b", "Beta", true, true, agentdock.ConnectionProtocolVersion, "read_file"),
		makeCandidate("node_a", "Alpha", true, true, agentdock.ConnectionProtocolVersion, "read_file"),
		makeCandidate("node_offline", "Offline", true, false, agentdock.ConnectionProtocolVersion, "read_file"),
		makeCandidate("node_disabled", "Disabled", false, true, agentdock.ConnectionProtocolVersion, "read_file"),
		makeCandidate("node_old", "Old", true, true, "old", "read_file"),
	}
	got := registry.RouteCandidates(FilesystemRead, nodes, true)
	want := []RouteCandidate{
		{NodeID: "node_a", NodeName: "Alpha", Tool: "read_file", WorkspaceSafe: true},
		{NodeID: "node_b", NodeName: "Beta", Tool: "read_file", WorkspaceSafe: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates=%#v want=%#v", got, want)
	}
}

func TestRouteCandidatesDoesNotAutoRouteUnsafeShellInWorkspace(t *testing.T) {
	registry := DefaultRegistry()
	node := agentdock.Node{
		ID: "node_shell", Name: "Shell", Enabled: true,
		ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Capabilities:    []string{"exec_command"},
	}
	candidate := NodeCandidate{Node: node, Online: true, Compatibility: agentdock.NewCompatibility(node, nil)}
	if got := registry.RouteCandidates(ShellExec, []NodeCandidate{candidate}, true); len(got) != 0 {
		t.Fatalf("strict workspace shell candidates=%#v", got)
	}
	if got := registry.RouteCandidates(ShellExec, []NodeCandidate{candidate}, false); len(got) != 1 || got[0].Tool != "exec_command" {
		t.Fatalf("legacy shell candidates=%#v", got)
	}
}
