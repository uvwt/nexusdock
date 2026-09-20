package agentdock

import (
	"strings"
	"testing"

	protocol "github.com/uvwt/agentdock-protocol"
)

func TestValidateProtocolVersionUsesSharedProtocolContract(t *testing.T) {
	if err := ValidateProtocolVersion(ConnectionProtocolVersion); err != nil {
		t.Fatalf("current protocol rejected: %v", err)
	}
	if err := ValidateProtocolVersion("future-incompatible"); err == nil {
		t.Fatal("incompatible protocol was accepted")
	} else if !strings.Contains(err.Error(), ConnectionProtocolVersion) {
		t.Fatalf("compatibility error does not name expected version: %v", err)
	}
}

func TestCompatibilityUsesCapabilitiesInsteadOfRuntimeVersion(t *testing.T) {
	compatibility := NewCompatibility(Node{
		ID:              "node_test",
		Version:         "99.0.0-future",
		ProtocolVersion: ConnectionProtocolVersion,
		Capabilities:    []string{"read_file", "read_file", "exec_command"},
	}, []string{protocol.ArtifactReadCapability, protocol.ArtifactReadCapability})

	if !compatibility.Compatible {
		t.Fatalf("compatible protocol rejected: %#v", compatibility)
	}
	if !compatibility.SupportsCapability("read_file") || !compatibility.SupportsCapability("exec_command") {
		t.Fatalf("runtime capabilities not preserved: %#v", compatibility.Capabilities)
	}
	if !compatibility.SupportsFeature(FeatureArtifactRead) {
		t.Fatalf("bridge feature not detected: %#v", compatibility.BridgeCapabilities)
	}
}

func TestCompatibilityRejectsFeaturesOnProtocolMismatch(t *testing.T) {
	compatibility := NewCompatibility(Node{
		ID:              "node_test",
		Version:         "0.8.3",
		ProtocolVersion: "old",
	}, []string{protocol.ArtifactReadCapability})

	if compatibility.Compatible {
		t.Fatal("incompatible protocol marked compatible")
	}
	if compatibility.SupportsFeature(FeatureArtifactRead) {
		t.Fatal("feature exposed through incompatible protocol")
	}
}
