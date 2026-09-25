package httpx

import (
	"net/http"
	"strings"

	"github.com/uvwt/nexusdock/internal/agentdock"
)

func (s *Server) mcpOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if s.agentDockHub == nil {
		writeError(w, http.StatusServiceUnavailable, "AGENTDOCK_CONNECTION_UNAVAILABLE", "AgentDock 节点连接服务不可用")
		return
	}
	nodeID := strings.TrimSpace(r.PathValue("nodeID"))
	if nodeID == "" || strings.Contains(nodeID, "/") || len(r.URL.RawQuery) > 16*1024 {
		writeError(w, http.StatusBadRequest, "INVALID_MCP_AUTH_CALLBACK", "OAuth 回调参数无效")
		return
	}
	query := r.URL.Query()
	one := func(name string) (string, bool) {
		values, exists := query[name]
		if !exists {
			return "", true
		}
		if len(values) != 1 {
			return "", false
		}
		return strings.TrimSpace(values[0]), true
	}
	state, ok := one("state")
	if !ok || state == "" {
		writeError(w, http.StatusBadRequest, "INVALID_MCP_AUTH_CALLBACK", "OAuth 回调参数无效")
		return
	}
	code, codeOK := one("code")
	issuer, issuerOK := one("iss")
	oauthError, errorOK := one("error")
	description, descriptionOK := one("error_description")
	if !codeOK || !issuerOK || !errorOK || !descriptionOK || (code == "" && oauthError == "") || (code != "" && oauthError != "") {
		writeError(w, http.StatusBadRequest, "INVALID_MCP_AUTH_CALLBACK", "OAuth 回调参数无效")
		return
	}

	err := s.agentDockHub.RuntimeMCPOAuthCallback(r.Context(), nodeID, agentdock.RuntimeMCPOAuthCallback{
		State: state, Code: code, Issuer: issuer, Error: oauthError, ErrorDescription: description,
	})
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<!doctype html><meta charset=\"utf-8\"><title>NexusDock</title><p>授权信息已发送到 AgentDock，可以关闭此页面。</p>"))
}
