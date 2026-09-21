package capability

import (
	"errors"
	"testing"

	"github.com/uvwt/nexusdock/internal/agentdock"
)

func TestBrowserCapabilitiesResolveButFailClosedInStrictWorkspace(t *testing.T) {
	registry := DefaultRegistry()
	compatibility := agentdock.NewCompatibility(agentdock.Node{
		ID:              "node_browser",
		ProtocolVersion: agentdock.ConnectionProtocolVersion,
		Capabilities:    []string{"browser_session", "browser_act", "browser_snapshot"},
	}, nil)

	for _, tc := range []struct {
		name Name
		tool string
	}{
		{BrowserSession, "browser_session"},
		{BrowserAct, "browser_act"},
		{BrowserSnapshot, "browser_snapshot"},
	} {
		if _, err := registry.Resolve(tc.name, compatibility, true); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s should fail closed in strict workspace, err=%v", tc.name, err)
		}
		got, err := registry.Resolve(tc.name, compatibility, false)
		if err != nil {
			t.Fatalf("%s legacy resolution failed: %v", tc.name, err)
		}
		if got.Tool != tc.tool || got.WorkspaceSafe {
			t.Fatalf("%s resolution=%#v", tc.name, got)
		}
		if reverse, ok := registry.CapabilityForTool(tc.tool); !ok || reverse != tc.name {
			t.Fatalf("%s reverse lookup=%q ok=%v", tc.tool, reverse, ok)
		}
	}
}
