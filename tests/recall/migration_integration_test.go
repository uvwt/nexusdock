package recall_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	recall "github.com/uvwt/nexusdock/internal/recall"
)

func TestLegacyRepositoryMigrationIsLosslessAndUpdatable(t *testing.T) {
	source := filepath.Join(t.TempDir(), "legacy")
	target := filepath.Join(t.TempDir(), "nexus")
	if err := os.MkdirAll(filepath.Join(source, "projects", "agentdock", "runbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		"profile.md": "# Profile\nlegacy profile\n",
		"recall/docs/projects/agentdock/project.md":         "---\nscope: shared\nproject: agentdock\n---\n\n# AgentDock\nlegacy project\n",
		"recall/docs/projects/agentdock/runbooks/deploy.md": "# Deploy\nlegacy steps\n",
	}
	for path, content := range fixtures {
		abs := filepath.Join(source, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dry, err := recall.MigrateRepository(recall.MigrationRequest{SourceRoot: source, TargetRoot: target, DryRun: true})
	if err != nil || dry.FileCount != len(fixtures) || dry.Verified {
		t.Fatalf("dry=%#v err=%v", dry, err)
	}
	report, err := recall.MigrateRepository(recall.MigrationRequest{SourceRoot: source, TargetRoot: target})
	if err != nil || !report.Verified {
		t.Fatalf("report=%#v err=%v", report, err)
	}
	before, _ := recall.SnapshotFiles(source)
	after, _ := recall.SnapshotFiles(target)
	if len(before) != len(after) {
		t.Fatalf("snapshot length changed: %d %d", len(before), len(after))
	}
	for path, digest := range before {
		if after[path] != digest {
			t.Fatalf("digest mismatch: %s", path)
		}
	}

	store, err := recall.NewStore(target)
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := recall.NewService(store)
	index, err := store.BuildContextIndex(recall.ContextIndexRequest{Project: "agentdock", MaxBytes: 4096})
	if err != nil || len(index.Items) < 2 {
		t.Fatalf("context index regressed: %#v err=%v", index, err)
	}

	proposal, err := svc.ProposeUpdate(context.Background(), recall.ProposeUpdateRequest{Path: "recall/docs/projects/agentdock/project.md", Content: "# AgentDock\nupdated", Scope: recall.ScopeProject, Status: recall.StatusActive, Project: "agentdock", Source: "user_edit", Confidence: recall.ConfidenceHigh})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyUpdate(context.Background(), recall.ApplyUpdateRequest{Proposal: proposal, Approved: true}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Read("recall/docs/projects/agentdock/project.md")
	if err != nil || !strings.Contains(updated.Content, "updated") {
		t.Fatalf("updated recall missing: %#v err=%v", updated, err)
	}
}

func TestLiveRecallRepositoryValidation(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("RECALL_LIVE_STORE"))
	if root == "" {
		t.Skip("RECALL_LIVE_STORE is not set")
	}
	before, err := recall.SnapshotFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	report, err := recall.MigrateRepository(recall.MigrationRequest{SourceRoot: root, TargetRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if !report.InPlace || !report.Verified {
		t.Fatalf("live recall repository not verified: %#v", report)
	}
	after, err := recall.SnapshotFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("live validation changed file count: %d != %d", len(before), len(after))
	}
	for path, digest := range before {
		if after[path] != digest {
			t.Fatalf("live validation changed %s", path)
		}
	}
}
