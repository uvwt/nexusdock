package httpx

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/settings"
	"github.com/uvwt/nexusdock/internal/stage3"
)

const (
	stage3TaskListPerNode   = 32
	stage3TaskDetailPerNode = 8
	stage3LifecycleLimit    = 50
	stage3WorkflowLimit     = 30
)

// StartEvolutionStage3 starts one long-lived scheduler. Runtime settings changes wake the
// scheduler so model endpoint, model name, key and interval take effect without restarting Nexus.
func (s *Server) StartEvolutionStage3(ctx context.Context) {
	go s.evolutionStage3Loop(ctx, time.Now, newEvolutionStage3Timer, s.runEvolutionStage3Configured)
}

// evolutionStage3Loop keeps scheduling semantics deterministic and testable:
// a runnable configuration gets one immediate attempt, then every interval is anchored to
// the previous attempt. A wake only reloads configuration; it cannot keep postponing the timer.
func (s *Server) evolutionStage3Loop(
	ctx context.Context,
	now func() time.Time,
	newTimer func(time.Duration) evolutionStage3Timer,
	run func(context.Context, settings.RuntimeAIConfig),
) {
	var lastAttempt time.Time
	wasRunnable := false
	for {
		cfg := s.currentAIConfig()
		runnable := cfg.Stage3Enabled && strings.TrimSpace(cfg.Stage3Endpoint) != "" && strings.TrimSpace(cfg.Stage3Model) != ""
		if !runnable {
			// Re-enabling Stage 3 is a new runnable period, so it should run immediately once.
			lastAttempt = time.Time{}
			wasRunnable = false
			select {
			case <-ctx.Done():
				return
			case <-s.stage3Wake:
				continue
			}
		}

		interval := cfg.Stage3Interval
		if interval < time.Hour {
			interval = time.Hour
		}
		if !wasRunnable || lastAttempt.IsZero() {
			wasRunnable = true
			run(ctx, cfg)
			lastAttempt = now()
			continue
		}

		wait := lastAttempt.Add(interval).Sub(now())
		if wait <= 0 {
			run(ctx, cfg)
			lastAttempt = now()
			continue
		}
		timer := newTimer(wait)
		select {
		case <-ctx.Done():
			timer.stop()
			return
		case <-s.stage3Wake:
			timer.stop()
			continue
		case <-timer.c:
			run(ctx, cfg)
			lastAttempt = now()
		}
	}
}

type evolutionStage3Timer struct {
	c    <-chan time.Time
	stop func()
}

func newEvolutionStage3Timer(wait time.Duration) evolutionStage3Timer {
	timer := time.NewTimer(wait)
	return evolutionStage3Timer{
		c: timer.C,
		stop: func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		},
	}
}

func (s *Server) runEvolutionStage3Configured(ctx context.Context, cfg settings.RuntimeAIConfig) {
	client, err := stage3.NewClient(stage3.Config{
		Endpoint: cfg.Stage3Endpoint,
		Model:    cfg.Stage3Model,
		APIKey:   cfg.Stage3APIKey,
		Timeout:  cfg.Stage3Timeout,
	})
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("Stage 3 evolution skipped invalid model configuration", "error", err)
		}
		return
	}
	if err := s.runEvolutionStage3(ctx, client); err != nil && s.logger != nil {
		s.logger.Warn("Stage 3 evolution run failed", "error", err)
	}
}

func (s *Server) runEvolutionStage3(ctx context.Context, client *stage3.Client) error {
	if client == nil {
		return fmt.Errorf("Stage 3 model client is nil")
	}
	snapshot, nodes, err := s.stage3Snapshot(ctx)
	if err != nil {
		return err
	}
	if len(nodes) == 0 || (len(snapshot.Tasks) == 0 && len(snapshot.Lifecycle) == 0 && len(snapshot.Workflows) == 0) {
		return nil
	}
	output, err := client.Generate(ctx, snapshot)
	if err != nil {
		return err
	}
	allowedEvidence := stage3AllowedEvidence(snapshot.Tasks)
	for _, candidate := range output.Candidates {
		candidate.EvidenceRefs = filterStage3Evidence(candidate.EvidenceRefs, allowedEvidence)
		nodeID := stage3TargetNode(nodes, candidate.Device)
		payload := map[string]any{
			"intent": "propose",
			"candidate": map[string]any{
				"type": candidate.Type, "statement": candidate.Statement, "scope": candidate.Scope,
				"project": candidate.Project, "device": candidate.Device, "canonical_key": candidate.CanonicalKey,
				"tags": candidate.Tags,
			},
			"evidence_refs": candidate.EvidenceRefs,
			"rationale":     candidate.Rationale,
		}
		if _, err := s.runtimePost(ctx, nodeID, "/internal/runtime/evolve", payload); err != nil {
			if s.logger != nil {
				s.logger.Warn("Stage 3 proposal rejected by AgentDock", "node_id", nodeID, "candidate_type", candidate.Type, "error", err)
			}
			continue
		}
	}
	return nil
}

