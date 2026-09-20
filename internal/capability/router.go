package capability

import (
	"sort"

	"github.com/uvwt/nexusdock/internal/agentdock"
)

type NodeCandidate struct {
	Node          agentdock.Node
	Online        bool
	Compatibility agentdock.Compatibility
}

type RouteCandidate struct {
	NodeID        string
	NodeName      string
	Tool          string
	WorkspaceSafe bool
}

func (r *Registry) RouteCandidates(name Name, nodes []NodeCandidate, strictWorkspace bool) []RouteCandidate {
	candidates := make([]RouteCandidate, 0, len(nodes))
	for _, node := range nodes {
		if !node.Node.Enabled || !node.Online || !node.Compatibility.Compatible {
			continue
		}
		resolution, err := r.Resolve(name, node.Compatibility, strictWorkspace)
		if err != nil {
			continue
		}
		candidates = append(candidates, RouteCandidate{
			NodeID: node.Node.ID, NodeName: node.Node.Name,
			Tool: resolution.Tool, WorkspaceSafe: resolution.WorkspaceSafe,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].NodeName != candidates[j].NodeName {
			return candidates[i].NodeName < candidates[j].NodeName
		}
		return candidates[i].NodeID < candidates[j].NodeID
	})
	return candidates
}
