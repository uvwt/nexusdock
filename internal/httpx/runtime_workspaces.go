package httpx

import (
	"errors"
	"net/http"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/workspace"
)

func (s *Server) registerRuntimeWorkspaceRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/runtime/workspaces", protected(s.runtimeWorkspacesList))
	mux.HandleFunc("POST /v1/runtime/workspaces", protected(s.runtimeWorkspaceCreate))
	mux.HandleFunc("GET /v1/runtime/workspaces/{workspaceID}", protected(s.runtimeWorkspaceGet))
	mux.HandleFunc("PATCH /v1/runtime/workspaces/{workspaceID}", protected(s.runtimeWorkspaceUpdate))
	mux.HandleFunc("DELETE /v1/runtime/workspaces/{workspaceID}", protected(s.runtimeWorkspaceDelete))
}

func (s *Server) runtimeWorkspacesList(w http.ResponseWriter, r *http.Request) {
	if s.workspaces == nil {
		writeError(w, http.StatusServiceUnavailable, "WORKSPACE_STORE_UNAVAILABLE", "Runtime Workspace 存储不可用")
		return
	}
	items, err := s.workspaces.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "WORKSPACE_LIST_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items})
}

func (s *Server) runtimeWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	if s.workspaces == nil {
		writeError(w, http.StatusServiceUnavailable, "WORKSPACE_STORE_UNAVAILABLE", "Runtime Workspace 存储不可用")
		return
	}
	var request workspace.CreateInput
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.validateWorkspaceNode(r, request.NodeID); err != nil {
		writeWorkspaceError(w, err)
		return
	}
	item, err := s.workspaces.Create(r.Context(), request)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "workspace": item})
}

func (s *Server) runtimeWorkspaceGet(w http.ResponseWriter, r *http.Request) {
	if s.workspaces == nil {
		writeError(w, http.StatusServiceUnavailable, "WORKSPACE_STORE_UNAVAILABLE", "Runtime Workspace 存储不可用")
		return
	}
	item, err := s.workspaces.Get(r.Context(), r.PathValue("workspaceID"))
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace": item})
}

func (s *Server) runtimeWorkspaceUpdate(w http.ResponseWriter, r *http.Request) {
	if s.workspaces == nil {
		writeError(w, http.StatusServiceUnavailable, "WORKSPACE_STORE_UNAVAILABLE", "Runtime Workspace 存储不可用")
		return
	}
	var request workspace.UpdateInput
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.NodeID != nil {
		if err := s.validateWorkspaceNode(r, *request.NodeID); err != nil {
			writeWorkspaceError(w, err)
			return
		}
	}
	item, err := s.workspaces.Update(r.Context(), r.PathValue("workspaceID"), request)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace": item})
}

func (s *Server) runtimeWorkspaceDelete(w http.ResponseWriter, r *http.Request) {
	if s.workspaces == nil {
		writeError(w, http.StatusServiceUnavailable, "WORKSPACE_STORE_UNAVAILABLE", "Runtime Workspace 存储不可用")
		return
	}
	if err := s.workspaces.Delete(r.Context(), r.PathValue("workspaceID")); err != nil {
		writeWorkspaceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": true, "workspace_id": r.PathValue("workspaceID")})
}

func (s *Server) validateWorkspaceNode(r *http.Request, nodeID string) error {
	if s.agentDock == nil {
		return errors.New("AgentDock 节点存储不可用")
	}
	_, err := s.agentDock.Get(r.Context(), nodeID)
	return err
}

func writeWorkspaceError(w http.ResponseWriter, err error) {
	var validation workspace.ValidationError
	switch {
	case errors.As(err, &validation):
		writeError(w, http.StatusBadRequest, "WORKSPACE_VALIDATION_FAILED", validation.Error())
	case errors.Is(err, workspace.ErrNotFound):
		writeError(w, http.StatusNotFound, "WORKSPACE_NOT_FOUND", err.Error())
	case errors.Is(err, workspace.ErrExists):
		writeError(w, http.StatusConflict, "WORKSPACE_EXISTS", err.Error())
	case errors.Is(err, agentdock.ErrNodeNotFound):
		writeError(w, http.StatusBadRequest, "WORKSPACE_NODE_NOT_FOUND", "Workspace 指定的 AgentDock 节点不存在")
	default:
		writeError(w, http.StatusInternalServerError, "WORKSPACE_OPERATION_FAILED", err.Error())
	}
}
