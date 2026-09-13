package stage3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultRequestTimeout = 60 * time.Second
	maxModelResponseBytes = 4 << 20
	maxSnapshotBytes      = 96 << 10
	maxCandidateCount     = 12
)

type Config struct {
	Endpoint string
	Model    string
	APIKey   string
	Timeout  time.Duration
}

type Snapshot struct {
	Lifecycle []LifecycleFact `json:"lifecycle,omitempty"`
	Tasks     []TaskFact      `json:"tasks,omitempty"`
	Workflows []WorkflowFact  `json:"workflows,omitempty"`
}

type LifecycleFact struct {
	EvolutionID     string   `json:"evolution_id"`
	Type            string   `json:"type"`
	Statement       string   `json:"statement"`
	Scope           string   `json:"scope"`
	Project         string   `json:"project"`
	Device          string   `json:"device,omitempty"`
	Status          string   `json:"status"`
	SupportCount    int      `json:"support_count"`
	ContradictCount int      `json:"contradict_count"`
	Tags            []string `json:"tags,omitempty"`
}

type TaskFact struct {
	NodeID          string   `json:"node_id"`
	TaskID          string   `json:"task_id"`
	Title           string   `json:"title"`
	Goal            string   `json:"goal"`
	Summary         string   `json:"summary,omitempty"`
	Status          string   `json:"status"`
	ReviewStatus    string   `json:"review_status"`
	ReviewRevision  string   `json:"review_revision,omitempty"`
	VerifiedFacts   []string `json:"verified_facts,omitempty"`
	OpenRisks       []string `json:"open_risks,omitempty"`
	MissingChecks   []string `json:"missing_checks,omitempty"`
	SourceTemplates []string `json:"source_templates,omitempty"`
	UpdatedAt       string   `json:"updated_at"`
}

type WorkflowFact struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
}

