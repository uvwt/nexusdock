package agentdock

import (
	"testing"
	"time"
)

func TestEvaluateHealthUsesEnabledCompatibilityAndConnectionState(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	lastSeen := now.Add(-90 * time.Second)
	base := Node{
		ID: "node", Enabled: true, ProtocolVersion: ConnectionProtocolVersion,
		LastSeenAt: &lastSeen, Capabilities: []string{"read_file"},
	}
	compatible := NewCompatibility(base, nil)

	ready := EvaluateHealth(base, true, compatible, now)
	if ready.State != HealthReady || ready.LastSeenAgeSeconds != 90 {
		t.Fatalf("ready=%#v", ready)
	}
	offline := EvaluateHealth(base, false, compatible, now)
	if offline.State != HealthOffline {
		t.Fatalf("offline=%#v", offline)
	}
	disabledNode := base
	disabledNode.Enabled = false
	if health := EvaluateHealth(disabledNode, false, NewCompatibility(disabledNode, nil), now); health.State != HealthDisabled {
		t.Fatalf("disabled=%#v", health)
	}
	incompatibleNode := base
	incompatibleNode.ProtocolVersion = "old"
	if health := EvaluateHealth(incompatibleNode, true, NewCompatibility(incompatibleNode, nil), now); health.State != HealthIncompatible {
		t.Fatalf("incompatible=%#v", health)
	}
}
