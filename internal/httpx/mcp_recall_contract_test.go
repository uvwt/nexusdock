package httpx

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/recall"
)

func newRecallToolTestServer(t *testing.T) (*Server, *recall.Store) {
	t.Helper()
	store, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Server{store: store}, store
}

func TestRecallWriteMatchesCanonicalBehaviorCases(t *testing.T) {
	for _, behavior := range mcpcontract.RecallWriteBehaviorCases() {
		t.Run(behavior.Name, func(t *testing.T) {
			server, store := newRecallToolTestServer(t)
			args := recallWriteBehaviorArgs(behavior)
			before, existedBefore := prepareRecallWriteBehaviorFixture(t, store, behavior)

			result, err := server.callRecallWrite(t.Context(), args)
			if err == nil {
				assertCentralToolResultMatchesOutputSchema(t, mcpcontract.ToolRecallWrite, result)
			}
			got := classifyRecallWriteOutcome(behavior, result, err)
			if got != behavior.Expected {
				t.Fatalf("outcome=%s want=%s result=%#v err=%v", got, behavior.Expected, result, err)
			}
			assertRecallWriteBehaviorState(t, store, behavior, result, before, existedBefore)
		})
	}
}

func recallWriteBehaviorArgs(behavior mcpcontract.RecallWriteBehaviorCase) map[string]any {
	args := map[string]any{
		"target": behavior.Target, "action": behavior.Action,
		"confirmed": behavior.Confirmed,
	}
	if behavior.DryRun {
		args["dry_run"] = true
	}
	if behavior.Path != "" {
		args["path"] = behavior.Path
	}
	switch behavior.Target {
	case "card":
		args["title"] = "Canonical behavior card"
		args["content"] = "A reusable canonical behavior statement with enough detail to pass card validation without warnings."
	case "markdown":
		switch behavior.Action {
		case "append":
			args["content"] = "appended line\n"
		case "patch":
			args["old"] = "value: old"
			args["new"] = "value: new"
		case "update_fact":
			args["key"] = "value"
			args["value"] = "new"
		case "diff":
			args["content"] = "# Existing\n\nvalue: new\n"
		case "delete":
		default:
			args["content"] = "# Canonical\n\nvalue: new\n"
		}
	}
	return args
}

