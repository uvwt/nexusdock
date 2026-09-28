package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/uvwt/nexusdock/internal/workflow"
)

// 这些结构体是 MCP JSON 边界进入 Nexus 编排层后的明确输入模型。
// 动态 JSON 只能存在于 decode* 函数内部；编排函数不得继续接收 map[string]any。
type recallSearchInput struct {
	Query      string `json:"query"`
	Kind       string `json:"kind"`
	MaxResults int    `json:"max_results"`
}

type recallReadInput struct {
	Path       string `json:"path"`
	IncludeRaw bool   `json:"include_raw"`
}

type recallWriteInput struct {
	Target          string
	Action          string
	Confirmed       bool
	DryRun          bool
	Path            string
	Content         string
	Title           string
	Summary         string
	Overwrite       bool
	AllowWarnings   bool
	Old             string
	New             string
	Append          string
	Section         string
	SectionContent  string
	Key             string
	Value           string
	Facts           map[string]string
	AppendIfMissing bool
	MaxBytes        int
}

type recallMaintainInput struct {
	Action      string   `json:"action"`
	Prefix      string   `json:"prefix"`
	Terms       []string `json:"terms"`
	Regex       bool     `json:"regex"`
	MaxEntries  int      `json:"max_entries"`
	MaxFindings int      `json:"max_findings"`
	MaxResults  int      `json:"max_results"`
}

type privateNoteManageInput struct {
	Action            string   `json:"action"`
	Query             string   `json:"query"`
	MaxResults        int      `json:"max_results"`
	Path              string   `json:"path"`
	Category          string   `json:"category"`
	Title             string   `json:"title"`
	Summary           string   `json:"summary"`
	Tags              []string `json:"tags"`
	Content           string   `json:"content"`
	Confirmed         bool     `json:"confirmed"`
	Overwrite         bool     `json:"overwrite"`
	MaxBytes          int      `json:"max_bytes"`
	StatusAction      string   `json:"status_action"`
	MaintenanceAction string   `json:"maintenance_action"`
}

type workflowTemplateManageInput struct {
	Action             string             `json:"action"`
	Template           *workflow.Template `json:"template"`
	TemplateID         string             `json:"template_id"`
	TemplateIDs        []string           `json:"template_ids"`
	TemplateVersion    string             `json:"template_version"`
	TemplateStatus     string             `json:"template_status"`
	AllowLongTemplate  *bool              `json:"allow_long_template"`
	LongTemplateReason string             `json:"long_template_reason"`
	Goal               string             `json:"goal"`
	Device             string             `json:"device"`
	Type               string             `json:"type"`
}

type recallWriteWireInput struct {
	Target          string                     `json:"target"`
	Action          string                     `json:"action"`
	Confirmed       bool                       `json:"confirmed"`
	DryRun          bool                       `json:"dry_run"`
	Path            string                     `json:"path"`
	Content         string                     `json:"content"`
	Title           string                     `json:"title"`
	Summary         string                     `json:"summary"`
	Overwrite       bool                       `json:"overwrite"`
	AllowWarnings   bool                       `json:"allow_warnings"`
	Old             string                     `json:"old"`
	New             string                     `json:"new"`
	Append          string                     `json:"append"`
	Section         string                     `json:"section"`
	SectionContent  string                     `json:"section_content"`
	Key             string                     `json:"key"`
	Value           string                     `json:"value"`
	Facts           map[string]json.RawMessage `json:"facts"`
	AppendIfMissing bool                       `json:"append_if_missing"`
	MaxBytes        int                        `json:"max_bytes"`
}

func toolArgumentsJSON(requestRaw json.RawMessage) json.RawMessage {
	if len(requestRaw) == 0 || string(requestRaw) == "null" {
		return json.RawMessage("{}")
	}
	return requestRaw
}

func decodeToolInput(raw json.RawMessage, destination any) error {
	raw = toolArgumentsJSON(raw)
	if err := json.Unmarshal(raw, destination); err != nil {
		return errors.New("tool arguments must match the declared JSON object")
	}
	return nil
}

func decodeRecallWriteInput(raw json.RawMessage) (recallWriteInput, error) {
	var wire recallWriteWireInput
	if err := decodeToolInput(raw, &wire); err != nil {
		return recallWriteInput{}, err
	}
	facts := make(map[string]string, len(wire.Facts))
	for key, rawValue := range wire.Facts {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		var value any
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return recallWriteInput{}, fmt.Errorf("decode fact %q: %w", key, err)
		}
		facts[key] = fmt.Sprint(value)
	}
	return recallWriteInput{
		Target: strings.TrimSpace(wire.Target), Action: strings.TrimSpace(wire.Action),
		Confirmed: wire.Confirmed, DryRun: wire.DryRun, Path: wire.Path, Content: wire.Content,
		Title: wire.Title, Summary: wire.Summary, Overwrite: wire.Overwrite, AllowWarnings: wire.AllowWarnings,
		Old: wire.Old, New: wire.New, Append: wire.Append, Section: wire.Section,
		SectionContent: wire.SectionContent, Key: wire.Key, Value: wire.Value, Facts: facts,
		AppendIfMissing: wire.AppendIfMissing, MaxBytes: wire.MaxBytes,
	}, nil
}

func normalizedPositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
