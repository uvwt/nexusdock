package httpx

import (
	"net/http"
	"net/url"
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
	mux.HandleFunc("GET /v1/runtime/mcp", protected(s.runtimeMCPServers))
	mux.HandleFunc("GET /v1/runtime/mcp/{name}/environment", protected(s.runtimeMCPEnvironment))
	mux.HandleFunc("GET /v1/runtime/mcp/{name}", protected(s.runtimeMCPServer))
	mux.HandleFunc("POST /v1/runtime/mcp", protected(s.runtimeMCPManage))
}

func (s *Server) runtimeMCPServers(w http.ResponseWriter, r *http.Request) {
	payload, err := s.runtimeGet(r.Context(), "/internal/runtime/mcp", nil)
	if err != nil {
		writeJSON(w, runtimeErrorHTTPStatus(err), runtimeUnavailablePayload(err))
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) runtimeMCPServer(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "INVALID_MCP_NAME", "MCP 名称不能为空")
		return
	}
	payload, err := s.runtimeGet(r.Context(), "/internal/runtime/mcp/"+url.PathEscape(name), nil)
	if err != nil {
		writeJSON(w, runtimeErrorHTTPStatus(err), runtimeUnavailablePayload(err))
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) runtimeMCPEnvironment(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "INVALID_MCP_NAME", "MCP 名称不能为空")
		return
	}
	payload, err := s.runtimePost(r.Context(), "/internal/runtime/mcp", runtimeMCPRequest{
		Action: "env_list",
		Name:   name,
	})
	if err != nil {
		writeJSON(w, runtimeErrorHTTPStatus(err), runtimeUnavailablePayload(err))
		return
	}
	writeJSON(w, http.StatusOK, payload)
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
	payload, err := s.runtimePost(r.Context(), "/internal/runtime/mcp", request)
	if err != nil {
		writeJSON(w, runtimeErrorHTTPStatus(err), runtimeUnavailablePayload(err))
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
