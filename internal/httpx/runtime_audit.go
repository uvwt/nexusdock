package httpx

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/uvwt/nexusdock/internal/audit"
)

func (s *Server) registerRuntimeAuditRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/runtime/audit", protected(s.runtimeAuditList))
}

func (s *Server) runtimeAuditList(w http.ResponseWriter, r *http.Request) {
	if s.auditService == nil {
		writeError(w, http.StatusServiceUnavailable, "AUDIT_UNAVAILABLE", "Runtime 审计服务不可用")
		return
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "INVALID_QUERY", "limit 必须为 1-500")
			return
		}
		limit = parsed
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	nodeID := strings.TrimSpace(r.URL.Query().Get("node_id"))
	risk := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("risk")))
	result := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("result")))
	if risk != "" && risk != "low" && risk != "medium" && risk != "high" {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY", "risk 必须为 low、medium 或 high")
		return
	}
	if result != "" && result != "succeeded" && result != "failed" {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY", "result 必须为 succeeded 或 failed")
		return
	}

	events, err := s.auditService.List(r.Context(), 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "AUDIT_LIST_FAILED", "无法读取 Runtime 审计事件")
		return
	}
	items := make([]audit.Event, 0, limit)
	for _, event := range events {
		if workspaceID != "" && metadataString(event.Metadata, "workspace_id") != workspaceID {
			continue
		}
		if nodeID != "" && metadataString(event.Metadata, "node_id") != nodeID {
			continue
		}
		if risk != "" && event.Risk != risk {
			continue
		}
		if result != "" && event.Result != result {
			continue
		}
		items = append(items, event)
		if len(items) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items, "count": len(items)})
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}
