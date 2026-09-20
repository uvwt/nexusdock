package workspace

import "testing"

func policyWorkspace() Workspace {
	return Workspace{
		ID: "servocylmotion", NodeID: "node_test", ProjectRoot: `D:\website\ServoCylMotion`,
		Domain:       "servocylmotion.com",
		AllowedMCP:   []string{"elementor-servocylmotion", "novamira-servocylmotion-c"},
		ContextRoots: []string{`D:\website\ServoCylMotion\references`},
	}
}

func TestPolicyEnforcesMCPAllowlist(t *testing.T) {
	item := policyWorkspace()
	if err := Enforce(item, "windows", "mcp_tool_call", map[string]any{
		"name": "elementor-servocylmotion:update_page", "arguments": map[string]any{},
	}); err != nil {
		t.Fatalf("allowed MCP rejected: %v", err)
	}
	if err := Enforce(item, "windows", "mcp_tool_call", map[string]any{
		"name": "novamira-actulift-com:update_page", "arguments": map[string]any{},
	}); err == nil {
		t.Fatal("cross-project MCP was allowed")
	}
	if err := Enforce(item, "windows", "mcp_tool_search", map[string]any{"query": "page"}); err == nil {
		t.Fatal("unscoped MCP search was allowed")
	}
}

func TestPolicyEnforcesFilesystemRoots(t *testing.T) {
	item := policyWorkspace()
	if err := Enforce(item, "windows", "read_file", map[string]any{"path": `D:\website\ServoCylMotion\DESIGN.md`}); err != nil {
		t.Fatalf("project read rejected: %v", err)
	}
	if err := Enforce(item, "windows", "read_file", map[string]any{"path": "skill://rui-ui/SKILL.md"}); err != nil {
		t.Fatalf("skill read rejected: %v", err)
	}
	if err := Enforce(item, "windows", "read_file", map[string]any{"path": `D:\website\ActuLift\DESIGN.md`}); err == nil {
		t.Fatal("cross-project read was allowed")
	}
	if err := Enforce(item, "windows", "file_edit", map[string]any{"action": "replace", "path": `D:\website\ServoCylMotion\a.html`}); err != nil {
		t.Fatalf("project write rejected: %v", err)
	}
	if err := Enforce(item, "windows", "file_edit", map[string]any{"action": "replace", "path": `D:\website\ServoCylMotion2\a.html`}); err == nil {
		t.Fatal("prefix-confused path was allowed")
	}
	if err := Enforce(item, "windows", "exec_command", map[string]any{"workdir": `D:\website\ServoCylMotion`}); err == nil {
		t.Fatal("workspace exec_command was allowed without a real sandbox")
	}
}

func TestPolicyRecognizesWSLDriveAlias(t *testing.T) {
	item := policyWorkspace()
	if err := Enforce(item, "windows", "read_file", map[string]any{"path": "/mnt/d/website/ServoCylMotion/DESIGN.md"}); err != nil {
		t.Fatalf("WSL alias rejected: %v", err)
	}
}

func TestPolicyEnforcesDomainRecursively(t *testing.T) {
	item := policyWorkspace()
	if err := Enforce(item, "windows", "mcp_tool_call", map[string]any{
		"name":      "elementor-servocylmotion:update_page",
		"arguments": map[string]any{"url": "https://www.servocylmotion.com/products/test"},
	}); err != nil {
		t.Fatalf("allowed domain rejected: %v", err)
	}
	if err := Enforce(item, "windows", "mcp_tool_call", map[string]any{
		"name":      "elementor-servocylmotion:update_page",
		"arguments": map[string]any{"target_url": "https://actulift.com/products/test"},
	}); err == nil {
		t.Fatal("cross-domain URL was allowed")
	}
}
