package agentdock

import (
	"context"
	"fmt"
	"strings"

	protocol "github.com/uvwt/agentdock-protocol"
)

// Feature 表示 Nexus 真正依赖的 AgentDock 行为能力，而不是某个运行时版本。
// 新 AgentDock 可以增加版本或工具，只要继续声明相同能力，Nexus 上层无需跟随版本判断。
type Feature string

const (
	FeatureArtifactRead Feature = "artifact_read"
)

type Compatibility struct {
	NodeID                  string   `json:"node_id"`
	RuntimeVersion          string   `json:"runtime_version,omitempty"`
	ProtocolVersion         string   `json:"protocol_version,omitempty"`
	ExpectedProtocolVersion string   `json:"expected_protocol_version"`
	Compatible              bool     `json:"compatible"`
	Capabilities            []string `json:"capabilities"`
	BridgeCapabilities      []string `json:"bridge_capabilities"`
}

func ValidateProtocolVersion(actual string) error {
	actual = strings.TrimSpace(actual)
	if actual == "" {
		return fmt.Errorf("AgentDock 协议版本为空，Nexus 需要 %s", ConnectionProtocolVersion)
	}
	if actual != ConnectionProtocolVersion {
		return fmt.Errorf("AgentDock 协议版本 %s 与 Nexus 需要的 %s 不兼容", actual, ConnectionProtocolVersion)
	}
	return nil
}

func (s *Store) Compatibility(ctx context.Context, nodeID string) (Compatibility, error) {
	node, err := s.Get(ctx, nodeID)
	if err != nil {
		return Compatibility{}, err
	}
	bridgeCapabilities, err := s.BridgeCapabilities(ctx, node.ID)
	if err != nil {
		return Compatibility{}, err
	}
	return NewCompatibility(node, bridgeCapabilities), nil
}

func NewCompatibility(node Node, bridgeCapabilities []string) Compatibility {
	return Compatibility{
		NodeID:                  node.ID,
		RuntimeVersion:          strings.TrimSpace(node.Version),
		ProtocolVersion:         strings.TrimSpace(node.ProtocolVersion),
		ExpectedProtocolVersion: ConnectionProtocolVersion,
		Compatible:              ValidateProtocolVersion(node.ProtocolVersion) == nil,
		Capabilities:            normalizeCapabilities(node.Capabilities),
		BridgeCapabilities:      normalizeCapabilities(bridgeCapabilities),
	}
}

func (c Compatibility) SupportsCapability(name string) bool {
	return containsCapability(c.Capabilities, name)
}

func (c Compatibility) SupportsFeature(feature Feature) bool {
	if !c.Compatible {
		return false
	}
	switch feature {
	case FeatureArtifactRead:
		return containsCapability(c.BridgeCapabilities, protocol.ArtifactReadCapability)
	default:
		return false
	}
}
