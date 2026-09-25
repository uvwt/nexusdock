package httpx

import (
	"reflect"
	"sort"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/agentdock-protocol/mcpcontract"
)

func TestCentralToolDefinitionsMatchNexusOwnedCanonicalContract(t *testing.T) {
	definitions := make(map[string]any, len(mcpcontract.ToolNames())-1)
	for _, tool := range nexusToolDefinitions() {
		definitions[tool.Name] = tool
	}
	if len(definitions) != len(mcpcontract.ToolNames())-1 {
		t.Fatalf("central tool count=%d want=%d", len(definitions), len(mcpcontract.ToolNames())-1)
	}
	if _, ok := definitions[mcpcontract.ToolWorkspaceContext]; ok {
		t.Fatal("workspace_context must remain an AgentDock node tool")
	}
	for _, name := range mcpcontract.ToolNames() {
		if name == mcpcontract.ToolWorkspaceContext {
			continue
		}
		raw, ok := definitions[name]
		if !ok {
			t.Fatalf("central tool %s missing", name)
		}
		tool := raw.(*mcpsdk.Tool)
		wantInput, _ := mcpcontract.InputSchema(name)
		if !reflect.DeepEqual(tool.InputSchema, wantInput) {
			t.Fatalf("%s input schema drifted from canonical contract", name)
		}
		var wantOutput map[string]any
		if name == mcpcontract.ToolAgentDockContext {
			wantOutput = mcpcontract.FleetAgentDockContextOutputSchema()
		} else {
			wantOutput, _ = mcpcontract.OutputSchema(name)
		}
		if !reflect.DeepEqual(tool.OutputSchema, wantOutput) {
			t.Fatalf("%s output schema drifted from canonical contract", name)
		}
		wantAnnotations, _ := mcpcontract.AnnotationContract(name)
		if tool.Annotations == nil ||
			tool.Annotations.ReadOnlyHint != wantAnnotations.ReadOnlyHint ||
			!reflect.DeepEqual(tool.Annotations.DestructiveHint, wantAnnotations.DestructiveHint) ||
			!reflect.DeepEqual(tool.Annotations.OpenWorldHint, wantAnnotations.OpenWorldHint) {
			t.Fatalf("%s annotations drifted: got=%#v want=%#v", name, tool.Annotations, wantAnnotations)
		}
		wantIdempotent := wantAnnotations.IdempotentHint != nil && *wantAnnotations.IdempotentHint
		if tool.Annotations.IdempotentHint != wantIdempotent {
			t.Fatalf("%s idempotentHint=%v want=%v", name, tool.Annotations.IdempotentHint, wantIdempotent)
		}
	}
}

func TestCentralRecallSearchPrioritizesCitationOverAppsUI(t *testing.T) {
	tools := map[string]*mcpsdk.Tool{}
	for _, tool := range nexusToolDefinitions() {
		tools[tool.Name] = tool
	}

	search := tools[mcpcontract.ToolRecallSearch]
	if search == nil {
		t.Fatal("recall_search missing")
	}
	if search.Meta["ui"] != nil {
		t.Fatalf("recall_search should not bind an Apps UI: %#v", search.Meta)
	}

	write := tools[mcpcontract.ToolRecallWrite]
	if write == nil {
		t.Fatal("recall_write missing")
	}
	ui, ok := write.Meta["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != protocol.RecallUIResourceURI {
		t.Fatalf("recall_write should keep Recall Apps UI: %#v", write.Meta)
	}
}

func TestCentralRecallAndWorkflowInputContractParity(t *testing.T) {
	definitions := centralToolDefinitionsByName(t)

	recallMaintain := contractSchemaProperties(t, definitions["recall_maintain"].InputSchema)
	if _, ok := recallMaintain["max_results"]; !ok {
		t.Fatal("recall_maintain missing AgentDock max_results input")
	}

	privateNote := contractSchemaProperties(t, definitions["private_note_manage"].InputSchema)
	assertIntegerBounds(t, "private_note_manage.max_results", privateNote["max_results"], 1, 100)
	assertIntegerBounds(t, "private_note_manage.max_bytes", privateNote["max_bytes"], 1, 1048576)

	workflow := contractSchemaProperties(t, definitions["workflow_template_manage"].InputSchema)
	wantWorkflowFields := []string{
		"action", "template", "template_id", "template_ids", "template_version", "template_status",
		"allow_long_template", "long_template_reason", "goal", "device", "type",
	}
	if got := sortedKeys(workflow); !reflect.DeepEqual(got, wantWorkflowFieldsSorted(wantWorkflowFields)) {
		t.Fatalf("workflow input fields=%v want=%v", got, wantWorkflowFieldsSorted(wantWorkflowFields))
	}
}

func TestCentralOutputContractKeepsRecallCitationAndRenderableFields(t *testing.T) {
	definitions := centralToolDefinitionsByName(t)

	searchOutput := contractSchemaProperties(t, definitions["recall_search"].OutputSchema)
	results := searchOutput["results"].(map[string]any)
	item := results["items"].(map[string]any)
	required := stringSet(item["required"])
	for _, field := range []string{"id", "title", "url"} {
		if !required[field] {
			t.Fatalf("recall_search result item missing required citation field %s: %#v", field, item)
		}
	}

	writeOutput := contractSchemaProperties(t, definitions["recall_write"].OutputSchema)
	for _, field := range []string{"recall_target", "recall_action", "path", "changed", "dry_run", "confirmed", "written", "diff", "updates"} {
		if _, ok := writeOutput[field]; !ok {
			t.Fatalf("recall_write output missing %s", field)
		}
	}

	contextOutput := contractSchemaProperties(t, definitions["agentdock_context"].OutputSchema)
	for _, field := range []string{"nodes", "shared"} {
		if _, ok := contextOutput[field]; !ok {
			t.Fatalf("central fleet context output missing intentional Nexus field %s", field)
		}
	}
}

func centralToolDefinitionsByName(t *testing.T) map[string]anyToolDefinition {
	t.Helper()
	result := make(map[string]anyToolDefinition)
	for _, tool := range nexusToolDefinitions() {
		result[tool.Name] = anyToolDefinition{InputSchema: tool.InputSchema, OutputSchema: tool.OutputSchema}
	}
	return result
}

type anyToolDefinition struct {
	InputSchema  any
	OutputSchema any
}

func contractSchemaProperties(t *testing.T, schema any) map[string]any {
	t.Helper()
	object, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("schema is %T, want map[string]any", schema)
	}
	properties, ok := object["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties=%#v", object["properties"])
	}
	return properties
}

func assertIntegerBounds(t *testing.T, name string, raw any, minimum, maximum int) {
	t.Helper()
	property, ok := raw.(map[string]any)
	if !ok || property["type"] != "integer" || property["minimum"] != minimum || property["maximum"] != maximum {
		t.Fatalf("%s=%#v", name, raw)
	}
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func wantWorkflowFieldsSorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func stringSet(raw any) map[string]bool {
	result := map[string]bool{}
	switch values := raw.(type) {
	case []string:
		for _, value := range values {
			result[value] = true
		}
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok {
				result[text] = true
			}
		}
	}
	return result
}