func prepareRecallWriteBehaviorFixture(t *testing.T, store *recall.Store, behavior mcpcontract.RecallWriteBehaviorCase) (string, bool) {
	t.Helper()
	if behavior.Target != "markdown" || behavior.Path == "" || !behavior.Existing {
		return "", false
	}
	content := "# Existing\n\nvalue: old\n"
	if _, err := store.Write(recall.WriteRequest{Path: behavior.Path, Content: content, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Read(behavior.Path)
	if err != nil {
		t.Fatal(err)
	}
	return stored.Content, true
}

func classifyRecallWriteOutcome(behavior mcpcontract.RecallWriteBehaviorCase, result map[string]any, err error) mcpcontract.RecallWriteOutcome {
	if err != nil {
		return mcpcontract.RecallWriteError
	}
	if behavior.Action == "diff" {
		return mcpcontract.RecallWriteReadOnly
	}
	if dryRun, _ := result["dry_run"].(bool); dryRun {
		return mcpcontract.RecallWritePreview
	}
	return mcpcontract.RecallWriteMutation
}

func assertRecallWriteBehaviorState(t *testing.T, store *recall.Store, behavior mcpcontract.RecallWriteBehaviorCase, result map[string]any, before string, existedBefore bool) {
	t.Helper()
	if behavior.Target == "card" {
		card, _ := result["card"].(map[string]any)
		path, _ := card["path"].(string)
		if path == "" {
			return
		}
		_, err := store.Read(path)
		if behavior.Expected == mcpcontract.RecallWriteMutation && err != nil {
			t.Fatalf("card mutation did not persist %q: %v", path, err)
		}
		if behavior.Expected != mcpcontract.RecallWriteMutation && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("non-mutating card behavior persisted %q: %v", path, err)
		}
		return
	}
	if behavior.Path == "" {
		return
	}
	after, err := store.Read(behavior.Path)
	if behavior.Expected == mcpcontract.RecallWriteMutation {
		if behavior.Action == "delete" {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("delete mutation left %q: %v", behavior.Path, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("mutation did not persist %q: %v", behavior.Path, err)
		}
		return
	}
	if existedBefore {
		if err != nil {
			t.Fatalf("non-mutating behavior removed %q: %v", behavior.Path, err)
		}
		if after.Content != before {
			t.Fatalf("non-mutating behavior changed %q: got %q want %q", behavior.Path, after.Content, before)
		}
		return
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-mutating behavior created %q: %v", behavior.Path, err)
	}
}

func TestRecallWriteMarkdownDryRunAndPlanNeverPersist(t *testing.T) {
	server, store := newRecallToolTestServer(t)
	path := "recall/docs/inbox/mcp-dry-run.md"

	for _, args := range []map[string]any{
		{"target": "markdown", "action": "create", "path": path, "content": "# Dry run", "confirmed": true, "dry_run": true},
		{"target": "markdown", "action": "plan", "path": path, "content": "# Plan", "confirmed": true},
	} {
		result, err := server.callRecallWrite(t.Context(), args)
		if err != nil {
			t.Fatalf("args=%#v err=%v", args, err)
		}
		if result["dry_run"] != true || result["path"] != path {
			t.Fatalf("args=%#v result=%#v", args, result)
		}
		if _, err := store.Read(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("args=%#v persisted preview: %v", args, err)
		}
	}
}

func TestRecallWriteDryRunProtectsExistingMarkdownAndCard(t *testing.T) {
	server, store := newRecallToolTestServer(t)
	markdownPath := "recall/docs/inbox/existing.md"
	if _, err := store.Write(recall.WriteRequest{Path: markdownPath, Content: "# Existing\n\nvalue: old\n", Confirmed: true}); err != nil {
		t.Fatal(err)
	}

	patched, err := server.callRecallWrite(t.Context(), map[string]any{
		"target": "markdown", "action": "patch", "path": markdownPath,
		"old": "value: old", "new": "value: new", "confirmed": true, "dry_run": true,
	})
	if err != nil || patched["dry_run"] != true {
		t.Fatalf("patch preview=%#v err=%v", patched, err)
	}
	current, err := store.Read(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(current.Content, "value: new") {
		t.Fatalf("patch dry-run mutated markdown: %q", current.Content)
	}

	deleted, err := server.callRecallWrite(t.Context(), map[string]any{
		"target": "markdown", "action": "delete", "path": markdownPath, "confirmed": true, "dry_run": true,
	})
	if err != nil || deleted["dry_run"] != true || deleted["would_delete"] != true {
		t.Fatalf("delete preview=%#v err=%v", deleted, err)
	}
	if _, err := store.Read(markdownPath); err != nil {
		t.Fatalf("delete dry-run removed markdown: %v", err)
	}

	cardResult, err := server.callRecallWrite(t.Context(), map[string]any{
		"target": "card", "action": "create", "title": "Dry Run Card",
		"content": "A reusable self-test statement with enough detail to pass card validation.",
		"type":    "runbook", "scope": "project", "project": "agentdock",
		"confirmed": true, "dry_run": true,
	})
	if err != nil || cardResult["dry_run"] != true {
		t.Fatalf("card preview=%#v err=%v", cardResult, err)
	}
	card, ok := cardResult["card"].(map[string]any)
	if !ok {
		t.Fatalf("card preview payload=%#v", cardResult["card"])
	}
	if _, err := store.Read(card["path"].(string)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("card dry-run persisted candidate: %v", err)
	}
}

func TestRecallMaintainRejectsRemovedSyncStatus(t *testing.T) {
	server, _ := newRecallToolTestServer(t)
	if _, err := server.callRecallMaintain(t.Context(), map[string]any{"action": "sync_status"}); err == nil || !strings.Contains(err.Error(), "unsupported recall maintenance action") {
		t.Fatalf("sync_status must be removed, got err=%v", err)
	}
}

func TestRecallWriteModelSchemaMatchesAgentDockContract(t *testing.T) {
	var schema map[string]any
	for _, tool := range nexusToolDefinitions() {
		if tool.Name == "recall_write" {
			schema = tool.InputSchema.(map[string]any)
			break
		}
	}
	if schema == nil {
		t.Fatal("recall_write definition missing")
	}
	properties := schema["properties"].(map[string]any)
	expected := map[string]bool{
		"target": true, "action": true, "title": true, "content": true, "summary": true,
		"confirmed": true, "path": true, "overwrite": true, "max_bytes": true,
		"old": true, "new": true, "append": true, "section": true, "section_content": true,
		"key": true, "value": true, "facts": true, "append_if_missing": true, "allow_warnings": true,
	}
	if len(properties) != len(expected) {
		t.Fatalf("recall_write properties=%#v", properties)
	}
	for field := range expected {
		if _, ok := properties[field]; !ok {
			t.Fatalf("recall_write schema missing %q", field)
		}
	}
	for _, hidden := range []string{"project", "scope", "status", "type", "tags", "confidence", "source", "evidence", "boundary", "dry_run"} {
		if _, ok := properties[hidden]; ok {
			t.Fatalf("internal field %q leaked into recall_write model schema", hidden)
		}
	}
}

func TestRecallWriteSectionPatchReturnsRenderableMetadataAndDiff(t *testing.T) {
	server, store := newRecallToolTestServer(t)
	path := "recall/docs/inbox/section-patch.md"
	original := "# Root\n\n## Target\nold body\n\n## Tail\nkeep\n"
	if _, err := store.Write(recall.WriteRequest{Path: path, Content: original, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}

	preview, err := server.callRecallWrite(t.Context(), map[string]any{
		"target": "markdown", "action": "patch", "path": path,
		"section": "Target", "section_content": "new body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview["recall_target"] != "markdown" || preview["recall_action"] != "patch" || preview["dry_run"] != true {
		t.Fatalf("preview metadata=%#v", preview)
	}
	diff, _ := preview["diff"].(string)
	if !strings.Contains(diff, "-old body") || !strings.Contains(diff, "+new body") {
		t.Fatalf("preview diff=%q", diff)
	}
	unchanged, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Content != baseline.Content {
		t.Fatalf("preview mutated content=%q", unchanged.Content)
	}

	written, err := server.callRecallWrite(t.Context(), map[string]any{
		"target": "markdown", "action": "patch", "path": path,
		"section": "Target", "section_content": "new body", "confirmed": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if written["recall_target"] != "markdown" || written["recall_action"] != "patch" {
		t.Fatalf("write metadata=%#v", written)
	}
	current, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current.Content, "## Target\nnew body\n## Tail") {
		t.Fatalf("section patch content=%q", current.Content)
	}
}

func TestRecallWriteSectionPatchUsesContentFallback(t *testing.T) {
	server, store := newRecallToolTestServer(t)
	path := "recall/docs/inbox/section-content-fallback.md"
	if _, err := store.Write(recall.WriteRequest{Path: path, Content: "# Root\n\n## Target\nold body\n", Confirmed: true}); err != nil {
		t.Fatal(err)
	}

	preview, err := server.callRecallWrite(t.Context(), map[string]any{
		"target": "markdown", "action": "patch", "path": path,
		"section": "Target", "content": "fallback body",
	})
	if err != nil {
		t.Fatal(err)
	}
	diff, _ := preview["diff"].(string)
	if !strings.Contains(diff, "-old body") || !strings.Contains(diff, "+fallback body") {
		t.Fatalf("content fallback diff=%q", diff)
	}
}

func TestRecallWriteDiffUsesProposedContent(t *testing.T) {
	server, store := newRecallToolTestServer(t)
	path := "recall/docs/inbox/diff.md"
	if _, err := store.Write(recall.WriteRequest{Path: path, Content: "# Old\n", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	result, err := server.callRecallWrite(t.Context(), map[string]any{
		"target": "markdown", "action": "diff", "path": path, "content": "# New\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["recall_action"] != "diff" || result["changed"] != true {
		t.Fatalf("diff result=%#v", result)
	}
	diff, _ := result["diff"].(string)
	if !strings.Contains(diff, "-# Old") || !strings.Contains(diff, "+# New") {
		t.Fatalf("diff=%q", diff)
	}
}

func TestDecorateRecallSearchResultsAddsStableCitationFields(t *testing.T) {
	server := &Server{cfg: config.Config{PublicURL: "https://nexus.example.test"}}
	results, err := server.decorateRecallSearchResults([]recall.SearchResult{{
		Path: "recall/docs/runbooks/deploy guide.md", Snippet: "deploy",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results=%#v", results)
	}
	item := results[0]
	if item["id"] != "recall/docs/runbooks/deploy guide.md" || item["title"] != "deploy guide" {
		t.Fatalf("citation identity=%#v", item)
	}
	url, _ := item["url"].(string)
	if !strings.HasPrefix(url, "https://nexus.example.test?") || !strings.Contains(url, "path=recall%2Fdocs%2Frunbooks%2Fdeploy+guide.md") || !strings.HasSuffix(url, "#recall/library") {
		t.Fatalf("citation url=%q", url)
	}
}

func TestCentralRecallResultsDoNotExposeGenericOK(t *testing.T) {
	server, _ := newRecallToolTestServer(t)

	maintain, err := server.callRecallMaintain(t.Context(), map[string]any{"action": "embedding_status"})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := maintain["ok"]; exists {
		t.Fatalf("recall_maintain leaked generic ok: %#v", maintain)
	}
	if maintain["recall_action"] != "embedding_status" {
		t.Fatalf("recall_maintain metadata=%#v", maintain)
	}
}

func TestCentralRecallSearchKindFiltersCardsFromMarkdown(t *testing.T) {
	server, store := newRecallToolTestServer(t)
	server.cfg.PublicURL = "https://nexus.example.test"
	if _, err := store.Write(recall.WriteRequest{Path: "profile.md", Content: "# Profile\n\nshared recall term\n", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	card, err := store.WriteCard(recall.CardRequest{
		Title: "Shared", Content: "shared recall term should remain reusable for search filtering", Type: recall.CardRunbook,
		Scope: recall.ScopeProject, Project: "agentdock", Status: recall.StatusInbox, Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		kind      string
		wantPaths map[string]bool
	}{
		{kind: "all", wantPaths: map[string]bool{"profile.md": true, card.Recall.Path: true}},
		{kind: "markdown", wantPaths: map[string]bool{"profile.md": true}},
		{kind: "card", wantPaths: map[string]bool{card.Recall.Path: true}},
	}

	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			result, err := server.callNexusTool(t.Context(), "recall_search", map[string]any{
				"query": "shared recall term", "kind": test.kind, "max_results": 10,
			})
			if err != nil {
				t.Fatal(err)
			}
			items, ok := result["results"].([]any)
			if !ok {
				t.Fatalf("unexpected results type: %#v", result["results"])
			}
			gotPaths := make(map[string]bool, len(items))
			for _, raw := range items {
				item, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("unexpected result item: %#v", raw)
				}
				path, _ := item["path"].(string)
				gotPaths[path] = true
			}
			if len(gotPaths) != len(test.wantPaths) {
				t.Fatalf("kind=%s paths=%#v want=%#v", test.kind, gotPaths, test.wantPaths)
			}
			for path := range test.wantPaths {
				if !gotPaths[path] {
					t.Fatalf("kind=%s missing path %q in %#v", test.kind, path, gotPaths)
				}
			}
		})
	}
}
