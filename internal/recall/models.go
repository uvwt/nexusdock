package recall

import (
	"fmt"
	"strings"
	"time"
)

type Scope string

const (
	ScopeProfile Scope = "profile"
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
	ScopeDevice  Scope = "device"
	ScopeAgent   Scope = "agent"
	ScopeOps     Scope = "ops"
	ScopeInbox   Scope = "inbox"
)

func (s Scope) Valid() bool {
	switch s {
	case ScopeProfile, ScopeGlobal, ScopeProject, ScopeDevice, ScopeAgent, ScopeOps, ScopeInbox:
		return true
	default:
		return false
	}
}

type Status string

const (
	StatusInbox      Status = "inbox"
	StatusActive     Status = "active"
	StatusVerified   Status = "verified"
	StatusStale      Status = "stale"
	StatusArchived   Status = "archived"
	StatusRejected   Status = "rejected"
	StatusConflicted Status = "conflicted"
	StatusUnverified Status = "unverified"
	StatusDeprecated Status = "deprecated"
)

func (s Status) Valid() bool {
	switch s {
	case StatusInbox, StatusActive, StatusVerified, StatusStale, StatusArchived, StatusRejected, StatusConflicted, StatusUnverified, StatusDeprecated:
		return true
	default:
		return false
	}
}

type Confidence string

const (
	ConfidenceUnknown Confidence = "unknown"
	ConfidenceLow     Confidence = "low"
	ConfidenceMedium  Confidence = "medium"
	ConfidenceHigh    Confidence = "high"
)

func (c Confidence) Valid() bool {
	switch c {
	case ConfidenceUnknown, ConfidenceLow, ConfidenceMedium, ConfidenceHigh:
		return true
	default:
		return false
	}
}

type VerificationMetadata struct {
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	VerificationRunID string     `json:"verification_run_id,omitempty"`
	SourceDevice      string     `json:"source_device,omitempty"`
	SourceAgent       string     `json:"source_agent,omitempty"`
	Confidence        Confidence `json:"confidence"`
}

type Metadata struct {
	Scope        Scope                `json:"scope"`
	Status       Status               `json:"status"`
	Project      string               `json:"project,omitempty"`
	Device       string               `json:"device,omitempty"`
	Agent        string               `json:"agent,omitempty"`
	Skill        string               `json:"skill,omitempty"`
	Source       string               `json:"source,omitempty"`
	Verification VerificationMetadata `json:"verification"`
}

type Record struct {
	Recall
	Metadata Metadata `json:"metadata"`
}

func MetadataFromRecall(mem Recall) Metadata {
	fm := mem.Frontmatter
	scope := Scope(strings.ToLower(strings.TrimSpace(fm["scope"])))
	if !scope.Valid() {
		scope = inferScope(mem.Path)
	}
	status := Status(strings.ToLower(strings.TrimSpace(fm["status"])))
	if !status.Valid() {
		status = StatusActive
	}
	confidence := Confidence(strings.ToLower(strings.TrimSpace(fm["confidence"])))
	if !confidence.Valid() {
		confidence = ConfidenceUnknown
	}
	var verifiedAt *time.Time
	if raw := strings.TrimSpace(fm["verified_at"]); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			verifiedAt = &parsed
		}
	}
	project := strings.TrimSpace(fm["project"])
	device := firstNonEmpty(fm["device"], fm["source_device"])
	agent := firstNonEmpty(fm["agent"], fm["source_agent"])
	parts := strings.Split(strings.Trim(mem.Path, "/"), "/")
	if project == "" && len(parts) > 3 && parts[0] == "recall" && parts[1] == "docs" && parts[2] == "projects" {
		project = parts[3]
	}
	if device == "" && len(parts) == 4 && parts[0] == "recall" && parts[1] == "docs" && parts[2] == "devices" {
		device = strings.TrimSuffix(parts[3], ".md")
	}
	return Metadata{
		Scope: scope, Status: status, Project: project, Device: device, Agent: agent,
		Skill: strings.TrimSpace(fm["skill"]), Source: strings.TrimSpace(fm["source"]),
		Verification: VerificationMetadata{
			VerifiedAt: verifiedAt, VerificationRunID: strings.TrimSpace(fm["verification_run_id"]),
			SourceDevice: strings.TrimSpace(fm["source_device"]), SourceAgent: strings.TrimSpace(fm["source_agent"]), Confidence: confidence,
		},
	}
}

func recordFromRecall(mem Recall) Record {
	return Record{Recall: mem, Metadata: MetadataFromRecall(mem)}
}

func verifiedTime(meta Metadata) time.Time {
	if meta.Verification.VerifiedAt == nil {
		return time.Time{}
	}
	return *meta.Verification.VerifiedAt
}

func inferScope(path string) Scope {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	switch {
	case path == "profile.md":
		return ScopeProfile
	case strings.HasPrefix(path, "recall/docs/projects/"):
		return ScopeProject
	case strings.HasPrefix(path, "recall/docs/devices/"):
		return ScopeDevice
	case strings.HasPrefix(path, "recall/docs/ops/"):
		return ScopeOps
	case strings.HasPrefix(path, "recall/managed/cards/"):
		return ScopeProject
	case strings.HasPrefix(path, "recall/docs/inbox/"):
		return ScopeInbox
	default:
		return ScopeGlobal
	}
}

func validateMetadata(meta Metadata) error {
	if !meta.Scope.Valid() {
		return fmt.Errorf("invalid recall scope %q", meta.Scope)
	}
	if !meta.Status.Valid() {
		return fmt.Errorf("invalid recall status %q", meta.Status)
	}
	if c := meta.Verification.Confidence; c != "" && !c.Valid() {
		return fmt.Errorf("invalid recall confidence %q", c)
	}
	return nil
}

func matchesMetadata(meta Metadata, scopes []Scope, statuses []Status, project, device, agent, skill string) bool {
	containsScope := func(value Scope) bool {
		for _, item := range scopes {
			if item == value {
				return true
			}
		}
		return false
	}
	containsStatus := func(value Status) bool {
		for _, item := range statuses {
			if item == value {
				return true
			}
		}
		return false
	}
	if len(scopes) > 0 && !containsScope(meta.Scope) {
		return false
	}
	if len(statuses) > 0 && !containsStatus(meta.Status) {
		return false
	}
	match := func(want, got string) bool {
		return strings.TrimSpace(want) == "" || strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(got))
	}
	return match(project, meta.Project) && match(device, meta.Device) && match(agent, meta.Agent) && match(skill, meta.Skill)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
