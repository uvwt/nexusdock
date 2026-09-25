package httpx

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/nexusdock/internal/agentdock"
)

type centralOutputContractCoverageEntry struct {
	Variants []string
}

// Nexus 只负责自己生成 structuredContent 的中央工具契约。
// 节点工具由 AgentDock 负责 runtime contract，Nexus 这里只验证代理层保持 outputSchema 与结果透明。
var centralOutputContractCoverageInventory = map[string]centralOutputContractCoverageEntry{
	"agentdock_context":        {Variants: []string{"success"}},
	"recall_search":            {Variants: []string{"success"}},
	"recall_read":              {Variants: []string{"success"}},
	"recall_write":             {Variants: []string{"plan", "create", "replace", "append", "patch", "update_fact", "diff", "delete"}},
	"recall_maintain":          {Variants: []string{"list", "lint", "embedding_status"}},
	"private_note_manage":      {Variants: []string{"search", "read", "write", "delete", "status", "maintain"}},
	"workflow_template_manage": {Variants: []string{"publish", "retire", "list", "get", "get_many", "match", "vector_index"}},
}

func TestCentralOutputContractCoverageMatchesPublishedTools(t *testing.T) {
	if findings := centralOutputContractCoverageFindings(nexusToolDefinitions(), centralOutputContractCoverageInventory); len(findings) > 0 {
		t.Fatalf("central output contract coverage guard failed:\n- %s", strings.Join(findings, "\n- "))
	}
}

func TestCentralOutputContractCoverageGuardDetectsMissingAndInvalidEntries(t *testing.T) {
	inventory := make(map[string]centralOutputContractCoverageEntry, len(centralOutputContractCoverageInventory))
	for name, entry := range centralOutputContractCoverageInventory {
		inventory[name] = entry
	}
	delete(inventory, "agentdock_context")
	inventory["recall_maintain"] = centralOutputContractCoverageEntry{Variants: []string{"future_action"}}

	findings := strings.Join(centralOutputContractCoverageFindings(nexusToolDefinitions(), inventory), "\n")
	for _, want := range []string{
		"agentdock_context: missing coverage",
		`recall_maintain: variant "future_action" is not in action enum`,
	} {
		if !strings.Contains(findings, want) {
			t.Fatalf("coverage guard findings missing %q:\n%s", want, findings)
		}
	}
}

func centralOutputContractCoverageFindings(definitions []*mcpsdk.Tool, inventory map[string]centralOutputContractCoverageEntry) []string {
	known := make(map[string]*mcpsdk.Tool, len(definitions))
	findings := make([]string, 0)
	for _, definition := range definitions {
		known[definition.Name] = definition
		if definition.OutputSchema == nil {
			findings = append(findings, definition.Name+": missing outputSchema")
		}
		entry, ok := inventory[definition.Name]
		if !ok {
			findings = append(findings, definition.Name+": missing coverage")
			continue
		}
		if len(entry.Variants) == 0 {
			findings = append(findings, definition.Name+": coverage has no success variants")
			continue
		}
		findings = append(findings, centralOutputContractVariantFindings(definition, entry.Variants)...)
	}
	for name := range inventory {
		if _, ok := known[name]; !ok {
			findings = append(findings, name+": stale coverage for removed central tool")
		}
	}
	sort.Strings(findings)
	return findings
}

func centralOutputContractVariantFindings(definition *mcpsdk.Tool, variants []string) []string {
	properties, _ := definition.InputSchema.(map[string]any)["properties"].(map[string]any)
	action, _ := properties["action"].(map[string]any)
	allowed, _ := action["enum"].([]string)
	if len(allowed) == 0 {
		if len(variants) != 1 || variants[0] != "success" {
			return []string{fmt.Sprintf("%s: actionless tool must use the success variant, got %v", definition.Name, variants)}
		}
		return nil
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	findings := make([]string, 0)
	for _, variant := range variants {
		if _, ok := allowedSet[variant]; !ok {
			findings = append(findings, fmt.Sprintf("%s: variant %q is not in action enum %v", definition.Name, variant, allowed))
		}
	}
	return findings
}

func TestNodeToolProxyKeepsAgentDockOutputContractAndStructuredContent(t *testing.T) {
	outputSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"items": map[string]any{"type": "array"}},
		"required":   []string{"items"},
	}
	descriptor := agentdock.ToolDescriptor{
		Name: "demo_node_tool",
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}},
		},
		OutputSchema: outputSchema,
	}
	published := nodeMCPTool(descriptor)
	if !reflect.DeepEqual(published.OutputSchema, outputSchema) {
		t.Fatalf("node output schema changed: got=%#v want=%#v", published.OutputSchema, outputSchema)
	}

	structured := map[string]any{"items": []any{}}
	proxied, err := gatewayToolResult(descriptor.Name, map[string]any{
		"isError":           false,
		"structuredContent": structured,
		"content":           []map[string]any{{"type": "text", "text": "upstream"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(proxied.StructuredContent, structured) {
		t.Fatalf("node structuredContent changed: got=%#v want=%#v", proxied.StructuredContent, structured)
	}
}
