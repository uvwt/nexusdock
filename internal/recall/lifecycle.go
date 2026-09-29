package recall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	maxLifecycleRecords    = 100
	maxAppliedOperationIDs = 256
)

var (
	ErrLifecycleRevisionConflict      = errors.New("lifecycle revision conflict")
	ErrLifecyclePolicyVersionConflict = errors.New("lifecycle policy version conflict")
	ErrLifecycleOperationConflict     = errors.New("lifecycle operation conflict")
	ErrLifecycleNotFound              = errors.New("lifecycle record not found")
	lifecycleIDPattern                = regexp.MustCompile(`^evo_[a-f0-9]{16,64}$`)
	operationIDPattern                = regexp.MustCompile(`^op_[A-Za-z0-9_-]{8,128}$`)
)

type LifecycleEvidence struct {
	Ref            string `json:"ref"`
	Relation       string `json:"relation"`
	TaskID         string `json:"task_id,omitempty"`
	ReviewRevision string `json:"review_revision,omitempty"`
	Rationale      string `json:"rationale,omitempty"`
	RecordedAt     string `json:"recorded_at"`
}

type LifecycleOperation struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type LifecycleRecord struct {
	EvolutionID       string               `json:"evolution_id"`
	Title             string               `json:"title"`
	Statement         string               `json:"statement"`
	Type              string               `json:"type"`
	Scope             string               `json:"scope"`
	Project           string               `json:"project"`
	Device            string               `json:"device,omitempty"`
	CanonicalKey      string               `json:"canonical_key,omitempty"`
	Status            string               `json:"status"`
	PolicyVersion     string               `json:"policy_version"`
	Revision          int64                `json:"revision"`
	SupportCount      int                  `json:"support_count"`
	ContradictCount   int                  `json:"contradict_count"`
	Source            string               `json:"source,omitempty"`
	Tags              []string             `json:"tags,omitempty"`
	Evidence          []LifecycleEvidence  `json:"evidence,omitempty"`
	SupersededBy      string               `json:"superseded_by,omitempty"`
	AppliedOperations []LifecycleOperation `json:"applied_operations,omitempty"`
	CreatedAt         string               `json:"created_at"`
	UpdatedAt         string               `json:"updated_at"`
}

