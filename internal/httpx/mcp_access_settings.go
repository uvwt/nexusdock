package httpx

import "net/http"

type mcpAccessTokenResponse struct {
	OK    bool   `json:"ok"`
	Token string `json:"token"`
}

type mcpSettingsResponse struct {
	OK             bool   `json:"ok"`
	Token          string `json:"token"`
	MCPAppsEnabled bool   `json:"mcp_apps_enabled"`
	Persisted      bool   `json:"persisted"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type mcpSettingsUpdateRequest struct {
	MCPAppsEnabled *bool `json:"mcp_apps_enabled"`
}

func (s *Server) getMCPSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.mcpToken == nil || s.mcpSettings == nil {
		writeError(w, http.StatusServiceUnavailable, "MCP_SETTINGS_UNAVAILABLE", "MCP 设置存储不可用")
		return
	}
	_, view, err := s.mcpSettings.Load(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MCP_SETTINGS_READ_FAILED", "读取 MCP 设置失败")
		return
	}
	writeJSON(w, http.StatusOK, mcpSettingsResponse{
		OK: true, Token: s.mcpToken.Token(), MCPAppsEnabled: view.MCPAppsEnabled,
		Persisted: view.Persisted, UpdatedAt: view.UpdatedAt,
	})
}

func (s *Server) updateMCPSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.mcpToken == nil || s.mcpSettings == nil {
		writeError(w, http.StatusServiceUnavailable, "MCP_SETTINGS_UNAVAILABLE", "MCP 设置存储不可用")
		return
	}
	var request mcpSettingsUpdateRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.MCPAppsEnabled == nil {
		writeError(w, http.StatusBadRequest, "MCP_APPS_ENABLED_REQUIRED", "mcp_apps_enabled 不能为空")
		return
	}
	view, err := s.mcpSettings.Update(r.Context(), *request.MCPAppsEnabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MCP_SETTINGS_UPDATE_FAILED", "保存 MCP 设置失败")
		return
	}
	s.setMCPAppsEnabled(view.MCPAppsEnabled)
	writeJSON(w, http.StatusOK, mcpSettingsResponse{
		OK: true, Token: s.mcpToken.Token(), MCPAppsEnabled: view.MCPAppsEnabled,
		Persisted: view.Persisted, UpdatedAt: view.UpdatedAt,
	})
}

func (s *Server) getMCPAccessToken(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.mcpToken == nil {
		writeError(w, http.StatusServiceUnavailable, "MCP_TOKEN_UNAVAILABLE", "MCP Token 存储不可用")
		return
	}
	writeJSON(w, http.StatusOK, mcpAccessTokenResponse{OK: true, Token: s.mcpToken.Token()})
}

func (s *Server) resetMCPAccessToken(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.mcpToken == nil {
		writeError(w, http.StatusServiceUnavailable, "MCP_TOKEN_UNAVAILABLE", "MCP Token 存储不可用")
		return
	}
	token, err := s.mcpToken.Reset()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("reset MCP access token failed", "error", err)
		}
		writeError(w, http.StatusInternalServerError, "MCP_TOKEN_RESET_FAILED", "unable to reset MCP access token")
		return
	}
	writeJSON(w, http.StatusOK, mcpAccessTokenResponse{OK: true, Token: token})
}
