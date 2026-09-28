package httpx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/uvwt/nexusdock/internal/workflow"
)

const workflowCompositionNextAction = "Combine these templates for the current user goal: prune irrelevant steps, deduplicate, order the remaining steps, and merge completion conditions. Then call task_manage create with source_template_ids, composed steps, and completion_conditions."

// callWorkflowTemplateManage 只负责 MCP 动作分派和响应形状；
// Publish/Retire/Get/List/Match/VectorIndex 的业务路径与 REST 共用 workflow operations。
func (s *Server) callWorkflowTemplateManage(ctx context.Context, input workflowTemplateManageInput) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "publish":
		if input.Template == nil {
			return nil, errors.New("template is required")
		}
		template := *input.Template
		if input.AllowLongTemplate != nil {
			template.AllowLongTemplate = *input.AllowLongTemplate
		}
		if reason := strings.TrimSpace(input.LongTemplateReason); reason != "" {
			template.LongTemplateReason = reason
		}
		result, err := s.executeWorkflowTemplatePublish(template)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok": true, "action": action, "template_id": result.Template.ID,
			"template_summary": result.Summary, "source": "nexus-registry",
		}, nil

	case "retire":
		id, version := strings.TrimSpace(input.TemplateID), strings.TrimSpace(input.TemplateVersion)
		if id == "" || version == "" {
			return nil, errors.New("template_id and template_version are required")
		}
		result, err := s.executeWorkflowTemplateRetire(id, version)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok": true, "action": action, "template_id": result.Template.ID,
			"template_summary": result.Summary, "source": "nexus-registry",
		}, nil

	case "get":
		result, err := s.executeWorkflowTemplateGet(input.TemplateID, input.TemplateVersion)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok": true, "action": action, "template": result.Template,
			"template_summary": result.Summary, "source": "nexus-registry",
		}, nil

	case "get_many":
		ids := workflowTemplateIDs(input.TemplateIDs)
		if len(ids) < 2 || len(ids) > 3 {
			return nil, errors.New("template_ids must contain 2 or 3 distinct ids")
		}
		templates := make([]workflow.Template, 0, len(ids))
		for _, id := range ids {
			result, err := s.executeWorkflowTemplateGet(id, "")
			if err != nil {
				return nil, err
			}
			templates = append(templates, result.Template)
		}
		return map[string]any{
			"ok": true, "action": action, "templates": templates, "count": len(templates),
			"composition_required": true, "next_required_action": workflowCompositionNextAction,
			"source": "nexus-registry",
		}, nil

	case "list":
		status := workflow.Status(strings.TrimSpace(input.TemplateStatus))
		if status != "" && status != workflow.StatusActive && status != workflow.StatusRetired {
			return nil, errors.New("template_status must be active or retired")
		}
		result, err := s.executeWorkflowTemplateList(status)
		if err != nil {
			return nil, err
		}
		summaries := result.Summaries
		if status == "" {
			summaries = currentWorkflowTemplates(summaries)
		}
		return map[string]any{
			"ok": true, "action": action, "templates": summaries, "count": len(summaries),
			"workflow_dir": s.workflowRegistry.Root(), "source": "nexus-registry",
		}, nil

	case "match":
		result, err := s.executeWorkflowTemplateMatch(
			ctx, strings.TrimSpace(input.Goal), strings.TrimSpace(input.Device), strings.TrimSpace(input.Type),
		)
		if err != nil {
			return nil, err
		}
		return workflowTemplateMatchMCPResult(result), nil

	case "vector_index":
		result, err := s.executeWorkflowTemplateVectorIndex()
		if err != nil {
			return nil, err
		}
		return workflowTemplateVectorIndexMCPResult(result), nil

	default:
		return nil, fmt.Errorf("unsupported workflow_template_manage action: %s", action)
	}
}

func workflowTemplateIDs(values []string) []string {
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
	return ids
}

func workflowTemplateMatchMCPResult(result workflowTemplateMatchOperationResult) map[string]any {
	return map[string]any{
		"ok": result.OK, "action": result.Action, "candidates": result.Candidates, "count": result.Count,
		"workflow_dir": result.WorkflowDir, "root": result.Root, "source": result.Source,
		"vector_search_enabled": result.VectorSearchEnabled, "vector_index_status": result.VectorIndexStatus,
		"vector_index_items": result.VectorIndexItems, "embedding_model": result.EmbeddingModel,
		"recommended": result.Recommended, "recommendation_reason": result.RecommendationReason,
		"best_candidate_score": result.BestCandidateScore,
		"score_thresholds": map[string]any{
			"use_template":      result.ScoreThresholds.UseTemplate,
			"consider_template": result.ScoreThresholds.Consider,
			"plain_task_below":  result.ScoreThresholds.PlainTaskBelow,
		},
	}
}

func workflowTemplateVectorIndexMCPResult(result workflowTemplateVectorIndexOperationResult) map[string]any {
	mapped := map[string]any{
		"ok": result.OK, "action": "vector_index", "source": result.Source,
		"vector_index_available": result.Available, "vector_index_status": result.VectorIndexStatus,
	}
	if result.EmbeddingModel != "" {
		mapped["embedding_model"] = result.EmbeddingModel
	}
	if !result.Available {
		return mapped
	}
	mapped["file_name"] = result.FileName
	mapped["path"] = result.Path
	mapped["size_bytes"] = result.SizeBytes
	mapped["updated_at"] = result.UpdatedAt
	mapped["content"] = result.Content
	mapped["vector_index_items"] = result.VectorIndexItems
	mapped["dimension"] = result.Dimension
	return mapped
}
