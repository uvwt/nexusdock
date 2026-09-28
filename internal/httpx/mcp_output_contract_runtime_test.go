package httpx

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	jsonschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/nexusdock/internal/privatenotes"
	"github.com/uvwt/nexusdock/internal/recall"
)

func assertCentralToolResultMatchesOutputSchema(t *testing.T, name string, result map[string]any) map[string]any {
	t.Helper()

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal %s result: %v", name, err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		t.Fatalf("unmarshal %s result: %v", name, err)
	}

	definitions := centralToolDefinitionsByName(t)
	definition, ok := definitions[name]
	if !ok {
		t.Fatalf("central tool %s missing", name)
	}
	schemaJSON, err := json.Marshal(definition.OutputSchema)
	if err != nil {
		t.Fatalf("marshal %s output schema: %v", name, err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		t.Fatalf("unmarshal %s output schema: %v", name, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve %s output schema: %v", name, err)
	}
	if err := resolved.Validate(normalized); err != nil {
		t.Fatalf("%s result violates output schema: %v\nresult: %s", name, err, encoded)
	}
	return normalized
}

func TestCentralRuntimeOutputContractRecallSearch(t *testing.T) {
	server, store := newRecallToolTestServer(t)
	server.cfg.PublicURL = "https://nexus.example.test"
	if _, err := store.Write(recall.WriteRequest{
		Path:      "recall/docs/inbox/output-contract.md",
		Content:   "# Output Contract\n\nschema validation marker\n",
		Confirmed: true,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolRecallSearch, map[string]any{
		"query": "schema validation",
		"kind":  "markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	normalized := assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolRecallSearch, result)
	results, ok := normalized["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("recall_search results = %#v", normalized["results"])
	}
	item, ok := results[0].(map[string]any)
	if !ok || item["id"] == "" || item["title"] == "" || item["url"] == "" {
		t.Fatalf("recall_search citation fields = %#v", results[0])
	}
}

func TestCentralRuntimeOutputContractRecallBasics(t *testing.T) {
	server, store := newRecallToolTestServer(t)
	server.cfg.PublicURL = "https://nexus.example.test"

	path := "recall/docs/inbox/output-contract.md"
	if _, err := store.Write(recall.WriteRequest{
		Path: path, Content: "# Output Contract\n\nschema validation marker\n", Confirmed: true,
	}); err != nil {
		t.Fatal(err)
	}
	read, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolRecallRead, map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolRecallRead, read)

	write, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolRecallWrite, map[string]any{
		"target": "markdown", "action": "plan", "path": "recall/docs/inbox/planned.md", "content": "# Planned\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolRecallWrite, write)

	maintain, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolRecallMaintain, map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolRecallMaintain, maintain)

	lint, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolRecallMaintain, map[string]any{
		"action": "lint", "terms": []string{"definitely-not-present"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolRecallMaintain, lint)

	embeddingStatus, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolRecallMaintain, map[string]any{"action": "embedding_status"})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolRecallMaintain, embeddingStatus)

	card, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolRecallWrite, map[string]any{
		"target": "card", "action": "plan", "title": "Output contract card",
		"content": "Reusable output contract regression knowledge for central Recall validation.",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolRecallWrite, card)
}

func TestCentralRuntimeOutputContractPrivateNoteSearchAndStatus(t *testing.T) {
	store, err := privatenotes.New(filepath.Join(t.TempDir(), "private-notes"))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{privateNotes: store}

	search, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolPrivateNoteManage, map[string]any{
		"action": "search", "query": "no matching note",
	})
	if err != nil {
		t.Fatal(err)
	}
	normalizedSearch := assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolPrivateNoteManage, search)
	if normalizedSearch["query"] != "no matching note" || normalizedSearch["root"] == "" || normalizedSearch["metadata_only"] != true {
		t.Fatalf("private note search identity = %#v", normalizedSearch)
	}

	status, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolPrivateNoteManage, map[string]any{
		"action": "status", "status_action": "check",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolPrivateNoteManage, status)

	maintain, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolPrivateNoteManage, map[string]any{
		"action": "maintain", "maintenance_action": "init-encryption",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolPrivateNoteManage, maintain)

	written, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolPrivateNoteManage, map[string]any{
		"action": "write", "title": "Output contract note", "content": "private contract fixture", "confirmed": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolPrivateNoteManage, written)
	path, _ := written["path"].(string)
	if path == "" {
		t.Fatalf("private note write path = %#v", written["path"])
	}

	read, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolPrivateNoteManage, map[string]any{
		"action": "read", "path": path,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolPrivateNoteManage, read)

	deleted, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolPrivateNoteManage, map[string]any{
		"action": "delete", "path": path, "confirmed": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolPrivateNoteManage, deleted)
}

func TestCentralRuntimeOutputContractRecallSearchRejectsMissingPublicURL(t *testing.T) {
	server, store := newRecallToolTestServer(t)
	if _, err := store.Write(recall.WriteRequest{
		Path:      "recall/docs/inbox/output-contract.md",
		Content:   "# Output Contract\n\nschema validation marker\n",
		Confirmed: true,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolRecallSearch, map[string]any{
		"query": "schema validation",
		"kind":  "markdown",
	})
	if err == nil {
		t.Fatalf("recall_search without NEXUS_PUBLIC_URL returned schema-invalid success: %#v", result)
	}
	if result != nil {
		t.Fatalf("recall_search without NEXUS_PUBLIC_URL returned partial result: %#v", result)
	}
	if !strings.Contains(err.Error(), "NEXUS_PUBLIC_URL") {
		t.Fatalf("unexpected missing public URL error: %v", err)
	}
}

func TestCentralRuntimeOutputContractRecallSearchAllowsEmptyResultsWithoutPublicURL(t *testing.T) {
	server, _ := newRecallToolTestServer(t)
	result, err := callNexusToolForTest(t, server, t.Context(), mcpcontract.ToolRecallSearch, map[string]any{
		"query": "no matching document",
		"kind":  "markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	normalized := assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolRecallSearch, result)
	results, ok := normalized["results"].([]any)
	if !ok || len(results) != 0 {
		t.Fatalf("recall_search empty results = %#v, want []", normalized["results"])
	}
}
