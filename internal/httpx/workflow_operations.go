package httpx

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/workflow"
)

type workflowTemplateOperationResult struct {
	Template workflow.Template
	Summary  workflowTemplateSummary
}

type workflowTemplateListOperationResult struct {
	Templates []workflow.Template
	Summaries []workflowTemplateSummary
}

type workflowScoreThresholds struct {
	UseTemplate    int `json:"use_template"`
	Consider       int `json:"consider_template"`
	PlainTaskBelow int `json:"plain_task_below"`
}

type workflowTemplateMatchOperationResult struct {
	OK                   bool                    `json:"ok"`
	Action               string                  `json:"action"`
	Candidates           []workflow.Candidate    `json:"candidates"`
	Count                int                     `json:"count"`
	WorkflowDir          string                  `json:"workflow_dir"`
	Root                 string                  `json:"root"`
	Source               string                  `json:"source"`
	VectorSearchEnabled  bool                    `json:"vector_search_enabled"`
	VectorIndexStatus    string                  `json:"vector_index_status"`
	VectorIndexItems     int                     `json:"vector_index_items"`
	EmbeddingModel       string                  `json:"embedding_model"`
	Recommended          string                  `json:"recommended"`
	RecommendationReason string                  `json:"recommendation_reason"`
	BestCandidateScore   int                     `json:"best_candidate_score"`
	ScoreThresholds      workflowScoreThresholds `json:"score_thresholds"`
}

type workflowTemplateVectorIndexOperationResult struct {
	OK                bool   `json:"ok"`
	Available         bool   `json:"available"`
	Source            string `json:"source"`
	FileName          string `json:"file_name,omitempty"`
	Path              string `json:"path,omitempty"`
	SizeBytes         int64  `json:"size_bytes,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
	Content           string `json:"content,omitempty"`
	VectorIndexStatus string `json:"vector_index_status"`
	VectorIndexItems  int    `json:"vector_index_items,omitempty"`
	EmbeddingModel    string `json:"embedding_model,omitempty"`
	Dimension         int    `json:"dimension,omitempty"`
}

// Workflow Registry 负责模板领域约束；这些 operation 统一 REST 与 MCP 的用例编排和展示摘要。
// 边界层可以保留各自协议字段，但不得重新实现 Publish/Retire/Get/List/Match 等业务链路。
func (s *Server) executeWorkflowTemplatePublish(template workflow.Template) (workflowTemplateOperationResult, error) {
	published, err := s.workflowRegistry.Publish(template)
	if err != nil {
		return workflowTemplateOperationResult{}, err
	}
	return workflowTemplateOperationResult{
		Template: published,
		Summary:  s.workflowTemplateSummary(published),
	}, nil
}

func (s *Server) executeWorkflowTemplateRetire(id, version string) (workflowTemplateOperationResult, error) {
	retired, err := s.workflowRegistry.Retire(strings.TrimSpace(id), strings.TrimSpace(version))
	if err != nil {
		return workflowTemplateOperationResult{}, err
	}
	return workflowTemplateOperationResult{
		Template: retired,
		Summary:  s.workflowTemplateSummary(retired),
	}, nil
}

func (s *Server) executeWorkflowTemplateGet(id, version string) (workflowTemplateOperationResult, error) {
	id = strings.TrimSpace(id)
	version = strings.TrimSpace(version)
	if id == "" {
		return workflowTemplateOperationResult{}, errors.New("template_id is required")
	}
	var (
		template workflow.Template
		err      error
	)
	if version == "" {
		template, err = s.workflowRegistry.Active(id)
	} else {
		template, err = s.workflowRegistry.Get(id, version)
	}
	if err != nil {
		return workflowTemplateOperationResult{}, err
	}
	return workflowTemplateOperationResult{
		Template: template,
		Summary:  s.workflowTemplateSummary(template),
	}, nil
}

func (s *Server) executeWorkflowTemplateList(status workflow.Status) (workflowTemplateListOperationResult, error) {
	templates, err := s.workflowRegistry.List(status)
	if err != nil {
		return workflowTemplateListOperationResult{}, err
	}
	summaries := make([]workflowTemplateSummary, 0, len(templates))
	for _, template := range templates {
		summaries = append(summaries, s.workflowTemplateSummary(template))
	}
	return workflowTemplateListOperationResult{
		Templates: templates,
		Summaries: summaries,
	}, nil
}

func (s *Server) executeWorkflowTemplateMatch(ctx context.Context, goal, device, taskType string) (workflowTemplateMatchOperationResult, error) {
	ai := s.workflowAIConfig()
	candidates, err := s.workflowRegistry.Match(ctx, ai, goal, device, taskType)
	if err != nil {
		return workflowTemplateMatchOperationResult{}, err
	}
	vectorStatus, vectorItems := s.workflowRegistry.VectorIndexInfo(ai)
	root := s.workflowRegistry.Root()
	best := 0
	if len(candidates) > 0 {
		best = candidates[0].Score
	}
	recommended := "plain_task"
	reason := "no active template is specific enough; create a plain recoverable task"
	if best >= 85 {
		recommended = "use_template"
		reason = "top candidate score is strong enough to select by default"
	} else if best >= 60 {
		recommended = "consider_template"
		reason = "top candidate is plausible but should be checked against the user goal"
	}
	return workflowTemplateMatchOperationResult{
		OK: true, Action: "match", Candidates: candidates, Count: len(candidates),
		WorkflowDir: root, Root: root, Source: "nexus-registry",
		VectorSearchEnabled: ai.VectorEnabled(), VectorIndexStatus: vectorStatus,
		VectorIndexItems: vectorItems, EmbeddingModel: ai.Model,
		Recommended: recommended, RecommendationReason: reason, BestCandidateScore: best,
		ScoreThresholds: workflowScoreThresholds{UseTemplate: 85, Consider: 60, PlainTaskBelow: 60},
	}, nil
}

func (s *Server) executeWorkflowTemplateVectorIndex() (workflowTemplateVectorIndexOperationResult, error) {
	snapshot, err := s.workflowRegistry.VectorIndexSnapshot(s.workflowAIConfig())
	if err != nil {
		return workflowTemplateVectorIndexOperationResult{}, err
	}
	result := workflowTemplateVectorIndexOperationResult{
		OK: true, Source: "nexus-registry", VectorIndexStatus: snapshot.Status,
	}
	switch snapshot.Status {
	case workflow.VectorIndexNotConfigured, workflow.VectorIndexMissing:
		return result, nil
	case workflow.VectorIndexStale:
		result.EmbeddingModel = snapshot.Model
		return result, nil
	}
	result.Available = true
	result.FileName = "vector-index.json"
	result.Path = "workflow-templates/vector-index.json"
	result.SizeBytes = snapshot.SizeBytes
	if !snapshot.ModTime.IsZero() {
		result.UpdatedAt = snapshot.ModTime.UTC().Format(time.RFC3339Nano)
	}
	result.Content = string(snapshot.Content)
	result.VectorIndexStatus = workflow.VectorIndexReady
	result.VectorIndexItems = snapshot.Items
	result.EmbeddingModel = snapshot.Model
	result.Dimension = snapshot.Dimension
	return result, nil
}
