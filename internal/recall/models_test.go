package recall

import (
	"testing"
	"time"
)

func TestRecallScopeSetMatchesContract(t *testing.T) {
	valid := []Scope{ScopeProfile, ScopeGlobal, ScopeProject, ScopeDevice, ScopeAgent, ScopeOps, ScopeInbox}
	for _, scope := range valid {
		if !scope.Valid() {
			t.Fatalf("scope %q should be valid", scope)
		}
	}
	if Scope("domain").Valid() {
		t.Fatalf("domain scope should not be valid unless it is documented in the public contract")
	}
}

func TestRecordFromRecallParsesVerificationMetadata(t *testing.T) {
	verifiedAt := "2026-06-05T12:00:00Z"
	record := recordFromRecall(Recall{
		Path: "recall/docs/devices/dockmini.md",
		Frontmatter: map[string]string{
			"scope": "device", "status": "active", "source_device": "DockMini",
			"source_agent": "agent-1", "confidence": "high", "verified_at": verifiedAt,
			"verification_run_id": "run-1",
		},
	})

	if record.Metadata.Scope != ScopeDevice || record.Metadata.Status != StatusActive {
		t.Fatalf("unexpected metadata: %#v", record.Metadata)
	}
	if record.Metadata.Verification.Confidence != ConfidenceHigh || record.Metadata.Verification.VerificationRunID != "run-1" {
		t.Fatalf("verification lost: %#v", record.Metadata.Verification)
	}
	want, err := time.Parse(time.RFC3339, verifiedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got := record.Metadata.Verification.VerifiedAt; got == nil || !got.Equal(want) {
		t.Fatalf("verified_at=%v want=%v", got, want)
	}
}
