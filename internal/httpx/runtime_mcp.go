package httpx

import (
	"encoding/json"
	"net/http"
	"strings"
)

var runtimeMCPActions = map[string]bool{
	"add": true, "remove": true, "enable": true, "disable": true,
	"env_set": true, "env_unset": true, "env_list": true, "refresh": true,
}

type runtimeMCPRequest struct {
	Action      string            `json:"action"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Transport   string            `json:"transport,omitempty"`
	URL         string            `json:"url,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	HeaderEnv   map[string]string `json:"header_env,omitempty"`
	EnvFromEnv  map[string]string `json:"env_from_env,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	TimeoutMS   int               `json:"timeout_ms,omitempty"`
	Key         string            `json:"key,omitempty"`
	Value       string            `json:"value,omitempty"`
}

func (s *Server) registerRuntimeMCPRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/mcp", protected(s.runtimeMCPServers))
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/mcp/{name}/environment", protected(s.runtimeMCPEnvironment))
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}/mcp/{name}", protected(s.runtimeMCPServer))
	mux.HandleFunc("POST /v1/runtime/nodes/{nodeID}/mcp", protected(s.runtimeMCPManage))
}

func (s *Server) runtimeMCPServers(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")
	servers, err := s.agentDockHub.RuntimeMCPServers(r.Context(), nodeID)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	// Plugin 自带的 MCP 组件归属 Plugin 页面；独立 MCP 页面只展示 standalone 服务。
	// 对旧版 AgentDock 未返回 source_type 的条目继续保留，避免升级期间误隐藏已有服务。
	standalone := servers[:0]
	for _, server := range servers {
		if server.SourceType == "plugin" || server.PluginName != "" {
			continue
		}
		standalone = append(standalone, server)
	}
	servers = standalone
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "node_id": nodeID, "servers": servers, "count": len(servers), "source": "agentdock-runtime-api",
	})
}

func (s *Server) runtimeMCPServer(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "INVALID_MCP_NAME", "MCP 名称不能为空")
		return
	}
	nodeID := r.PathValue("nodeID")
	detail, err := s.agentDockHub.RuntimeMCPServer(r.Context(), nodeID, name)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "node_id": nodeID, "server": detail.Server, "config": detail.Config, "source": "agentdock-runtime-api",
	})
}

func (s *Server) runtimeMCPEnvironment(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "INVALID_MCP_NAME", "MCP 名称不能为空")
		return
	}
	nodeID := r.PathValue("nodeID")
	items, err := s.agentDockHub.RuntimeMCPEnvironment(r.Context(), nodeID, name)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "node_id": nodeID, "items": items, "count": len(items), "source": "agentdock-runtime-api",
	})
}

func (s *Server) runtimeMCPManage(w http.ResponseWriter, r *http.Request) {
	var request runtimeMCPRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	request.Name = strings.TrimSpace(request.Name)
	if !runtimeMCPActions[request.Action] {
		writeError(w, http.StatusBadRequest, "INVALID_MCP_ACTION", "不支持的 MCP 管理操作")
		return
	}
	if request.Name == "" || strings.Contains(request.Name, "/") {
		writeError(w, http.StatusBadRequest, "INVALID_MCP_NAME", "MCP 名称不能为空")
		return
	}
	nodeID := r.PathValue("nodeID")
	body, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INVALID_MCP_ACTION", "编码 MCP 管理请求失败")
		return
	}
	// 响应是随 action 变化的透传 map（密钥值已在 agentdock 层强制移除），Nexus 不解读其内容。
	payload, err := s.agentDockHub.RuntimeMCPManage(r.Context(), nodeID, body)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	payload["node_id"] = nodeID
	writeJSON(w, http.StatusOK, payload)
}