type LifecycleQuery struct {
	Query       string   `json:"query"`
	EvolutionID string   `json:"evolution_id,omitempty"`
	Statuses    []string `json:"statuses,omitempty"`
	Types       []string `json:"types,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	Project     string   `json:"project,omitempty"`
	Device      string   `json:"device,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

type LifecycleTransition struct {
	OperationID      string          `json:"operation_id"`
	ExpectedRevision int64           `json:"expected_revision"`
	PolicyVersion    string          `json:"policy_version"`
	NextState        string          `json:"next_state"`
	EvidenceRefs     []string        `json:"evidence_refs,omitempty"`
	Record           LifecycleRecord `json:"record"`
}

type LifecycleTransitionResult struct {
	Record     LifecycleRecord `json:"record"`
	Idempotent bool            `json:"idempotent"`
}

func (s *Store) QueryLifecycle(query LifecycleQuery) ([]LifecycleRecord, error) {
	query.EvolutionID = strings.TrimSpace(query.EvolutionID)
	query.Query = strings.TrimSpace(query.Query)
	if query.EvolutionID != "" && !lifecycleIDPattern.MatchString(query.EvolutionID) {
		return nil, errors.New("invalid evolution_id")
	}
	limit := query.Limit
	if limit <= 0 || limit > maxLifecycleRecords {
		limit = 20
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if query.EvolutionID != "" {
		record, err := s.readLifecycleLocked(query.EvolutionID)
		if errors.Is(err, os.ErrNotExist) {
			return []LifecycleRecord{}, nil
		}
		if err != nil {
			return nil, err
		}
		if lifecycleMatches(record, query) {
			return []LifecycleRecord{record}, nil
		}
		return []LifecycleRecord{}, nil
	}

	root := s.lifecycleRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []LifecycleRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	type scoredRecord struct {
		record LifecycleRecord
		score  int
	}
	scored := make([]scoredRecord, 0, minInt(limit, len(entries)))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !lifecycleIDPattern.MatchString(id) {
			continue
		}
		record, err := s.readLifecycleLocked(id)
		if err != nil {
			return nil, fmt.Errorf("read lifecycle %s: %w", id, err)
		}
		if !lifecycleHardMatches(record, query) {
			continue
		}
		score := lifecycleQueryScore(record, query.Query)
		if query.Query != "" && score == 0 {
			continue
		}
		scored = append(scored, scoredRecord{record: record, score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if lifecycleStatusRank(scored[i].record.Status) != lifecycleStatusRank(scored[j].record.Status) {
			return lifecycleStatusRank(scored[i].record.Status) > lifecycleStatusRank(scored[j].record.Status)
		}
		if scored[i].record.UpdatedAt != scored[j].record.UpdatedAt {
			return scored[i].record.UpdatedAt > scored[j].record.UpdatedAt
		}
		return scored[i].record.EvolutionID < scored[j].record.EvolutionID
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	result := make([]LifecycleRecord, 0, len(scored))
	for _, item := range scored {
		result = append(result, item.record)
	}
	return result, nil
}

func (s *Store) TransitionLifecycle(request LifecycleTransition) (LifecycleTransitionResult, error) {
	if err := validateLifecycleTransition(request); err != nil {
		return LifecycleTransitionResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	incoming := request.Record
	digest, err := lifecycleTransitionDigest(request)
	if err != nil {
		return LifecycleTransitionResult{}, err
	}
	existing, err := s.readLifecycleLocked(incoming.EvolutionID)
	switch {
	case err == nil:
		if operation, ok := findLifecycleOperation(existing.AppliedOperations, request.OperationID); ok {
			if operation.Digest != digest {
				return LifecycleTransitionResult{}, ErrLifecycleOperationConflict
			}
			return LifecycleTransitionResult{Record: existing, Idempotent: true}, nil
		}
		if request.ExpectedRevision != existing.Revision {
			return LifecycleTransitionResult{}, ErrLifecycleRevisionConflict
		}
		if existing.PolicyVersion != request.PolicyVersion {
			return LifecycleTransitionResult{}, ErrLifecyclePolicyVersionConflict
		}
		if err := validateLifecycleIdentity(existing, incoming); err != nil {
			return LifecycleTransitionResult{}, err
		}
	case errors.Is(err, os.ErrNotExist):
		if request.ExpectedRevision != 0 {
			return LifecycleTransitionResult{}, ErrLifecycleRevisionConflict
		}
		existing = LifecycleRecord{}
	default:
		return LifecycleTransitionResult{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	incoming.Revision = request.ExpectedRevision + 1
	incoming.PolicyVersion = request.PolicyVersion
	incoming.Status = request.NextState
	incoming.AppliedOperations = appendAppliedOperation(existing.AppliedOperations, LifecycleOperation{ID: request.OperationID, Digest: digest})
	if existing.CreatedAt != "" {
		incoming.CreatedAt = existing.CreatedAt
	} else {
		incoming.CreatedAt = now
	}
	incoming.UpdatedAt = now
	if err := validateLifecycleRecord(incoming); err != nil {
		return LifecycleTransitionResult{}, err
	}
	if err := s.writeLifecycleLocked(incoming); err != nil {
		return LifecycleTransitionResult{}, err
	}
	return LifecycleTransitionResult{Record: incoming}, nil
}

func (s *Store) lifecycleRoot() string {
	return filepath.Join(s.root, "recall", "managed", "lifecycle")
}

func (s *Store) lifecyclePath(id string) string {
	return filepath.Join(s.lifecycleRoot(), id+".json")
}

func (s *Store) readLifecycleLocked(id string) (LifecycleRecord, error) {
	if !lifecycleIDPattern.MatchString(id) {
		return LifecycleRecord{}, errors.New("invalid evolution_id")
	}
	data, err := os.ReadFile(s.lifecyclePath(id))
	if err != nil {
		return LifecycleRecord{}, err
	}
	if len(data) > MaxFileBytes {
		return LifecycleRecord{}, errors.New("lifecycle record exceeds size limit")
	}
	var record LifecycleRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return LifecycleRecord{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return LifecycleRecord{}, errors.New("lifecycle record contains multiple JSON values")
		}
		return LifecycleRecord{}, err
	}
	if err := validateLifecycleRecord(record); err != nil {
		return LifecycleRecord{}, err
	}
	return record, nil
}

func (s *Store) writeLifecycleLocked(record LifecycleRecord) error {
	if err := os.MkdirAll(s.lifecycleRoot(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > MaxFileBytes {
		return errors.New("lifecycle record exceeds size limit")
	}
	return s.atomicWriteFile(s.lifecyclePath(record.EvolutionID), data, 0o644)
}

func validateLifecycleTransition(request LifecycleTransition) error {
	if !operationIDPattern.MatchString(strings.TrimSpace(request.OperationID)) {
		return errors.New("invalid operation_id")
	}
	if request.ExpectedRevision < 0 {
		return errors.New("expected_revision must not be negative")
	}
	if strings.TrimSpace(request.PolicyVersion) == "" {
		return errors.New("policy_version is required")
	}
	if !validLifecycleStatus(request.NextState) {
		return errors.New("invalid next_state")
	}
	if request.Record.PolicyVersion != "" && request.Record.PolicyVersion != request.PolicyVersion {
		return errors.New("record policy_version does not match transition")
	}
	if request.Record.Status != "" && request.Record.Status != request.NextState {
		return errors.New("record status does not match next_state")
	}
	if len(request.EvidenceRefs) > 128 {
		return errors.New("too many evidence_refs")
	}
	for _, ref := range request.EvidenceRefs {
		if strings.TrimSpace(ref) == "" || len(ref) > 512 {
			return errors.New("invalid evidence_ref")
		}
	}
	return validateLifecycleRecordIdentity(request.Record)
}

func validateLifecycleRecord(record LifecycleRecord) error {
	if err := validateLifecycleRecordIdentity(record); err != nil {
		return err
	}
	if !validLifecycleStatus(record.Status) {
		return errors.New("invalid lifecycle status")
	}
	if strings.TrimSpace(record.PolicyVersion) == "" {
		return errors.New("lifecycle policy_version is required")
	}
	if record.Revision <= 0 || record.SupportCount < 0 || record.ContradictCount < 0 {
		return errors.New("invalid lifecycle counters or revision")
	}
	if len(record.Evidence) > 1024 {
		return errors.New("too many lifecycle evidence entries")
	}
	for _, evidence := range record.Evidence {
		if strings.TrimSpace(evidence.Ref) == "" || !validLifecycleRelation(evidence.Relation) {
			return errors.New("invalid lifecycle evidence")
		}
	}
	for _, operation := range record.AppliedOperations {
		if !operationIDPattern.MatchString(operation.ID) || len(operation.Digest) != sha256.Size*2 {
			return errors.New("invalid lifecycle operation metadata")
		}
		if _, err := hex.DecodeString(operation.Digest); err != nil {
			return errors.New("invalid lifecycle operation digest")
		}
	}
	return nil
}

func validateLifecycleRecordIdentity(record LifecycleRecord) error {
	if !lifecycleIDPattern.MatchString(strings.TrimSpace(record.EvolutionID)) {
		return errors.New("invalid evolution_id")
	}
	if strings.TrimSpace(record.Type) == "" || strings.TrimSpace(record.Statement) == "" || strings.TrimSpace(record.Scope) == "" || strings.TrimSpace(record.Project) == "" {
		return errors.New("lifecycle record identity is incomplete")
	}
	if len([]rune(record.Statement)) > 2000 || len(record.Tags) > 64 {
		return errors.New("lifecycle record exceeds bounded identity fields")
	}
	return nil
}

func validateLifecycleIdentity(existing, incoming LifecycleRecord) error {
	if existing.EvolutionID != incoming.EvolutionID || existing.Type != incoming.Type || existing.Statement != incoming.Statement || existing.Scope != incoming.Scope || existing.Project != incoming.Project || existing.Device != incoming.Device || existing.CanonicalKey != incoming.CanonicalKey {
		return errors.New("lifecycle identity fields are immutable")
	}
	return nil
}

func lifecycleMatches(record LifecycleRecord, query LifecycleQuery) bool {
	if !lifecycleHardMatches(record, query) {
		return false
	}
	return query.Query == "" || lifecycleQueryScore(record, query.Query) > 0
}

// lifecycleHardMatches 只处理作用域、状态等确定性边界；自然语言 query 只能影响相关度，
// 不能再作为“所有词都必须出现”的硬门槛，否则 Task 描述越完整越难召回经验。
func lifecycleHardMatches(record LifecycleRecord, query LifecycleQuery) bool {
	if query.EvolutionID != "" && record.EvolutionID != query.EvolutionID {
		return false
	}
	if len(query.Statuses) > 0 && !containsFold(query.Statuses, record.Status) {
		return false
	}
	if len(query.Types) > 0 && !containsFold(query.Types, record.Type) {
		return false
	}
	if query.Scope != "" && !strings.EqualFold(query.Scope, record.Scope) {
		return false
	}
	if query.Project != "" && !strings.EqualFold(query.Project, record.Project) {
		return false
	}
	if query.Device != "" && !strings.EqualFold(query.Device, record.Device) {
		return false
	}
	return true
}

// lifecycleQueryScore 是小语料上的确定性词项排序。英文/数字按词匹配，中文按相邻双字匹配，
// 避免依赖空格分词，也避免为最多百条 lifecycle 记录引入远程 embedding 或第二套索引。
func lifecycleQueryScore(record LifecycleRecord, rawQuery string) int {
	query := strings.TrimSpace(strings.ToLower(rawQuery))
	if query == "" {
		return 0
	}
	queryTokens := lifecycleSearchTokens(query)
	if len(queryTokens) == 0 {
		return 0
	}

	score := 0
	fields := []struct {
		value  string
		weight int
	}{
		{record.CanonicalKey, 5},
		{record.Title, 4},
		{record.Statement, 3},
		{strings.Join(record.Tags, " "), 2},
		{record.EvolutionID, 1},
	}
	for _, field := range fields {
		value := strings.TrimSpace(strings.ToLower(field.value))
		if value == "" {
			continue
		}
		if strings.Contains(value, query) {
			score += 100 * field.weight
		}
		fieldTokens := lifecycleSearchTokens(value)
		for token := range queryTokens {
			if _, ok := fieldTokens[token]; ok {
				score += field.weight
			}
		}
	}
	return score
}

func lifecycleSearchTokens(text string) map[string]struct{} {
	tokens := map[string]struct{}{}
	var word []rune
	var han []rune
	flushWord := func() {
		if len(word) >= 2 {
			tokens[string(word)] = struct{}{}
		}
		word = word[:0]
	}
	flushHan := func() {
		if len(han) == 1 {
			tokens[string(han)] = struct{}{}
		} else {
			for i := 0; i+1 < len(han); i++ {
				tokens[string(han[i:i+2])] = struct{}{}
			}
		}
		han = han[:0]
	}
	for _, r := range []rune(strings.ToLower(text)) {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			word = append(word, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return tokens
}

func lifecycleStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "verified":
		return 5
	case "active":
		return 4
	case "provisional":
		return 3
	case "quarantine":
		return 2
	case "retired":
		return 1
	default:
		return 0
	}
}

func validLifecycleStatus(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "provisional", "active", "verified", "quarantine", "retired":
		return true
	default:
		return false
	}
}

func validLifecycleRelation(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "support", "contradict":
		return true
	default:
		return false
	}
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func appendAppliedOperation(values []LifecycleOperation, operation LifecycleOperation) []LifecycleOperation {
	out := append([]LifecycleOperation(nil), values...)
	out = append(out, operation)
	if len(out) > maxAppliedOperationIDs {
		out = out[len(out)-maxAppliedOperationIDs:]
	}
	return out
}

func findLifecycleOperation(values []LifecycleOperation, operationID string) (LifecycleOperation, bool) {
	for i := len(values) - 1; i >= 0; i-- {
		if values[i].ID == operationID {
			return values[i], true
		}
	}
	return LifecycleOperation{}, false
}

func lifecycleTransitionDigest(request LifecycleTransition) (string, error) {
	normalized := struct {
		ExpectedRevision int64           `json:"expected_revision"`
		PolicyVersion    string          `json:"policy_version"`
		NextState        string          `json:"next_state"`
		EvidenceRefs     []string        `json:"evidence_refs,omitempty"`
		Record           LifecycleRecord `json:"record"`
	}{
		ExpectedRevision: request.ExpectedRevision,
		PolicyVersion:    request.PolicyVersion,
		NextState:        request.NextState,
		EvidenceRefs:     append([]string(nil), request.EvidenceRefs...),
		Record:           request.Record,
	}
	normalized.Record.Revision = 0
	normalized.Record.Status = request.NextState
	normalized.Record.PolicyVersion = request.PolicyVersion
	normalized.Record.AppliedOperations = nil
	normalized.Record.CreatedAt = ""
	normalized.Record.UpdatedAt = ""
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
