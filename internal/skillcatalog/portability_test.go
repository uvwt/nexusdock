package skillcatalog

import "testing"

func TestGeneralClassificationRejectsBoundSkills(t *testing.T) {
	for _, files := range []map[string]string{
		{"SKILL.md": document + "Use /Users/owner/tools."},
		{"SKILL.md": document + "Call MCP__provider__tool from Codex."},
		{"SKILL.md": document, "run.py": "print('hello')"},
	} {
		s := New(t.TempDir())
		if _, err := s.Put(archive(t, files), Metadata{Portability: "general", ReviewNote: "Reviewed"}); err == nil {
			t.Fatal("environment-bound Skill mislabeled general")
		}
	}
	s := New(t.TempDir())
	if _, err := s.Put(archive(t, map[string]string{"SKILL.md": document}), Metadata{Portability: "general", ReviewNote: "All instructions are plain text and require no client or machine-specific capabilities."}); err != nil {
		t.Fatal(err)
	}
}
func TestBoundSkillRequiresUsageAndReviewPreservesArchive(t *testing.T) {
	s := New(t.TempDir())
	data := archive(t, map[string]string{"SKILL.md": document + "Use Codex on macOS."})
	if _, err := s.Put(data, Metadata{Portability: "environment_bound"}); err == nil {
		t.Fatal("accepted non-general Skill without usage")
	}
	e, err := s.Put(data, Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if e.Metadata.Portability != "unreviewed" {
		t.Fatal("unreviewed upload promoted automatically")
	}
	meta := Metadata{Portability: "environment_bound", Usage: Usage{Environment: "Codex on macOS", Setup: "Install the named tools", Example: "Read an input file", Verification: "Compare with bundled reference"}}
	got, err := s.Review(e.Name, e.Version, meta)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != e.SHA256 || got.InstallSHA256 != e.InstallSHA256 || got.Metadata.Usage.Example == "" {
		t.Fatal("review changed immutable content or lost usage")
	}
	if _, err := s.Review(e.Name, e.Version, Metadata{Portability: "general", ReviewNote: "Reviewed"}); err == nil {
		t.Fatal("review bypassed portability check")
	}
}
