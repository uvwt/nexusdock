package httpx

import (
	"errors"
	"net/http"
	"strings"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/core"
)

func (s *Server) registerAgentDockNodeRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/runtime/nodes", protected(s.agentDockNodeList))
	mux.HandleFunc("POST /v1/runtime/nodes/pairing-codes", protected(s.agentDockPairingCodeCreate))
	mux.HandleFunc("GET /v1/runtime/nodes/{nodeID}", protected(s.agentDockNodeGet))
	mux.HandleFunc("PATCH /v1/runtime/nodes/{nodeID}", protected(s.agentDockNodeUpdate))
	mux.HandleFunc("DELETE /v1/runtime/nodes/{nodeID}", protected(s.agentDockNodeDelete))

	// 配对码和 Device Token 是这两个入口各自的身份边界，不能套用浏览器会话认证。
	mux.HandleFunc("POST /v1/nodes/pair", s.agentDockNodePair)
	mux.HandleFunc("GET /v1/nodes/connect", s.agentDockNodeConnect)
}

func (s *Server) agentDockNodeList(w http.ResponseWriter, r *http.Request) {
	if s.agentDock == nil || s.agentDockHub == nil {
		writeError(w, http.StatusServiceUnavailable, "AGENTDOCK_NODE_STORE_UNAVAILABLE", "AgentDock 节点存储不可用")
		return
	}
	nodes, err := s.agentDock.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "AGENTDOCK_NODE_LIST_FAILED", "无法读取 AgentDock 节点")
		return
	}
	for index := range nodes {
		nodes[index].Online = s.agentDockHub.Online(nodes[index].ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nodes": nodes, "count": len(nodes)})
}

func (s *Server) agentDockPairingCodeCreate(w http.ResponseWriter, r *http.Request) {
	if s.agentDock == nil {
		writeError(w, http.StatusServiceUnavailable, "AGENTDOCK_NODE_STORE_UNAVAILABLE", "AgentDock 节点存储不可用")
		return
	}
	code, err := s.agentDock.CreatePairingCode(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "AGENTDOCK_PAIRING_CODE_FAILED", "无法创建 AgentDock 配对码")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "pairing": code})
}

func (s *Server) agentDockNodePair(w http.ResponseWriter, r *http.Request) {
	if s.agentDock == nil || s.auth == nil {
		writeError(w, http.StatusServiceUnavailable, "AGENTDOCK_PAIRING_UNAVAILABLE", "AgentDock 配对服务不可用")
		return
	}
	var request agentdock.PairInput
	if !decodeJSON(w, r, &request) {
		return
	}
	node, err := s.agentDock.Pair(r.Context(), request)
	if err != nil {
		writeAgentDockNodeError(w, err)
		return
	}
	// Device Token 只表达固定设备身份，不承载可配置权限集合。
	issued, err := s.auth.IssueToken(r.Context(), core.Actor{Type: core.ActorDevice, ID: node.ID}, "device_token", nil, 0)
	if err != nil {
		_ = s.agentDock.Delete(r.Context(), node.ID)
		writeError(w, http.StatusInternalServerError, "AGENTDOCK_DEVICE_TOKEN_FAILED", "无法签发 AgentDock Device Token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "node": node, "device_token": issued.Token,
		"connect_path": "/v1/nodes/connect",
	})
}

func (s *Server) agentDockNodeConnect(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || s.agentDockHub == nil {
		writeError(w, http.StatusServiceUnavailable, "AGENTDOCK_CONNECTION_UNAVAILABLE", "AgentDock 节点连接服务不可用")
		return
	}
	token := bearerToken(r.Header.Get("Authorization"))
	principal, err := s.auth.Authenticate(r.Context(), token)
	if err != nil {
		switch core.ErrorCodeOf(err) {
		case core.CodeAuthRequired, core.CodeInvalidToken, core.CodeTokenRevoked:
			writeError(w, http.StatusUnauthorized, "INVALID_DEVICE_TOKEN", "AgentDock Device Token 无效")
		default:
			if s.logger != nil {
				s.logger.Error("验证 AgentDock Device Token 失败", "request_id", requestIDFromContext(r.Context()), "error", err)
			}
			writeError(w, http.StatusInternalServerError, "AGENTDOCK_DEVICE_AUTH_FAILED", "无法验证 AgentDock Device Token")
		}
		return
	}
	if principal.Actor.Type != core.ActorDevice || principal.TokenKind != "device_token" {
		writeError(w, http.StatusUnauthorized, "INVALID_DEVICE_TOKEN", "AgentDock Device Token 无效")
		return
	}
	if err := s.agentDockHub.Accept(w, r, principal.Actor.ID, s.cfg.PublicURL); err != nil {
		// WebSocket Upgrade 成功后不能再写 HTTP 响应；连接端会收到关闭事件并重连。
		return
	}
}