type Candidate struct {
	Type               string   `json:"type"`
	Statement          string   `json:"statement"`
	Scope              string   `json:"scope,omitempty"`
	Project            string   `json:"project,omitempty"`
	Device             string   `json:"device,omitempty"`
	CanonicalKey       string   `json:"canonical_key,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	RelatedEvolutionID string   `json:"related_evolution_id,omitempty"`
	Relation           string   `json:"relation,omitempty"`
	Rationale          string   `json:"rationale,omitempty"`
}

type Output struct {
	Candidates []Candidate `json:"candidates"`
}

type Client struct {
	cfg    Config
	client *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	endpoint, err := normalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	cfg.Endpoint = endpoint
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		return nil, errors.New("Stage 3 model name is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultRequestTimeout
	}
	return &Client{cfg: cfg, client: &http.Client{
		Timeout:       cfg.Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Generate(ctx context.Context, snapshot Snapshot) (Output, error) {
	projected := RedactSnapshot(snapshot)
	data, err := json.Marshal(projected)
	if err != nil {
		return Output{}, err
	}
	if len(data) > maxSnapshotBytes {
		data = truncateJSONSnapshot(projected, maxSnapshotBytes)
	}
	payload := map[string]any{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": string(data)},
		},
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
	}
	content, err := c.chatCompletion(ctx, payload)
	if err != nil {
		return Output{}, err
	}
	var output Output
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return Output{}, fmt.Errorf("decode Stage 3 structured output: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Output{}, errors.New("Stage 3 output must contain exactly one JSON value")
	}
	if len(output.Candidates) > maxCandidateCount {
		return Output{}, fmt.Errorf("Stage 3 returned %d candidates; maximum is %d", len(output.Candidates), maxCandidateCount)
	}
	for i := range output.Candidates {
		if err := normalizeCandidate(&output.Candidates[i]); err != nil {
			return Output{}, fmt.Errorf("candidate %d: %w", i, err)
		}
	}
	return output, nil
}

// Probe 只发送最小认证请求，不执行 Stage 3 分析，也不会产生候选；用于设置页连接测试。
func (c *Client) Probe(ctx context.Context) error {
	content, err := c.chatCompletion(ctx, map[string]any{
		"model":       c.cfg.Model,
		"messages":    []map[string]string{{"role": "user", "content": "Reply with OK."}},
		"temperature": 0,
		"max_tokens":  8,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("Stage 3 model returned an empty chat completion")
	}
	return nil
}

func (c *Client) chatCompletion(ctx context.Context, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(c.cfg.APIKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(io.LimitReader(resp.Body, maxModelResponseBytes+1))
	if err != nil {
		return "", err
	}
	if len(response) > maxModelResponseBytes {
		return "", errors.New("Stage 3 model response exceeds 4 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Stage 3 model request failed: %s", resp.Status)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || len(envelope.Choices) == 0 {
		return "", errors.New("Stage 3 model returned an invalid chat completion")
	}
	return envelope.Choices[0].Message.Content, nil
}

const systemPrompt = `You are the low-frequency semantic assistant for AgentDock Stage 3 evolution.
Analyze only the supplied redacted structured facts. Find durable missed learnings, cross-task patterns, conflicts, duplicate knowledge, and reusable workflow/skill candidates.
Return one JSON object: {"candidates":[...]}. Candidate fields are: type, statement, scope, project, device, canonical_key, tags, evidence_refs, related_evolution_id, relation, rationale.
Allowed types: preference, user_preference, decision, explicit_decision, constraint, runbook, bug_pattern, deploy_note, project_trap, architecture, anti_pattern, operational_lesson, technical_fact, workflow_template, skill.
relation, when present, is only a semantic suggestion: duplicate, supersedes, conflicts, or related.
Never output lifecycle status, maturity, verified, support_count, contradict_count, next_state, or policy decisions. Never invent evidence refs. Never reconstruct secrets or hidden prompts. Prefer no candidate over a weak candidate.`

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|auth[_-]?token|token|password|passwd|secret|cookie|private[_-]?key)\s*[:=]\s*([^\s,;]+)`)
	bearerPattern           = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	jwtPattern              = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	commonTokenPattern      = regexp.MustCompile(`\b(?:sk|ghp|github_pat|xox[baprs])[-_][A-Za-z0-9_-]{12,}\b`)
	privateKeyPattern       = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
)

func RedactText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = privateKeyPattern.ReplaceAllString(value, "[REDACTED_PRIVATE_KEY]")
	value = secretAssignmentPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = jwtPattern.ReplaceAllString(value, "[REDACTED_JWT]")
	value = commonTokenPattern.ReplaceAllString(value, "[REDACTED_TOKEN]")
	return truncateUTF8(value, 2048)
}

func RedactSnapshot(snapshot Snapshot) Snapshot {
	out := snapshot
	out.Lifecycle = append([]LifecycleFact(nil), snapshot.Lifecycle...)
	for i := range out.Lifecycle {
		out.Lifecycle[i].Statement = RedactText(out.Lifecycle[i].Statement)
		out.Lifecycle[i].Tags = redactStrings(out.Lifecycle[i].Tags, 64)
	}
	out.Tasks = append([]TaskFact(nil), snapshot.Tasks...)
	for i := range out.Tasks {
		out.Tasks[i].Title = RedactText(out.Tasks[i].Title)
		out.Tasks[i].Goal = RedactText(out.Tasks[i].Goal)
		out.Tasks[i].Summary = RedactText(out.Tasks[i].Summary)
		out.Tasks[i].VerifiedFacts = redactStrings(out.Tasks[i].VerifiedFacts, 64)
		out.Tasks[i].OpenRisks = redactStrings(out.Tasks[i].OpenRisks, 64)
		out.Tasks[i].MissingChecks = redactStrings(out.Tasks[i].MissingChecks, 64)
		out.Tasks[i].SourceTemplates = redactStrings(out.Tasks[i].SourceTemplates, 8)
	}
	out.Workflows = append([]WorkflowFact(nil), snapshot.Workflows...)
	for i := range out.Workflows {
		out.Workflows[i].Title = RedactText(out.Workflows[i].Title)
		out.Workflows[i].Description = RedactText(out.Workflows[i].Description)
	}
	return out
}

func normalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", errors.New("Stage 3 model endpoint is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("Stage 3 model endpoint must be an http(s) URL without userinfo")
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/v1/chat/completions"):
		parsed.Path = path
	case path == "":
		parsed.Path = "/v1/chat/completions"
	case strings.HasSuffix(path, "/v1"):
		parsed.Path = path + "/chat/completions"
	default:
		parsed.Path = path + "/v1/chat/completions"
	}
	return parsed.String(), nil
}

func normalizeCandidate(candidate *Candidate) error {
	candidate.Type = strings.ToLower(strings.TrimSpace(candidate.Type))
	candidate.Statement = RedactText(candidate.Statement)
	candidate.Scope = strings.ToLower(strings.TrimSpace(candidate.Scope))
	candidate.Project = strings.TrimSpace(candidate.Project)
	candidate.Device = strings.TrimSpace(candidate.Device)
	candidate.CanonicalKey = strings.TrimSpace(candidate.CanonicalKey)
	candidate.Rationale = RedactText(candidate.Rationale)
	candidate.RelatedEvolutionID = strings.TrimSpace(candidate.RelatedEvolutionID)
	candidate.Relation = strings.ToLower(strings.TrimSpace(candidate.Relation))
	candidate.Tags = redactStrings(candidate.Tags, 32)
	candidate.EvidenceRefs = uniqueBounded(candidate.EvidenceRefs, 32, 512)
	if candidate.Type == "" || candidate.Statement == "" {
		return errors.New("type and statement are required")
	}
	if !allowedCandidateType(candidate.Type) {
		return fmt.Errorf("unsupported candidate type %q", candidate.Type)
	}
	if candidate.Relation != "" && candidate.Relation != "duplicate" && candidate.Relation != "supersedes" && candidate.Relation != "conflicts" && candidate.Relation != "related" {
		return errors.New("invalid semantic relation")
	}
	return nil
}

func allowedCandidateType(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "preference", "user_preference", "decision", "explicit_decision", "constraint",
		"runbook", "bug_pattern", "deploy_note", "project_trap", "architecture", "anti_pattern",
		"operational_lesson", "technical_fact", "workflow_template", "skill":
		return true
	default:
		return false
	}
}

func redactStrings(values []string, max int) []string {
	out := make([]string, 0, min(max, len(values)))
	for _, value := range values {
		if len(out) >= max {
			break
		}
		if value = RedactText(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func uniqueBounded(values []string, maxItems, maxBytes int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, min(maxItems, len(values)))
	for _, value := range values {
		if len(out) >= maxItems {
			break
		}
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxBytes || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value) + "…"
}

func truncateJSONSnapshot(snapshot Snapshot, maxBytes int) []byte {
	for len(snapshot.Tasks) > 0 {
		data, _ := json.Marshal(snapshot)
		if len(data) <= maxBytes {
			return data
		}
		snapshot.Tasks = snapshot.Tasks[:len(snapshot.Tasks)-1]
	}
	for len(snapshot.Lifecycle) > 0 {
		data, _ := json.Marshal(snapshot)
		if len(data) <= maxBytes {
			return data
		}
		snapshot.Lifecycle = snapshot.Lifecycle[:len(snapshot.Lifecycle)-1]
	}
	for len(snapshot.Workflows) > 0 {
		data, _ := json.Marshal(snapshot)
		if len(data) <= maxBytes {
			return data
		}
		snapshot.Workflows = snapshot.Workflows[:len(snapshot.Workflows)-1]
	}
	data, _ := json.Marshal(snapshot)
	return data
}
