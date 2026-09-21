// Package workflow 承载 NexusDock 自有 Workflow 模板注册表的完整业务：
// 模板模型与校验、published 文件持久化、发布/退役/回滚生命周期、
// 版本比较、match 打分与向量索引。HTTP 与 MCP 只做各自边界的映射，
// 统一调用本包的 Registry。
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Status 是模板生命周期状态；发布即 active，只有被新版本替换或显式退役才变成 retired。
type Status string

const (
	StatusActive  Status = "active"
	StatusRetired Status = "retired"
)

// MatchRule 描述模板与用户目标之间的匹配线索，全部字段可选。
type MatchRule struct {
	Keywords []string `json:"keywords,omitempty"`
	Devices  []string `json:"devices,omitempty"`
	Type     string   `json:"type,omitempty"`
}

// Step 是模板中的一个执行步骤；Phase 用于把步骤归入检查/执行/验证/收尾阶段。
type Step struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Phase string `json:"phase"`
}

// Contract 描述一个 Workflow 与相邻 Skill/能力之间的稳定边界。
// 它不是执行器：模型仍负责选择真实工具并按步骤执行。
type Contract struct {
	Inputs           []string `json:"inputs"`
	Outputs          []string `json:"outputs"`
	Authority        []string `json:"authority"`
	ForbiddenChanges []string `json:"forbidden_changes"`
	Validation       []string `json:"validation"`
	NextSkill        string   `json:"next_skill"`
}

// Template 是 published 模板文件的完整结构。文件使用严格 JSON 解码，
// 出现未知字段直接报错，避免模板内容被悄悄丢弃。
type Template struct {
	ID                   string     `json:"id"`
	Version              string     `json:"version"`
	Title                string     `json:"title"`
	Description          string     `json:"description,omitempty"`
	SourceEvolutionID    string     `json:"source_evolution_id,omitempty"`
	Status               Status     `json:"status"`
	Match                MatchRule  `json:"match,omitempty"`
	CompletionConditions []string   `json:"completion_conditions"`
	Steps                []Step     `json:"steps"`
	Contract             *Contract  `json:"contract,omitempty"`
	AllowLongTemplate    bool       `json:"allow_long_template,omitempty"`
	LongTemplateReason   string     `json:"long_template_reason,omitempty"`
	Hash                 string     `json:"hash,omitempty"`
	PublishedAt          *time.Time `json:"published_at,omitempty"`
	RetiredAt            *time.Time `json:"retired_at,omitempty"`
}

// Candidate 是一次 match 命中的候选模板与打分理由；Reason 供模型自查命中原因。
type Candidate struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Score   int    `json:"score"`
	Reason  string `json:"reason"`
}

// validateTemplate 在发布入口执行全部结构校验与反 SOP guardrails。
func validateTemplate(t Template) error {
	if !ValidToken(t.ID) || !ValidToken(t.Version) {
		return errors.New("template id and version must contain only letters, numbers, dot, dash, or underscore")
	}
	if strings.TrimSpace(t.Title) == "" {
		return errors.New("template title is required")
	}
	if t.SourceEvolutionID != "" && !validEvolutionID(t.SourceEvolutionID) {
		return errors.New("source_evolution_id must be a valid evo_ identifier")
	}
	if len(normalizeTexts(t.CompletionConditions)) == 0 {
		return errors.New("template requires at least one completion condition")
	}
	if len(t.Steps) == 0 {
		return errors.New("template requires at least one step")
	}
	if err := validateContract(t.Contract); err != nil {
		return err
	}
	if err := validateTemplateGuardrails(t); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, step := range t.Steps {
		if !ValidToken(step.ID) || strings.TrimSpace(step.Title) == "" {
			return fmt.Errorf("invalid template step %q", step.ID)
		}
		if !validPhase(step.Phase) {
			return fmt.Errorf("step %s has invalid phase %q", step.ID, step.Phase)
		}
		if seen[step.ID] {
			return fmt.Errorf("duplicate step id %q", step.ID)
		}
		seen[step.ID] = true
	}
	return nil
}

const maxTemplateSteps = 8
const maxTemplateConditions = 10

// sopTemplateTerms 拦截“逐条命令、证据账本”这类啰嗦 SOP 用语：
// 模板应保持精炼，细节必须下沉到 script/Skill/runbook，否则模型会被带偏。
var sopTemplateTerms = []string{"每条命令", "每个命令", "逐命令", "逐条命令", "记录证据", "补充证据", "再次记录", "逐项记录", "证据账本", "详细证据", "每一步", "每个步骤", "逐来源", "逐条", "分别记录"}

