package recall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBuildContextIndex_LongProfileDoesNotMonopolizeBudget(t *testing.T) {
	store := newTestStore(t)
	writeFixture(t, store, "profile.md", "---\nscope: profile\nstatus: active\n---\n\n# 用户长期偏好\n\n## 协作偏好\n\n"+strings.Repeat("长期偏好内容", 1200)+"\n")
	writeFixture(t, store, "recall/docs/projects/agentdock/project.md", "---\nscope: project\nstatus: active\nproject: agentdock\n---\n\n# AgentDock\n\n## 当前架构\n\n项目事实。\n")
	writeFixture(t, store, "recall/docs/projects/agentdock/environment.md", "---\nscope: project\nstatus: active\nproject: agentdock\n---\n\n# Environment\n\n生产环境说明。\n")
	writeFixture(t, store, "recall/docs/projects/agentdock/runbooks/deploy.md", "---\nscope: project\nstatus: active\nproject: agentdock\nkeywords: deploy,macos\naliases: deployment\nupdated_at: 2026-08-28T06:00:00Z\n---\n\n# AgentDock 部署\n\n完整步骤必须按需读取。\n")
	writeFixture(t, store, "recall/managed/cards/global/active/preference/commit-style.md", "---\ntype: recall-card\ncard_type: preference\nscope: global\nproject: global\nstatus: active\nconfidence: high\ntags: workflow,commit\nupdated_at: 2026-08-28T07:00:00Z\n---\n\n# 提交偏好\n\n同类修改优先整理。\n")

	index, err := store.BuildContextIndex(ContextIndexRequest{Project: "agentdock", MaxBytes: 3000})
	if err != nil {
		t.Fatal(err)
	}
	if index.TotalBytes > index.MaxBytes {
		t.Fatalf("context index exceeded budget: total=%d max=%d", index.TotalBytes, index.MaxBytes)
	}
	for _, kind := range []string{"profile", "project", "runbook", "card"} {
		if !contextIndexHasKind(index, kind) {
			t.Fatalf("long profile should not hide %s items: %#v", kind, index.Items)
		}
	}
	if got := len([]rune(index.Items[0].Summary)); got > contextProfileSummaryRunes {
		t.Fatalf("profile summary too long: %d", got)
	}
}

func TestBuildContextIndex_FiltersUnsafeStatusesAndKeepsGlobalCards(t *testing.T) {
	store := newTestStore(t)
	writeFixture(t, store, "profile.md", "---\nscope: profile\nstatus: active\n---\n\n# Profile\n\n用户偏好。\n")
	writeFixture(t, store, "recall/docs/projects/agentdock/runbooks/active.md", "---\nscope: project\nstatus: active\nproject: agentdock\nupdated_at: 2026-08-28T06:00:00Z\n---\n\n# Active Runbook\n")
	writeFixture(t, store, "recall/docs/projects/agentdock/runbooks/deprecated.md", "---\nscope: project\nstatus: deprecated\nproject: agentdock\nupdated_at: 2026-08-28T07:00:00Z\n---\n\n# Deprecated Runbook\n")
	writeFixture(t, store, "recall/managed/cards/global/active/preference/global.md", "---\ntype: recall-card\ncard_type: preference\nscope: global\nproject: global\nstatus: active\nconfidence: high\ntags: global\n---\n\n# Global Card\n")
	writeFixture(t, store, "recall/managed/cards/agentdock/inbox/preference/inbox.md", "---\ntype: recall-card\ncard_type: preference\nscope: project\nproject: agentdock\nstatus: inbox\nconfidence: high\n---\n\n# Inbox Card\n")

	index, err := store.BuildContextIndex(ContextIndexRequest{Project: "agentdock"})
	if err != nil {
		t.Fatal(err)
	}
	if !contextIndexHasPath(index, "recall/managed/cards/global/active/preference/global.md") {
		t.Fatalf("global active card missing: %#v", index.Items)
	}
	for _, forbidden := range []string{
		"recall/docs/projects/agentdock/runbooks/deprecated.md",
		"recall/managed/cards/agentdock/inbox/preference/inbox.md",
	} {
		if contextIndexHasPath(index, forbidden) {
			t.Fatalf("unsafe status entered context index: %s", forbidden)
		}
	}
}

func TestBuildContextIndex_DoesNotExposePrivateOrLifecycleFiles(t *testing.T) {
	store := newTestStore(t)
	writeFixture(t, store, "profile.md", "---\nscope: profile\nstatus: active\n---\n\n# Profile\n\n用户偏好。\n")
	writeFixture(t, store, "private-notes/notes/recovery/secret.md", "---\nscope: global\nstatus: active\n---\n\n# Secret\n\nshould never enter startup context\n")
	writeFixture(t, store, "recall/managed/lifecycle/evo_aaaaaaaaaaaaaaaa.json", `{"status":"active","secret":"should never enter startup context"}`)

	index, err := store.BuildContextIndex(ContextIndexRequest{Project: "agentdock", MaxBytes: contextIndexMaxBytes})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range index.Items {
		if strings.HasPrefix(item.Path, "private-notes/") || strings.HasPrefix(item.Path, "recall/managed/lifecycle/") {
			t.Fatalf("internal/private path leaked into context index: %#v", item)
		}
	}
}