func (s *Server) agentDockNodeGet(w http.ResponseWriter, r *http.Request) {
	if s.agentDock == nil {
		writeError(w, http.StatusServiceUnavailable, "AGENTDOCK_NODE_STORE_UNAVAILABLE", "AgentDock 节点存储不可用")
		return
	}
	node, err := s.agentDock.Get(r.Context(), r.PathValue("nodeID"))
	if err != nil {
		writeAgentDockNodeError(w, err)
		return
	}
	if s.agentDockHub != nil {
		node.Online = s.agentDockHub.Online(node.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node": node})
}

func (s *Server) agentDockNodeUpdate(w http.ResponseWriter, r *http.Request) {
	if s.agentDock == nil {
		writeError(w, http.StatusServiceUnavailable, "AGENTDOCK_NODE_STORE_UNAVAILABLE", "AgentDock 节点存储不可用")
		return
	}
	var request agentdock.UpdateInput
	if !decodeJSON(w, r, &request) {
		return
	}
	node, err := s.agentDock.Update(r.Context(), r.PathValue("nodeID"), request)
	if err != nil {
		writeAgentDockNodeError(w, err)
		return
	}
	if !node.Enabled && s.agentDockHub != nil {
		s.agentDockHub.Disconnect(node.ID)
	}
	if request.Enabled != nil {
		descriptors, descriptorErr := s.agentDock.ToolDescriptors(r.Context(), node.ID)
		if descriptorErr != nil {
			if s.logger != nil {
				s.logger.Warn("读取 AgentDock 节点工具契约失败", "node_id", node.ID, "error", descriptorErr)
			}
		} else {
			s.reconcileNodeTools(descriptors)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node": node})
}

func (s *Server) agentDockNodeDelete(w http.ResponseWriter, r *http.Request) {
	if s.agentDock == nil {
		writeError(w, http.StatusServiceUnavailable, "AGENTDOCK_NODE_STORE_UNAVAILABLE", "AgentDock 节点存储不可用")
		return
	}
	id := r.PathValue("nodeID")
	descriptors, descriptorErr := s.agentDock.ToolDescriptors(r.Context(), id)
	if s.agentDockHub != nil {
		s.agentDockHub.Disconnect(id)
	}
	if err := s.agentDock.Delete(r.Context(), id); err != nil {
		writeAgentDockNodeError(w, err)
		return
	}
	if descriptorErr != nil {
		if s.logger != nil {
			s.logger.Warn("读取待删除 AgentDock 节点工具契约失败", "node_id", id, "error", descriptorErr)
		}
	} else {
		s.reconcileNodeTools(descriptors)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node_id": id, "deleted": true})
}

func writeAgentDockNodeError(w http.ResponseWriter, err error) {
	var validationError agentdock.ValidationError
	switch {
	case errors.Is(err, agentdock.ErrNodeNotFound):
		writeError(w, http.StatusNotFound, "AGENTDOCK_NODE_NOT_FOUND", err.Error())
	case errors.Is(err, agentdock.ErrNodeExists):
		writeError(w, http.StatusConflict, "AGENTDOCK_NODE_EXISTS", err.Error())
	case errors.Is(err, agentdock.ErrNodeDisabled):
		writeError(w, http.StatusConflict, "AGENTDOCK_NODE_DISABLED", err.Error())
	case errors.Is(err, agentdock.ErrPairingCodeInvalid):
		writeError(w, http.StatusUnauthorized, "AGENTDOCK_PAIRING_CODE_INVALID", err.Error())
	case errors.As(err, &validationError):
		writeError(w, http.StatusBadRequest, "INVALID_AGENTDOCK_NODE", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "AGENTDOCK_NODE_OPERATION_FAILED", "无法完成 AgentDock 节点操作")
	}
}

func bearerToken(header string) string {
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}
