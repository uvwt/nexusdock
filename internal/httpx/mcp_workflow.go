package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const workflowCompositionNextAction = "Combine these templates for the current user goal: prune irrelevant steps, deduplicate, order the remaining steps, and merge completion conditions. Then call task_manage create with source_template_ids, composed steps, and completion_conditions."

func (s *Server) callWorkflowTemplateManage(ctx context.Context, args map[string]any) (map[string]any, error) {
	action := strings.ToLower(stringArgument(args, "action"))
	switch action {
	case "publish":
		var template workflowTemplate
		raw, ok := args["template"].(map[string]any)
		if !ok {
			return nil, errors.New("template is required")
		}
		if err := decodeMap(raw, &template); err != nil {
			return nil, fmt.Errorf("decode workflow template: %w", err)
		}
		if _, exists := args["allow_long_template"]; exists {
			template.AllowLongTemplate = boolArgument(args, "allow_long_template")
		}
		if reason := stringArgument(args, "long_template_reason"); reason != "" {
			template.LongTemplateReason = reason
		}
		published, err := s.publishWorkflowTemplateValue(template)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok": true, "action": action, "template_id": published.ID,
			"template_summary": s.workflowTemplateSummary(published), "source": "nexus-registry",
		}, nil

	case "retire":
		id, version := stringArgument(args, "template_id"), stringArgument(args, "template_version")
		if id == "" || version == "" {
			return nil, errors.New("template_id and template_version are required")
		}
		retired, err := s.retireWorkflowTemplateValue(id, version)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok": true, "action": action, "template_id": retired.ID,
			"template_summary": s.workflowTemplateSummary(retired), "source": "nexus-registry",
		}, nil

	case "get":
		id := stringArgument(args, "template_id")
		if id == "" {
			return nil, errors.New("template_id is required")
		}
		var template workflowTemplate
		var err error
		if version := stringArgument(args, "template_version"); version != "" {
			template, err = s.getWorkflowTemplate(id, version)
		} else {
			template, err = s.activeWorkflowTemplate(id)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok": true, "action": action, "template": template,
			"template_summary": s.workflowTemplateSummary(template), "source": "nexus-registry",
		}, nil

	case "get_many":
		ids, err := workflowTemplateIDsArgument(args, "template_ids")
		if err != nil {
			return nil, err
		}
		if len(ids) < 2 || len(ids) > 3 {
			return nil, errors.New("template_ids must contain 2 or 3 distinct ids")
		}
		templates := make([]workflowTemplate, 0, len(ids))
		for _, id := range ids {
			template, err := s.activeWorkflowTemplate(id)
			if err != nil {
				return nil, err
			}
			templates = append(templates, template)
		}
		return map[string]any{
			"ok": true, "action": action, "templates": templates, "count": len(templates),
			"composition_required": true, "next_required_action": workflowCompositionNextAction,
			"source": "nexus-registry",
		}, nil

	case "list":
		status := workflowTemplateStatus(stringArgument(args, "template_status"))
		if status != "" && status != workflowTemplateActive && status != workflowTemplateRetired {
			return nil, errors.New("template_status must be active or retired")
		}
		templates, err := s.listWorkflowTemplates(status)
		if err != nil {
			return nil, err
		}
		summaries := make([]workflowTemplateSummary, 0, len(templates))
		for _, template := range templates {
			summaries = append(summaries, s.workflowTemplateSummary(template))
		}
		if status == "" {
			summaries = currentWorkflowTemplates(summaries)
		}
		return map[string]any{
			"ok": true, "action": action, "templates": summaries, "count": len(summaries),
			"workflow_dir": s.workflowRegistryRoot(), "source": "nexus-registry",
		}, nil

	case "match":
		return s.workflowTemplateMatchResult(ctx, stringArgument(args, "goal"), stringArgument(args, "device"), stringArgument(args, "type"))

	case "vector_index":
		result, err := s.workflowTemplateVectorIndexResult()
		if err != nil {
			return nil, err
		}
		result["action"] = action
		if available, exists := result["available"]; exists {
			result["vector_index_available"] = available
			delete(result, "available")
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unsupported workflow_template_manage action: %s", action)
	}
}

func (s *Server) activeWorkflowTemplate(id string) (workflowTemplate, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return workflowTemplate{}, errors.New("template_id is required")
	}
	templates, err := s.listWorkflowTemplates(workflowTemplateActive)
	if err != nil {
		return workflowTemplate{}, err
	}
	for _, template := range templates {
		if template.ID == id {
			return template, nil
		}
	}
	return workflowTemplate{}, fmt.Errorf("active workflow template %s not found", id)
}

func workflowTemplateIDsArgument(args map[string]any, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok {
		return nil, errors.New("template_ids are required")
	}
	var values []string
	if err := decodeMapValue(raw, &values); err != nil {
		return nil, errors.New("template_ids must be an array of strings")
	}
	seen := make(map[string]struct{}, len(values))
	ids := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ids = append(ids, value)
	}
	return ids, nil
}

func decodeMapValue(value any, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func (s *Server) workflowTemplateMatchResult(ctx context.Context, goal, device, taskType string) (map[string]any, error) {
	candidates, err := s.matchWorkflowTemplates(ctx, goal, device, taskType)
	if err != nil {
		return nil, err
	}
	cfg := s.currentAIConfig()
	vectorStatus, vectorItems := s.workflowTemplateVectorIndexInfoForConfig(cfg)
	result := map[string]any{
		"ok": true, "action": "match", "candidates": candidates, "count": len(candidates),
		"workflow_dir": s.workflowRegistryRoot(), "root": s.workflowRegistryRoot(), "source": "nexus-registry",
		"vector_search_enabled": workflowTemplateVectorEnabled(cfg), "vector_index_status": vectorStatus,
		"vector_index_items": vectorItems, "embedding_model": cfg.EmbeddingModel,
	}
	for key, value := range workflowMatchRecommendation(candidates) {
		result[key] = value
	}
	return result, nil
}

func (s *Server) workflowTemplateVectorIndexResult() (map[string]any, error) {
	cfg := s.currentAIConfig()
	if !workflowTemplateVectorEnabled(cfg) {
		return map[string]any{"ok": true, "available": false, "source": "nexus-registry", "vector_index_status": "not_configured"}, nil
	}
	data, err := os.ReadFile(s.workflowTemplateVectorIndexPath())
	if err != nil {
		return map[string]any{"ok": true, "available": false, "source": "nexus-registry", "vector_index_status": "missing"}, nil
	}
	idx, err := decodeWorkflowTemplateVectorIndex(data, cfg.EmbeddingModel)
	if errors.Is(err, errWorkflowVectorIndexStale) {
		return map[string]any{"ok": true, "available": false, "source": "nexus-registry", "vector_index_status": "stale", "embedding_model": cfg.EmbeddingModel}, nil
	}
	if err != nil {
		return nil, err
	}
	info, _ := os.Stat(s.workflowTemplateVectorIndexPath())
	return map[string]any{
		"ok": true, "available": true, "source": "nexus-registry",
		"file_name": "vector-index.json", "path": "workflow-templates/vector-index.json",
		"size_bytes": fileSize(info), "updated_at": modTime(info), "content": string(data),
		"vector_index_status": "ready", "vector_index_items": len(idx.Documents),
		"embedding_model": idx.Model, "dimension": idx.Dimension,
	}, nil
}
