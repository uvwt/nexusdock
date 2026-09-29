package httpx

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uvwt/nexusdock/internal/recall"
)

type evolutionLifecycleSummary struct {
	EvolutionID     string   `json:"evolution_id"`
	Title           string   `json:"title"`
	Statement       string   `json:"statement"`
	Type            string   `json:"type"`
	Scope           string   `json:"scope"`
	Project         string   `json:"project"`
	Device          string   `json:"device,omitempty"`
	Status          string   `json:"status"`
	Revision        int64    `json:"revision"`
	SupportCount    int      `json:"support_count"`
	ContradictCount int      `json:"contradict_count"`
	EvidenceCount   int      `json:"evidence_count"`
	Tags            []string `json:"tags,omitempty"`
	UpdatedAt       string   `json:"updated_at"`
}

type evolutionLifecycleDetail struct {
	evolutionLifecycleSummary
	CanonicalKey  string                     `json:"canonical_key,omitempty"`
	PolicyVersion string                     `json:"policy_version"`
	Source        string                     `json:"source,omitempty"`
	Evidence      []recall.LifecycleEvidence `json:"evidence,omitempty"`
	SupersededBy  string                     `json:"superseded_by,omitempty"`
	CreatedAt     string                     `json:"created_at"`
}

func evolutionSummary(record recall.LifecycleRecord) evolutionLifecycleSummary {
	return evolutionLifecycleSummary{
		EvolutionID: record.EvolutionID, Title: record.Title, Statement: record.Statement,
		Type: record.Type, Scope: record.Scope, Project: record.Project, Device: record.Device,
		Status: record.Status, Revision: record.Revision, SupportCount: record.SupportCount,
		ContradictCount: record.ContradictCount, EvidenceCount: len(record.Evidence), Tags: record.Tags,
		UpdatedAt: record.UpdatedAt,
	}
}

func evolutionDetail(record recall.LifecycleRecord) evolutionLifecycleDetail {
	return evolutionLifecycleDetail{
		evolutionLifecycleSummary: evolutionSummary(record),
		CanonicalKey:              record.CanonicalKey, PolicyVersion: record.PolicyVersion, Source: record.Source,
		Evidence: record.Evidence, SupersededBy: record.SupersededBy, CreatedAt: record.CreatedAt,
	}
}

func (s *Server) registerEvolutionAdminRoutes(r chi.Router) {
	r.Get("/v1/evolution/lifecycle", s.evolutionLifecycleList)
	r.Get("/v1/evolution/lifecycle/{evolutionID}", s.evolutionLifecycleRead)
}

func (s *Server) registerEvolutionInternalRoutes(r chi.Router) {
	// 生命周期读写属于 AgentDock 设备域，认证由路由分支统一继承。
	r.Post("/internal/recall/lifecycle/query", s.lifecycleQuery)
	r.Post("/internal/recall/lifecycle/transition", s.lifecycleTransition)
}

func (s *Server) evolutionLifecycleList(w http.ResponseWriter, _ *http.Request) {
	records, err := s.store.QueryLifecycle(recall.LifecycleQuery{Limit: 100})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIFECYCLE_QUERY_FAILED", "failed to read evolution lifecycle")
		return
	}

	items := make([]evolutionLifecycleSummary, 0, len(records))
	for _, record := range records {
		items = append(items, evolutionSummary(record))
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": items, "count": len(items)})
}

func (s *Server) evolutionLifecycleRead(w http.ResponseWriter, r *http.Request) {
	evolutionID := strings.TrimSpace(r.PathValue("evolutionID"))
	records, err := s.store.QueryLifecycle(recall.LifecycleQuery{EvolutionID: evolutionID, Limit: 1})
	if err != nil {
		writeError(w, http.StatusBadRequest, "LIFECYCLE_QUERY_FAILED", err.Error())
		return
	}
	if len(records) == 0 {
		writeError(w, http.StatusNotFound, "EVOLUTION_NOT_FOUND", "evolution record not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"record": evolutionDetail(records[0])})
}

func (s *Server) withEvolutionAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nodeID, err := s.access.authenticateDeviceToken(r.Context(), r.Header.Get("Authorization"))
		if err != nil || s.agentDock == nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid evolution credentials")
			return
		}
		node, err := s.agentDock.Get(r.Context(), nodeID)
		if err != nil || !node.Enabled {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid evolution credentials")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) lifecycleQuery(w http.ResponseWriter, r *http.Request) {
	var request recall.LifecycleQuery
	if !decodeJSON(w, r, &request) {
		return
	}
	records, err := s.store.QueryLifecycle(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "LIFECYCLE_QUERY_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records, "count": len(records)})
}

func (s *Server) lifecycleTransition(w http.ResponseWriter, r *http.Request) {
	var request recall.LifecycleTransition
	if !decodeJSON(w, r, &request) {
		return
	}
	result, err := s.store.TransitionLifecycle(request)
	if err != nil {
		switch err {
		case recall.ErrLifecycleRevisionConflict:
			writeError(w, http.StatusConflict, "LIFECYCLE_REVISION_CONFLICT", err.Error())
			return
		case recall.ErrLifecyclePolicyVersionConflict:
			writeError(w, http.StatusConflict, "LIFECYCLE_POLICY_VERSION_CONFLICT", err.Error())
			return
		case recall.ErrLifecycleOperationConflict:
			writeError(w, http.StatusConflict, "LIFECYCLE_OPERATION_CONFLICT", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "LIFECYCLE_TRANSITION_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