func validateTemplateGuardrails(t Template) error {
	if t.AllowLongTemplate && len([]rune(strings.TrimSpace(t.LongTemplateReason))) < 12 {
		return errors.New("long_template_reason is required when allow_long_template=true")
	}
	if !t.AllowLongTemplate && len(t.Steps) > maxTemplateSteps {
		return fmt.Errorf("template has %d steps; max %d unless allow_long_template=true with long_template_reason", len(t.Steps), maxTemplateSteps)
	}
	if !t.AllowLongTemplate && len(normalizeTexts(t.CompletionConditions)) > maxTemplateConditions {
		return fmt.Errorf("template has %d completion conditions; max %d unless allow_long_template=true with long_template_reason", len(normalizeTexts(t.CompletionConditions)), maxTemplateConditions)
	}
	texts := []string{t.Title, t.Description, t.LongTemplateReason}
	texts = append(texts, t.CompletionConditions...)
	for _, step := range t.Steps {
		texts = append(texts, step.ID, step.Title)
	}
	if t.Contract != nil {
		texts = append(texts, t.Contract.Inputs...)
		texts = append(texts, t.Contract.Outputs...)
		texts = append(texts, t.Contract.Authority...)
		texts = append(texts, t.Contract.ForbiddenChanges...)
		texts = append(texts, t.Contract.Validation...)
		texts = append(texts, t.Contract.NextSkill)
	}
	for _, text := range texts {
		for _, term := range sopTemplateTerms {
			if strings.Contains(text, term) {
				return fmt.Errorf("template text looks like verbose SOP or evidence ledger; move details to script/Skill/runbook instead of using term %q", term)
			}
		}
	}
	return nil
}

func validEvolutionID(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "evo_") || len(value) < len("evo_")+16 || len(value) > len("evo_")+64 {
		return false
	}
	for _, r := range value[len("evo_"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validPhase(value string) bool {
	switch value {
	case "check", "execute", "verify", "closeout":
		return true
	default:
		return false
	}
}

func validateContract(contract *Contract) error {
	if contract == nil {
		return nil
	}
	fields := []struct {
		name   string
		values []string
	}{
		{"inputs", contract.Inputs},
		{"outputs", contract.Outputs},
		{"authority", contract.Authority},
		{"forbidden_changes", contract.ForbiddenChanges},
		{"validation", contract.Validation},
	}
	for _, field := range fields {
		values := normalizeTexts(field.values)
		if len(values) == 0 {
			return fmt.Errorf("workflow contract %s requires at least one non-empty item", field.name)
		}
		if len(values) > 16 {
			return fmt.Errorf("workflow contract %s has %d items; max 16", field.name, len(values))
		}
	}
	nextSkill := strings.TrimSpace(contract.NextSkill)
	if nextSkill == "" {
		return errors.New("workflow contract next_skill is required")
	}
	if nextSkill != "none" && !ValidToken(nextSkill) {
		return errors.New("workflow contract next_skill must be a valid token or none")
	}
	return nil
}

// ValidToken 既是服务内部的 id/version 校验，也暴露给 HTTP 边界做同样的前置校验，
// 保证两个入口对“非法标识”的判定完全一致。
func ValidToken(v string) bool {
	if strings.TrimSpace(v) == "" {
		return false
	}
	for _, r := range v {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// templateHash 覆盖除自身 hash 与发布/退役时间以外的全部内容，
// 使同一份模板内容总能得到稳定哈希，用于向量索引与文件的一致性核对。
func templateHash(t Template) string {
	t.Hash = ""
	t.PublishedAt = nil
	t.RetiredAt = nil
	data, _ := json.Marshal(t)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeTexts(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

// LatestVersions 从（可能包含多版本的）模板列表中挑出每个 ID 的最高版本，
// 供 match、向量重建与 context 汇总使用，避免旧版本参与打分。
func LatestVersions(templates []Template) []Template {
	byID := map[string]Template{}
	for _, template := range templates {
		current, exists := byID[template.ID]
		if !exists || CompareVersions(template.Version, current.Version) > 0 {
			byID[template.ID] = template
		}
	}
	out := make([]Template, 0, len(byID))
	for _, template := range byID {
		out = append(out, template)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return CompareVersions(out[i].Version, out[j].Version) > 0
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// CompareVersions 按三段数字版本号比较；模板版本必须形如 1.2.3，
// 缺失段按 0 处理，解析失败的段按 0 处理以保持排序稳定。
// HTTP/MCP 的版本视图（当前版本挑选、排序）也依赖同一套比较规则，
// 因此导出供边界复用，避免两处实现漂移。
func CompareVersions(a, b string) int {
	pa := parseVersion(a)
	pb := parseVersion(b)
	for i := 0; i < len(pa); i++ {
		if pa[i] > pb[i] {
			return 1
		}
		if pa[i] < pb[i] {
			return -1
		}
	}
	return 0
}

func parseVersion(value string) [3]int {
	var result [3]int
	parts := strings.Split(value, ".")
	for i := 0; i < len(result) && i < len(parts); i++ {
		parsed, _ := strconv.Atoi(parts[i])
		result[i] = parsed
	}
	return result
}
