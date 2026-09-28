package httpx

import (
	"testing"

	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/workflow"
)

func TestCentralWorkflowTemplateManageUsesNexusRegistry(t *testing.T) {
	dataDir := t.TempDir()
	server := &Server{cfg: config.Config{NexusDataDir: dataDir}, workflowRegistry: newTestWorkflowRegistry(dataDir)}
	for _, id := range []string{"development.demo", "development.review"} {
		if _, err := server.workflowRegistry.Publish(testWorkflowTemplate(id, "1.0.0")); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := callWorkflowTemplateManageForTest(t, server, t.Context(), map[string]any{"action": "list", "template_status": "active"})
	if err != nil || listed["count"] != 2 {
		t.Fatalf("list=%#v err=%v", listed, err)
	}
	assertCentralToolResultMatchesOutputSchema(t, "workflow_template_manage", listed)

	loaded, err := callWorkflowTemplateManageForTest(t, server, t.Context(), map[string]any{"action": "get", "template_id": "development.demo"})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, "workflow_template_manage", loaded)
	template, ok := loaded["template"].(workflow.Template)
	if !ok || template.ID != "development.demo" || template.Status != workflow.StatusActive {
		t.Fatalf("get=%#v", loaded)
	}

	many, err := callWorkflowTemplateManageForTest(t, server, t.Context(), map[string]any{
		"action": "get_many", "template_ids": []string{"development.demo", "development.review"},
	})
	if err != nil || many["count"] != 2 || many["composition_required"] != true || many["next_required_action"] != workflowCompositionNextAction {
		t.Fatalf("get_many=%#v err=%v", many, err)
	}
	assertCentralToolResultMatchesOutputSchema(t, "workflow_template_manage", many)

	matched, err := callWorkflowTemplateManageForTest(t, server, t.Context(), map[string]any{
		"action": "match", "goal": "demo development", "device": "DockMini", "type": "development",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, "workflow_template_manage", matched)
	if matched["action"] != "match" || matched["count"].(int) == 0 {
		t.Fatalf("match=%#v", matched)
	}

	vectorIndex, err := callWorkflowTemplateManageForTest(t, server, t.Context(), map[string]any{"action": "vector_index"})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, "workflow_template_manage", vectorIndex)
	if vectorIndex["action"] != "vector_index" || vectorIndex["vector_index_available"] != false {
		t.Fatalf("vector_index=%#v", vectorIndex)
	}
	if _, legacy := vectorIndex["available"]; legacy {
		t.Fatalf("vector_index leaked REST-only available field: %#v", vectorIndex)
	}
}

func TestCentralWorkflowTemplateManageSchemaHasNoNodeID(t *testing.T) {
	for _, tool := range nexusToolDefinitions() {
		if tool.Name != "workflow_template_manage" {
			continue
		}
		input := tool.InputSchema.(map[string]any)
		properties := input["properties"].(map[string]any)
		if _, exists := properties["node_id"]; exists {
			t.Fatalf("central workflow schema exposes node_id: %#v", input)
		}
		for _, name := range []string{"action", "template", "template_id", "template_ids", "template_version", "template_status", "goal", "device", "type"} {
			if _, exists := properties[name]; !exists {
				t.Fatalf("central workflow schema missing %s: %#v", name, properties)
			}
		}
		return
	}
	t.Fatal("central workflow_template_manage tool is missing")
}

func TestCentralWorkflowListDefaultsToCurrentVersionPerTemplate(t *testing.T) {
	dataDir := t.TempDir()
	server := &Server{cfg: config.Config{NexusDataDir: dataDir}, workflowRegistry: newTestWorkflowRegistry(dataDir)}
	for _, version := range []string{"1.0.0", "2.0.0"} {
		if _, err := server.workflowRegistry.Publish(testWorkflowTemplate("development.current", version)); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := callWorkflowTemplateManageForTest(t, server, t.Context(), map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if listed["count"] != 1 {
		t.Fatalf("default list=%#v", listed)
	}
	templates, ok := listed["templates"].([]workflowTemplateSummary)
	if !ok || len(templates) != 1 || templates[0].ID != "development.current" || templates[0].Version != "2.0.0" {
		t.Fatalf("default current templates=%#v", listed["templates"])
	}

	retired, err := callWorkflowTemplateManageForTest(t, server, t.Context(), map[string]any{"action": "list", "template_status": "retired"})
	if err != nil || retired["count"] != 1 {
		t.Fatalf("retired history=%#v err=%v", retired, err)
	}
}

func TestCentralWorkflowPublishRetireOutputContract(t *testing.T) {
	dataDir := t.TempDir()
	server := &Server{cfg: config.Config{NexusDataDir: dataDir}, workflowRegistry: newTestWorkflowRegistry(dataDir)}
	template, err := asBoundaryMap(testWorkflowTemplate("development.contract", "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}

	published, err := callWorkflowTemplateManageForTest(t, server, t.Context(), map[string]any{"action": "publish", "template": template})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, "workflow_template_manage", published)

	retired, err := callWorkflowTemplateManageForTest(t, server, t.Context(), map[string]any{
		"action": "retire", "template_id": "development.contract", "template_version": "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, "workflow_template_manage", retired)
}