func (s *Server) stage3Snapshot(ctx context.Context) (stage3.Snapshot, []agentdock.Node, error) {
	if s.agentDock == nil {
		return stage3.Snapshot{}, nil, fmt.Errorf("AgentDock node store unavailable")
	}
	nodes, err := s.agentDock.List(ctx)
	if err != nil {
		return stage3.Snapshot{}, nil, err
	}
	enabled := make([]agentdock.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.Enabled {
			enabled = append(enabled, node)
		}
	}
	snapshot := stage3.Snapshot{}

	records, err := s.store.QueryLifecycle(recall.LifecycleQuery{Limit: stage3LifecycleLimit})
	if err != nil {
		return stage3.Snapshot{}, nil, fmt.Errorf("query lifecycle for Stage 3: %w", err)
	}
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.Scope), "local_only") || stage3SensitiveTags(record.Tags) {
			continue
		}
		snapshot.Lifecycle = append(snapshot.Lifecycle, stage3.LifecycleFact{
			EvolutionID: record.EvolutionID, Type: record.Type, Statement: record.Statement, Scope: record.Scope,
			Project: record.Project, Device: record.Device, Status: record.Status, SupportCount: record.SupportCount,
			ContradictCount: record.ContradictCount, Tags: append([]string(nil), record.Tags...),
		})
	}

	for _, node := range enabled {
		tasks, taskErr := s.collectOpsTasksFromRuntime(ctx, node.ID, stage3TaskListPerNode)
		if taskErr != nil {
			if s.logger != nil {
				s.logger.Debug("Stage 3 skipped unavailable AgentDock node", "node_id", node.ID, "error", taskErr)
			}
			continue
		}
		sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].UpdatedAt > tasks[j].UpdatedAt })
		count := 0
		for _, summary := range tasks {
			if count >= stage3TaskDetailPerNode {
				break
			}
			if summary.ReviewStatus != "pass" && summary.ReviewStatus != "failed" {
				continue
			}
			detail, detailErr := s.runtimeTaskDetailFromRuntime(ctx, node.ID, summary.ID)
			if detailErr != nil {
				continue
			}
			reviewRevision := opsString(detail.FinalReview["review_revision"])
			if reviewRevision == "" {
				continue
			}
			snapshot.Tasks = append(snapshot.Tasks, stage3.TaskFact{
				NodeID: node.ID, TaskID: summary.ID, Title: summary.Title, Goal: summary.Goal, Summary: summary.Summary,
				Status: summary.Status, ReviewStatus: summary.ReviewStatus, ReviewRevision: reviewRevision,
				VerifiedFacts: opsStringArray(detail.FinalReview["verified_facts"]), OpenRisks: opsStringArray(detail.FinalReview["open_risks"]),
				MissingChecks: opsStringArray(detail.FinalReview["missing_checks"]), UpdatedAt: summary.UpdatedAt,
			})
			count++
		}
	}

	workflows, err := s.listWorkflowTemplates(workflowTemplateActive)
	if err == nil {
		workflows = latestWorkflowTemplateVersions(workflows)
		if len(workflows) > stage3WorkflowLimit {
			workflows = workflows[:stage3WorkflowLimit]
		}
		for _, workflow := range workflows {
			snapshot.Workflows = append(snapshot.Workflows, stage3.WorkflowFact{
				ID: workflow.ID, Version: workflow.Version, Title: workflow.Title, Description: workflow.Description, Type: workflow.Match.Type,
			})
		}
	}
	return stage3.RedactSnapshot(snapshot), enabled, nil
}

func stage3SensitiveTags(tags []string) bool {
	for _, tag := range tags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "sensitive", "local_only", "private", "secret":
			return true
		}
	}
	return false
}

func stage3AllowedEvidence(tasks []stage3.TaskFact) map[string]bool {
	allowed := map[string]bool{}
	for _, task := range tasks {
		prefix := "task:" + task.TaskID + ":review:" + task.ReviewRevision
		for i := range task.VerifiedFacts {
			allowed[fmt.Sprintf("%s:verified:%d", prefix, i)] = true
		}
		for i := range task.OpenRisks {
			allowed[fmt.Sprintf("%s:risk:%d", prefix, i)] = true
		}
		for i := range task.MissingChecks {
			allowed[fmt.Sprintf("%s:missing:%d", prefix, i)] = true
		}
	}
	return allowed
}

func filterStage3Evidence(refs []string, allowed map[string]bool) []string {
	out := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if !allowed[ref] || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

func stage3TargetNode(nodes []agentdock.Node, device string) string {
	device = strings.TrimSpace(device)
	for _, node := range nodes {
		if device != "" && strings.EqualFold(node.ID, device) {
			return node.ID
		}
	}
	return nodes[0].ID
}
