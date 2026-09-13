package recall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type ConflictSource string

const (
	ConflictSourceRuntimeObservation ConflictSource = "runtime_observation"
	ConflictSourceSkillRun           ConflictSource = "skill_run"
	ConflictSourceUserEdit           ConflictSource = "user_edit"
	ConflictSourceAgentRepair        ConflictSource = "agent_repair"
)

func (s ConflictSource) Valid() bool {
	switch s {
	case ConflictSourceRuntimeObservation, ConflictSourceSkillRun, ConflictSourceUserEdit, ConflictSourceAgentRepair:
		return true
	default:
		return false
	}
}

type ConflictStatus string

const (
	ConflictOpen     ConflictStatus = "open"
	ConflictResolved ConflictStatus = "resolved"
	ConflictIgnored  ConflictStatus = "ignored"
)

type ObservedFact struct {
	RecallPath    string         `json:"recall_path"`
	Key           string         `json:"key"`
	RecallValue   string         `json:"recall_value"`
	ObservedValue string         `json:"observed_value"`
	Source        ConflictSource `json:"source"`
	SourceID      string         `json:"source_id,omitempty"`
	Device        string         `json:"device,omitempty"`
	Agent         string         `json:"agent,omitempty"`
	RunID         string         `json:"run_id,omitempty"`
	Confidence    Confidence     `json:"confidence"`
	ObservedAt    time.Time      `json:"observed_at"`
}

type DetectConflictRequest struct {
	Facts []ObservedFact `json:"facts"`
}

type RecallConflict struct {
	ID            string         `json:"id"`
	RecallPath    string         `json:"recall_path"`
	Key           string         `json:"key"`
	RecallValue   string         `json:"recall_value"`
	ObservedValue string         `json:"observed_value"`
	Source        ConflictSource `json:"source"`
	SourceID      string         `json:"source_id,omitempty"`
	Device        string         `json:"device,omitempty"`
	Agent         string         `json:"agent,omitempty"`
	RunID         string         `json:"run_id,omitempty"`
	Confidence    Confidence     `json:"confidence"`
	Status        ConflictStatus `json:"status"`
	DetectedAt    time.Time      `json:"detected_at"`
	ResolvedAt    *time.Time     `json:"resolved_at,omitempty"`
}

type ConflictListRequest struct {
	Device string
	Agent  string
}

type ConflictRepository interface {
	Upsert(context.Context, RecallConflict) error
	ListOpen(context.Context, ConflictListRequest) ([]RecallConflict, error)
	Resolve(context.Context, string, ConflictStatus) error
}

type InRecallConflictRepository struct {
	mu    sync.RWMutex
	items map[string]RecallConflict
}

func NewInRecallConflictRepository() *InRecallConflictRepository {
	return &InRecallConflictRepository{items: map[string]RecallConflict{}}
}

func (r *InRecallConflictRepository) Upsert(_ context.Context, conflict RecallConflict) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.items[conflict.ID]; ok && existing.Status == ConflictResolved {
		return nil
	}
	r.items[conflict.ID] = conflict
	return nil
}

func (r *InRecallConflictRepository) ListOpen(_ context.Context, req ConflictListRequest) ([]RecallConflict, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RecallConflict, 0, len(r.items))
	for _, item := range r.items {
		if item.Status != ConflictOpen {
			continue
		}
		if req.Device != "" && !strings.EqualFold(req.Device, item.Device) {
			continue
		}
		if req.Agent != "" && !strings.EqualFold(req.Agent, item.Agent) {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DetectedAt.Equal(out[j].DetectedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].DetectedAt.After(out[j].DetectedAt)
	})
	return out, nil
}

func (r *InRecallConflictRepository) Resolve(_ context.Context, id string, status ConflictStatus) error {
	if status != ConflictResolved && status != ConflictIgnored {
		return errors.New("conflict can only be resolved or ignored")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return errors.New("recall conflict not found")
	}
	now := time.Now().UTC()
	item.Status = status
	item.ResolvedAt = &now
	r.items[id] = item
	return nil
}

func conflictFromFact(fact ObservedFact, now time.Time) (RecallConflict, bool) {
	if strings.TrimSpace(fact.RecallPath) == "" || strings.TrimSpace(fact.Key) == "" || !fact.Source.Valid() {
		return RecallConflict{}, false
	}
	if fact.Confidence == "" {
		fact.Confidence = ConfidenceMedium
	}
	if !fact.Confidence.Valid() || fact.Confidence == ConfidenceLow || fact.Confidence == ConfidenceUnknown {
		return RecallConflict{}, false
	}
	if normalizeFactValue(fact.RecallValue) == normalizeFactValue(fact.ObservedValue) {
		return RecallConflict{}, false
	}
	if fact.ObservedAt.IsZero() {
		fact.ObservedAt = now
	}
	fingerprint := strings.Join([]string{
		strings.TrimSpace(fact.RecallPath), strings.TrimSpace(fact.Key), normalizeFactValue(fact.RecallValue),
		normalizeFactValue(fact.ObservedValue), string(fact.Source), strings.TrimSpace(fact.SourceID),
	}, "\x00")
	sum := sha256.Sum256([]byte(fingerprint))
	return RecallConflict{
		ID: "mc_" + hex.EncodeToString(sum[:12]), RecallPath: fact.RecallPath, Key: fact.Key,
		RecallValue: fact.RecallValue, ObservedValue: fact.ObservedValue, Source: fact.Source,
		SourceID: fact.SourceID, Device: fact.Device, Agent: fact.Agent, RunID: fact.RunID,
		Confidence: fact.Confidence, Status: ConflictOpen, DetectedAt: fact.ObservedAt.UTC(),
	}, true
}

func normalizeFactValue(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}
