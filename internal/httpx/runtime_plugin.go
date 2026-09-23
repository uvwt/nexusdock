package httpx

import (
	"net/http"
	"sort"
	"strings"
)

func (s *Server) registerRuntimePluginRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/plugins", protected(s.runtimePlugins))
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/plugins/{name}", protected(s.runtimePlugin))
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