func TestBuildContextIndex_UsesWholeUTF8ItemsWithinBudget(t *testing.T) {
	store := newTestStore(t)
	writeFixture(t, store, "profile.md", "---\nscope: profile\nstatus: active\nsummary: "+strings.Repeat("中文摘要", 80)+"\n---\n\n# Profile\n")
	writeFixture(t, store, "recall/docs/projects/agentdock/project.md", "---\nscope: project\nstatus: active\nproject: agentdock\nsummary: "+strings.Repeat("项目说明", 60)+"\n---\n\n# AgentDock\n")

	index, err := store.BuildContextIndex(ContextIndexRequest{Project: "agentdock", MaxBytes: 700})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(index.Items)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > index.MaxBytes {
		t.Fatalf("encoded items exceeded budget: %d > %d", len(encoded), index.MaxBytes)
	}
	for _, item := range index.Items {
		if !utf8.ValidString(item.Title) || !utf8.ValidString(item.Summary) || strings.ContainsRune(item.Summary, '\uFFFD') {
			t.Fatalf("invalid UTF-8 item: %#v", item)
		}
	}
}

func TestBuildContextIndex_MarksQuotaOmissionsAndKeepsNewestRunbooks(t *testing.T) {
	store := newTestStore(t)
	writeFixture(t, store, "profile.md", "---\nscope: profile\nstatus: active\n---\n\n# Profile\n")
	for i := 0; i < 8; i++ {
		path := "recall/docs/projects/agentdock/runbooks/runbook-" + string(rune('a'+i)) + ".md"
		content := "---\nscope: project\nstatus: active\nproject: agentdock\nupdated_at: 2026-08-28T0" + string(rune('0'+i)) + ":00:00Z\n---\n\n# Runbook\n"
		writeFixture(t, store, path, content)
	}

	index, err := store.BuildContextIndex(ContextIndexRequest{Project: "agentdock", MaxBytes: contextIndexMaxBytes})
	if err != nil {
		t.Fatal(err)
	}
	runbooks := 0
	for _, item := range index.Items {
		if item.Kind == "runbook" {
			runbooks++
		}
	}
	if runbooks != contextRunbookLimit {
		t.Fatalf("runbook count=%d want=%d: %#v", runbooks, contextRunbookLimit, index.Items)
	}
	if !index.Truncated || index.OmittedCount < 2 {
		t.Fatalf("quota omissions must be explicit: %#v", index)
	}
	if !contextIndexHasPath(index, "recall/docs/projects/agentdock/runbooks/runbook-h.md") {
		t.Fatalf("newest runbook missing: %#v", index.Items)
	}
}

func TestBuildContextIndex_KeepsOldVerifiedFactAfterManyNewerOpsFiles(t *testing.T) {
	store := newTestStore(t)
	verifiedPath := "recall/docs/ops/verified-fact.md"
	writeFixture(t, store, verifiedPath, "---\nscope: ops\nstatus: verified\nconfidence: high\nverified_at: 2026-08-01T00:00:00Z\n---\n\n# 已验证事实\n\n这是应进入启动索引的稳定事实。\n")
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(store.Root(), filepath.FromSlash(verifiedPath)), old, old); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		path := "recall/docs/ops/newer-" + string(rune('a'+i)) + ".md"
		writeFixture(t, store, path, "---\nscope: ops\nstatus: active\nconfidence: low\n---\n\n# 临时运维记录\n")
	}

	index, err := store.BuildContextIndex(ContextIndexRequest{Project: "agentdock", MaxBytes: contextIndexMaxBytes})
	if err != nil {
		t.Fatal(err)
	}
	if !contextIndexHasPath(index, verifiedPath) || !contextIndexHasKind(index, "verified_fact") {
		t.Fatalf("old verified fact must survive unrelated mtime ordering: %#v", index.Items)
	}
}

func TestBuildContextIndex_TinyBudgetKeepsByteInvariant(t *testing.T) {
	store := newTestStore(t)
	writeFixture(t, store, "profile.md", "---\nscope: profile\nstatus: active\n---\n\n# Profile\n")
	index, err := store.BuildContextIndex(ContextIndexRequest{Project: "agentdock", MaxBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if index.MaxBytes != 2 || index.TotalBytes > index.MaxBytes {
		t.Fatalf("tiny budget invariant failed: %#v", index)
	}
}

func contextIndexHasKind(index ContextIndex, kind string) bool {
	for _, item := range index.Items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func contextIndexHasPath(index ContextIndex, path string) bool {
	for _, item := range index.Items {
		if item.Path == path {
			return true
		}
	}
	return false
}
