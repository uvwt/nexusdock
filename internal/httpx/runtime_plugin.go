package httpx

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"sort"
	"strings"
)

func (s *Server) registerRuntimePluginRoutes(r chi.Router) {
	r.Get("/v1/runtime/nodes/{nodeID}/plugins", s.runtimePlugins)
	r.Get("/v1/runtime/nodes/{nodeID}/plugins/{name}", s.runtimePlugin)
}

func (s *Server) runtimePlugins(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")
	plugins, err := s.agentDockHub.RuntimePlugins(r.Context(), nodeID)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	sort.SliceStable(plugins, func(i, j int) bool {
		return strings.ToLower(plugins[i].Name) < strings.ToLower(plugins[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "node_id": nodeID, "items": plugins, "count": len(plugins), "source": "agentdock-runtime-api",
	})
}

func (s *Server) runtimePlugin(w http.ResponseWriter, r *http.Request) {
	name, err := cleanOpsName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PLUGIN_NAME", "Plugin 名称无效")
		return
	}
	nodeID := r.PathValue("nodeID")
	plugin, err := s.agentDockHub.RuntimePlugin(r.Context(), nodeID, name)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "node_id": nodeID, "plugin": plugin, "source": "agentdock-runtime-api",
	})
}
