package recall

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ContextIndexDefaultMaxBytes = 3000
	contextIndexMaxBytes        = 32_000

	contextProfileLimit      = 1
	contextProjectCoreLimit  = 2
	contextVerifiedFactLimit = 4
	contextRunbookLimit      = 6
	contextCardLimit         = 6

	contextRunbookPrefetch = contextRunbookLimit * 3
	contextCardPrefetch    = contextCardLimit * 3

	contextProfileSummaryRunes = 200
	contextProjectSummaryRunes = 160
	contextFactSummaryRunes    = 120
)

type ContextIndexRequest struct {
	Project  string `json:"project,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

type ContextIndexItem struct {
	Kind       string   `json:"kind"`
	Path       string   `json:"path"`
	Title      string   `json:"title,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	CardType   string   `json:"card_type,omitempty"`
	Status     Status   `json:"status,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
	VerifiedAt string   `json:"verified_at,omitempty"`
}

type ContextIndex struct {
	Project      string             `json:"project,omitempty"`
	Items        []ContextIndexItem `json:"items"`
	TotalBytes   int                `json:"total_bytes"`
	MaxBytes     int                `json:"max_bytes"`
	Truncated    bool               `json:"truncated"`
	OmittedCount int                `json:"omitted_count,omitempty"`
}

type contextIndexCandidate struct {
	record   Record
	modified time.Time
}

// BuildContextIndex 为 agentdock_context 构造无查询的紧凑 Recall 索引。
// 每类先按条数限额，再受全局 JSON 字节预算约束，避免某一篇长文档独占启动上下文。
func (s *Store) BuildContextIndex(req ContextIndexRequest) (ContextIndex, error) {
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = ContextIndexDefaultMaxBytes
	}
	if maxBytes < 2 {
		maxBytes = 2 // [] 的 JSON 编码本身占 2 字节，保持 TotalBytes <= MaxBytes 不变量。
	}
	if maxBytes > contextIndexMaxBytes {
		maxBytes = contextIndexMaxBytes
	}
	project := SafeSegment(req.Project)
	index := ContextIndex{Project: project, Items: []ContextIndexItem{}, MaxBytes: maxBytes}

	selected := make(map[string]struct{})
	appendItem := func(item ContextIndexItem) {
		if item.Path == "" {
			return
		}
		if _, exists := selected[item.Path]; exists {
			return
		}
		trial := append(append([]ContextIndexItem(nil), index.Items...), item)
		encoded, err := json.Marshal(trial)
		if err != nil || len(encoded) > maxBytes {
			index.Truncated = true
			index.OmittedCount++
			return
		}
		index.Items = trial
		index.TotalBytes = len(encoded)
		selected[item.Path] = struct{}{}
	}
	omitUnreadable := func(err error) {
		if err != nil {
			index.Truncated = true
			index.OmittedCount++
		}
	}
	noteQuotaOmissions := func(total, limit int) {
		if total > limit {
			index.Truncated = true
			index.OmittedCount += total - limit
		}
	}
	markCategoryUnavailable := func() {
		index.Truncated = true
		index.OmittedCount++
	}

	if profile, ok, err := s.contextIndexRead("profile.md"); err != nil {
		omitUnreadable(err)
	} else if ok && contextStatusEligible(profile.record.Metadata.Status) {
		appendItem(contextIndexItem(profile.record, "profile", contextProfileSummaryRunes, true))
	}

	if project != "" {
		base := "recall/docs/projects/" + project
		for _, path := range []string{base + "/project.md", base + "/environment.md"} {
			candidate, ok, err := s.contextIndexRead(path)
			if err != nil {
				omitUnreadable(err)
				continue
			}
			if ok && contextStatusEligible(candidate.record.Metadata.Status) {
				appendItem(contextIndexItem(candidate.record, "project", contextProjectSummaryRunes, true))
			}
		}
	}

	// verified_fact 只从独立运维事实中补充。Runbook/Card 只作为“需要继续读取”的索引，
	// 不把它们的局部正文摘要误当成可以直接据此执行的完整事实。
	// ops 里的 verified fact 先完整读取候选 frontmatter 再筛选，不能按文件 mtime 预筛；
	// 文件复制或恢复可能重置 mtime，而稳定事实本来就可能长期不修改。
	verifiedFacts, skipped, err := s.contextIndexCandidates("recall/docs/ops", 0)
	if err != nil {
		markCategoryUnavailable()
		verifiedFacts = nil
	} else {
		index.OmittedCount += skipped
		if skipped > 0 {
			index.Truncated = true
		}
	}
	verifiedFacts = filterContextVerifiedFacts(verifiedFacts)
	sort.SliceStable(verifiedFacts, func(i, j int) bool {
		left, right := verifiedTime(verifiedFacts[i].record.Metadata), verifiedTime(verifiedFacts[j].record.Metadata)
		if !left.Equal(right) {
			return left.After(right)
		}
		return verifiedFacts[i].record.Path < verifiedFacts[j].record.Path
	})
	noteQuotaOmissions(len(verifiedFacts), contextVerifiedFactLimit)
	for i, candidate := range verifiedFacts {
		if i >= contextVerifiedFactLimit {
			break
		}
		appendItem(contextIndexItem(candidate.record, "verified_fact", contextFactSummaryRunes, true))
	}

	runbooks, skipped, err := s.contextIndexProjectRunbooks(project)
	if err != nil {
		markCategoryUnavailable()
		runbooks = nil
	} else {
		index.OmittedCount += skipped
		if skipped > 0 {
			index.Truncated = true
		}
	}
	sortContextCandidates(runbooks)
	noteQuotaOmissions(len(runbooks), contextRunbookLimit)
	for i, candidate := range runbooks {
		if i >= contextRunbookLimit {
			break
		}
		appendItem(contextIndexItem(candidate.record, "runbook", 0, false))
	}

	cards, skipped, err := s.contextIndexCards(project)
	if err != nil {
		markCategoryUnavailable()
		cards = nil
	} else {
		index.OmittedCount += skipped
		if skipped > 0 {
			index.Truncated = true
		}
	}
	sortContextCandidates(cards)
	noteQuotaOmissions(len(cards), contextCardLimit)
	for i, candidate := range cards {
		if i >= contextCardLimit {
			break
		}
		appendItem(contextIndexItem(candidate.record, "card", 0, false))
	}

	if index.TotalBytes == 0 {
		encoded, _ := json.Marshal(index.Items)
		index.TotalBytes = len(encoded)
	}
	return index, nil
}

func (s *Store) contextIndexRead(path string) (contextIndexCandidate, bool, error) {
	memory, err := s.Read(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return contextIndexCandidate{}, false, nil
		}
		return contextIndexCandidate{}, false, err
	}
	return contextIndexCandidate{record: recordFromRecall(memory)}, true, nil
}

func (s *Store) contextIndexCandidates(prefix string, prefetch int) ([]contextIndexCandidate, int, error) {
	entries, err := s.List(prefix, 1000)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []contextIndexCandidate{}, 0, nil
		}
		return nil, 0, err
	}
	entries = contextIndexFileEntries(entries)
	skipped := 0
	if prefetch > 0 && len(entries) > prefetch {
		skipped += len(entries) - prefetch
		entries = entries[:prefetch]
	}
	out := make([]contextIndexCandidate, 0, len(entries))
	for _, entry := range entries {
		memory, err := s.Read(entry.Path)
		if err != nil {
			skipped++
			continue
		}
		modified, _ := time.Parse(time.RFC3339, entry.Modified)
		out = append(out, contextIndexCandidate{record: recordFromRecall(memory), modified: modified})
	}
	return out, skipped, nil
}

func contextIndexFileEntries(entries []Entry) []Entry {
	files := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type == "file" && IsTextFile(entry.Path) {
			files = append(files, entry)
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Modified != files[j].Modified {
			return files[i].Modified > files[j].Modified
		}
		return files[i].Path < files[j].Path
	})
	return files
}

func (s *Store) contextIndexProjectRunbooks(project string) ([]contextIndexCandidate, int, error) {
	if project == "" {
		return []contextIndexCandidate{}, 0, nil
	}
	candidates, skipped, err := s.contextIndexCandidates("recall/docs/projects/"+project+"/runbooks", contextRunbookPrefetch)
	if err != nil {
		return nil, 0, err
	}
	out := candidates[:0]
	for _, candidate := range candidates {
		if contextStatusEligible(candidate.record.Metadata.Status) && strings.EqualFold(candidate.record.Metadata.Project, project) {
			out = append(out, candidate)
		}
	}
	return out, skipped, nil
}

func (s *Store) contextIndexCards(project string) ([]contextIndexCandidate, int, error) {
	projects := []string{"global"}
	if project != "" && !strings.EqualFold(project, "global") {
		projects = append([]string{project}, projects...)
	}

	entries := []Entry{}
	seenEntries := map[string]struct{}{}
	for _, cardProject := range projects {
		for _, status := range []Status{StatusVerified, StatusActive} {
			prefix := filepath.ToSlash(filepath.Join("recall", "managed", "cards", SafeSegment(cardProject), string(status)))
			listed, err := s.List(prefix, 1000)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, 0, err
			}
			for _, entry := range contextIndexFileEntries(listed) {
				if _, exists := seenEntries[entry.Path]; exists {
					continue
				}
				seenEntries[entry.Path] = struct{}{}
				entries = append(entries, entry)
			}
		}
	}
	entries = contextIndexFileEntries(entries)
	skipped := 0
	if len(entries) > contextCardPrefetch {
		skipped += len(entries) - contextCardPrefetch
		entries = entries[:contextCardPrefetch]
	}

	out := make([]contextIndexCandidate, 0, len(entries))
	for _, entry := range entries {
		memory, err := s.Read(entry.Path)
		if err != nil {
			skipped++
			continue
		}
		record := recordFromRecall(memory)
		record.Metadata.Status = contextCardStatus(memory.Frontmatter, entry.Path)
		if !contextStatusEligible(record.Metadata.Status) {
			continue
		}
		pathProject, _, _ := storedCardPathMetadata(entry.Path)
		cardProject := firstStoredCardValue(memory.Frontmatter["project"], pathProject, "global")
		if !strings.EqualFold(cardProject, project) && !strings.EqualFold(cardProject, "global") {
			continue
		}
		modified, _ := time.Parse(time.RFC3339, entry.Modified)
		out = append(out, contextIndexCandidate{record: record, modified: modified})
	}
	return out, skipped, nil
}

func contextCardStatus(frontmatter map[string]string, path string) Status {
	if status := Status(strings.ToLower(strings.TrimSpace(frontmatter["status"]))); status.Valid() {
		return status
	}
	_, pathStatus, _ := storedCardPathMetadata(path)
	status := Status(strings.ToLower(strings.TrimSpace(pathStatus)))
	if status.Valid() {
		return status
	}
	return ""
}

func filterContextVerifiedFacts(candidates []contextIndexCandidate) []contextIndexCandidate {
	out := make([]contextIndexCandidate, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		meta := candidate.record.Metadata
		if !contextStatusEligible(meta.Status) || meta.Verification.Confidence != ConfidenceHigh || meta.Verification.VerifiedAt == nil {
			continue
		}
		if _, exists := seen[candidate.record.Path]; exists {
			continue
		}
		seen[candidate.record.Path] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func sortContextCandidates(candidates []contextIndexCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		leftVerified, rightVerified := candidates[i].record.Metadata.Status == StatusVerified, candidates[j].record.Metadata.Status == StatusVerified
		if leftVerified != rightVerified {
			return leftVerified
		}
		left, right := contextCandidateTime(candidates[i]), contextCandidateTime(candidates[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return candidates[i].record.Path < candidates[j].record.Path
	})
}

func contextCandidateTime(candidate contextIndexCandidate) time.Time {
	if verified := verifiedTime(candidate.record.Metadata); !verified.IsZero() {
		return verified
	}
	for _, key := range []string{"updated_at", "created_at"} {
		if raw := strings.TrimSpace(candidate.record.Frontmatter[key]); raw != "" {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				return parsed
			}
		}
	}
	return candidate.modified
}

func contextStatusEligible(status Status) bool {
	return status == StatusActive || status == StatusVerified
}

func contextIndexItem(record Record, kind string, summaryRunes int, withSummary bool) ContextIndexItem {
	item := ContextIndexItem{
		Kind:     kind,
		Path:     record.Path,
		Title:    truncateContextRunes(firstMarkdownTitle(record.Body), 120),
		Keywords: compactContextLabels(frontmatterList(record.Frontmatter, "keywords")),
		Aliases:  compactContextLabels(frontmatterList(record.Frontmatter, "aliases")),
		Status:   record.Metadata.Status,
	}
	if confidence := record.Metadata.Verification.Confidence; confidence != "" && confidence != ConfidenceUnknown {
		item.Confidence = string(confidence)
	}
	if item.Title == "" {
		item.Title = truncateContextRunes(strings.TrimSuffix(filepath.Base(record.Path), filepath.Ext(record.Path)), 120)
	}
	if record.Metadata.Verification.VerifiedAt != nil {
		item.VerifiedAt = record.Metadata.Verification.VerifiedAt.Format(time.RFC3339)
	}
	if kind == "card" {
		item.CardType = truncateContextRunes(strings.TrimSpace(record.Frontmatter["card_type"]), 40)
		item.Tags = compactContextLabels(splitStoredCardTags(record.Frontmatter["tags"]))
	}
	if withSummary && summaryRunes > 0 {
		item.Summary = contextSummary(record.Recall, summaryRunes)
	}
	return item
}

func compactContextLabels(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(values), 6))
	for _, value := range values {
		value = truncateContextRunes(value, 40)
		if value == "" {
			continue
		}
		out = append(out, value)
		if len(out) >= 6 {
			break
		}
	}
	return out
}

func contextSummary(memory Recall, maxRunes int) string {
	if summary := strings.TrimSpace(memory.Frontmatter["summary"]); summary != "" {
		return truncateContextRunes(summary, maxRunes)
	}

	headings := []string{}
	for _, line := range strings.Split(memory.Body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			if heading := strings.TrimSpace(strings.TrimPrefix(line, "## ")); heading != "" {
				headings = append(headings, heading)
				if len(headings) >= 5 {
					break
				}
			}
		}
	}
	if len(headings) > 0 {
		return truncateContextRunes(strings.Join(headings, "；"), maxRunes)
	}

	paragraph := []string{}
	for _, line := range strings.Split(memory.Body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		paragraph = append(paragraph, line)
	}
	return truncateContextRunes(strings.Join(paragraph, " "), maxRunes)
}

func truncateContextRunes(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 || value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes]))
}
