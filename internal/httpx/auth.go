package httpx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uvwt/nexusdock/internal/auth"
	"github.com/uvwt/nexusdock/internal/core"
)

const sessionCookieName = "nexus_session"

type webSessionContextKey struct{}

func withWebSessionContext(ctx context.Context, session auth.WebSession) context.Context {
	return context.WithValue(ctx, webSessionContextKey{}, session)
}

func webSessionFromContext(ctx context.Context) (auth.WebSession, bool) {
	session, ok := ctx.Value(webSessionContextKey{}).(auth.WebSession)
	return session, ok
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "initialized": false})
		return
	}
	status, err := s.auth.AdminStatus(r.Context())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "AUTH_STATUS_FAILED", "unable to read authentication status")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "initialized": status.Initialized})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginTransportAllowed(r) {
		writeAuthError(w, http.StatusBadRequest, "HTTPS_REQUIRED", "login requires HTTPS")
		return
	}
	if !s.sameOrigin(r) {
		writeAuthError(w, http.StatusForbidden, "ORIGIN_REJECTED", "request origin is not allowed")
		return
	}
	status, err := s.auth.AdminStatus(r.Context())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "AUTH_STATUS_FAILED", "unable to read authentication status")
		return
	}
	if !status.Initialized {
		writeAuthError(w, http.StatusServiceUnavailable, "ADMIN_NOT_INITIALIZED", "administrator is not initialized")
		return
	}
	var request struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		RememberMe bool   `json:"remember_me"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	issued, err := s.auth.Login(r.Context(), request.Username, request.Password, s.clientIPPrefix(r), summarizeUserAgent(r.UserAgent()), request.RememberMe)
	if err != nil {
		var limited *auth.RateLimitError
		if errors.As(err, &limited) {
			seconds := int(limited.RetryAfter.Round(time.Second).Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeAuthError(w, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "too many login attempts; try again later")
			return
		}
		if core.ErrorCodeOf(err) == core.CodeInvalidToken {
			writeAuthError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid username or password")
			return
		}
		if s.logger != nil {
			s.logger.Error("administrator login failed", "request_id", requestIDFromWriter(w), "error", err)
		}
		writeAuthError(w, http.StatusInternalServerError, "LOGIN_FAILED", "unable to create login session")
		return
	}
	s.setSessionCookie(w, r, issued.Token, issued.Session.RememberMe, issued.Session.AbsoluteExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": issued.Session})
}

func (s *Server) currentSession(w http.ResponseWriter, r *http.Request) {
	session, _ := webSessionFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, _ := webSessionFromContext(r.Context())
	err := s.auth.RevokeWebSession(r.Context(), session.UserID, session.ID, "logout")
	if err != nil && core.ErrorCodeOf(err) != core.CodeNotFound {
		writeAuthError(w, http.StatusInternalServerError, "LOGOUT_FAILED", "unable to revoke session")
		return
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) updateCredential(w http.ResponseWriter, r *http.Request) {
	session, _ := webSessionFromContext(r.Context())
	var request struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.auth.UpdateSecret(r.Context(), session.UserID, request.Current, request.Next); err != nil {
		switch core.ErrorCodeOf(err) {
		case core.CodeInvalidToken:
			writeAuthError(w, http.StatusUnauthorized, "CURRENT_CREDENTIAL_INVALID", "current credential is incorrect")
		case core.CodeValidation:
			writeAuthError(w, http.StatusBadRequest, "CREDENTIAL_POLICY_FAILED", publicCodedMessage(err, "new credential does not meet policy"))
		default:
			writeAuthError(w, http.StatusInternalServerError, "CREDENTIAL_UPDATE_FAILED", "unable to update credential")
		}
		return
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reauthenticate": true})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	session, _ := webSessionFromContext(r.Context())
	items, err := s.auth.ListWebSessions(r.Context(), session.UserID, session.ID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "SESSION_LIST_FAILED", "unable to list sessions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	current, _ := webSessionFromContext(r.Context())
	target := r.PathValue("sessionID")
	if target == current.ID {
		writeAuthError(w, http.StatusBadRequest, "USE_LOGOUT", "use logout for the current session")
		return
	}
	if err := s.auth.RevokeWebSession(r.Context(), current.UserID, target, "user_revoked"); err != nil {
		if core.ErrorCodeOf(err) == core.CodeNotFound {
			writeAuthError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "active session not found")
			return
		}
		writeAuthError(w, http.StatusInternalServerError, "SESSION_REVOKE_FAILED", "unable to revoke session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) logoutOtherSessions(w http.ResponseWriter, r *http.Request) {
	current, _ := webSessionFromContext(r.Context())
	count, err := s.auth.RevokeOtherWebSessions(r.Context(), current.UserID, current.ID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "SESSION_REVOKE_FAILED", "unable to revoke other sessions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revoked": count})
}

func (s *Server) withUIAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			next.ServeHTTP(w, r)
			return
		}
		session, err := s.authenticateCookie(r)
		if err != nil {
			http.Redirect(w, r, "/login?return_to="+url.QueryEscape(safeReturnTo(r.URL.RequestURI())), http.StatusFound)
			return
		}
		if session.MustChangePassword && r.URL.Path != "/change-password" {
			http.Redirect(w, r, "/change-password?return_to="+url.QueryEscape(safeReturnTo(r.URL.RequestURI())), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r.WithContext(withWebSessionContext(r.Context(), session)))
	})
}

func (s *Server) withWebSession(next http.Handler, allowCredentialUpdate bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := s.authenticateCookie(r)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "SESSION_REQUIRED", "login session is missing or expired")
			return
		}
		if session.MustChangePassword && !allowCredentialUpdate {
			writeAuthError(w, http.StatusForbidden, "CREDENTIAL_UPDATE_REQUIRED", "administrator credential must be updated before continuing")
			return
		}
		if isUnsafeMethod(r.Method) {
			if !s.sameOrigin(r) {
				writeAuthError(w, http.StatusForbidden, "ORIGIN_REJECTED", "request origin is not allowed")
				return
			}
			if !s.auth.VerifySessionCSRF(session, r.Header.Get("X-CSRF-Token")) {
				writeAuthError(w, http.StatusForbidden, "CSRF_REJECTED", "CSRF token is missing or invalid")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(withWebSessionContext(r.Context(), session)))
	})
}

func (s *Server) webSessionMiddleware(allowCredentialUpdate bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return s.withWebSession(next, allowCredentialUpdate)
	}
}

func (s *Server) withAPIAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth != nil {
			s.withWebSession(next, false).ServeHTTP(w, r)
			return
		}
		if s.isLocalAPIRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid API credentials")
	})
}

func (s *Server) withDeviceOrAPIAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth != nil {
			principal, err := s.auth.Authenticate(r.Context(), bearerToken(r.Header.Get("Authorization")))
			if err == nil && principal.Actor.Type == core.ActorDevice && principal.TokenKind == "device_token" {
				if s.agentDock != nil {
					node, lookupErr := s.agentDock.Get(r.Context(), principal.Actor.ID)
					if lookupErr == nil && node.Enabled {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
		}
		s.withAPIAccess(next).ServeHTTP(w, r)
	})
}

func (s *Server) authenticateCookie(r *http.Request) (auth.WebSession, error) {
	if s.auth == nil {
		return auth.WebSession{}, core.NewError(core.CodeAuthRequired, "web authentication is not configured", nil)
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return auth.WebSession{}, core.NewError(core.CodeAuthRequired, "session is required", err)
	}
	return s.auth.AuthenticateWebSession(r.Context(), cookie.Value)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, remember bool, expires time.Time) {
	cookie := &http.Cookie{
		Name: sessionCookieName, Value: value, Path: "/", HttpOnly: true,
		Secure: s.secureRequest(r), SameSite: http.SameSiteStrictMode,
	}
	if remember {
		cookie.Expires = expires
		cookie.MaxAge = int(time.Until(expires).Seconds())
	}
	http.SetCookie(w, cookie)
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: s.secureRequest(r), SameSite: http.SameSiteStrictMode,
		Expires: time.Unix(1, 0), MaxAge: -1,
	})
}

func (s *Server) secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.isTrustedProxy(r) && strings.EqualFold(lastForwardedValue(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (s *Server) loginTransportAllowed(r *http.Request) bool {
	if s.secureRequest(r) {
		return true
	}
	// HTTP 登录只允许浏览器直接访问本机回环地址。反向代理后的请求必须通过 HTTPS，
	// 避免代理与后端之间的回环连接把公网 HTTP 误判成本机访问。
	if !isLoopbackHostPort(r.RemoteAddr) || !isLoopbackHostPort(r.Host) {
		return false
	}
	return r.Header.Get("Forwarded") == "" &&
		r.Header.Get("X-Forwarded-For") == "" &&
		r.Header.Get("X-Forwarded-Host") == "" &&
		r.Header.Get("X-Forwarded-Proto") == ""
}

func (s *Server) sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	scheme := "http"
	host := r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	if s.isTrustedProxy(r) {
		if value := lastForwardedValue(r.Header.Get("X-Forwarded-Proto")); value != "" {
			scheme = value
		}
		if value := lastForwardedValue(r.Header.Get("X-Forwarded-Host")); value != "" {
			host = value
		}
	}
	return strings.EqualFold(parsed.Scheme, scheme) && strings.EqualFold(parsed.Host, host)
}

func (s *Server) isLocalAPIRequest(r *http.Request) bool {
	return isLoopbackHostPort(r.RemoteAddr)
}

func isLoopbackHostPort(value string) bool {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		host = value
	}
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) isTrustedProxy(r *http.Request) bool {
	return s.isTrustedProxyIP(remoteIP(r.RemoteAddr))
}

func (s *Server) isTrustedProxyIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	addr, ok := netipAddr(ip)
	if !ok {
		return false
	}
	// cfg.TrustedProxies 在启动时已解析为规范化前缀，这里只做地址族一致的包含判断。
	for _, prefix := range s.cfg.TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// netipAddr 把请求侧的 net.IP 转换成 netip.Addr 并还原 IPv4-mapped 形式，
// 使其能与配置侧规范化后的 netip.Prefix 直接比对；XFF 与 RemoteAddr 里
// 的 IPv4 常以 ::ffff:a.b.c.d 的 16 字节形式出现，不还原会导致族不匹配。
func netipAddr(ip net.IP) (netip.Addr, bool) {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func (s *Server) clientIPPrefix(r *http.Request) string {
	ip := remoteIP(r.RemoteAddr)
	if ip != nil && s.isTrustedProxyIP(ip) {
		if forwarded, ok := forwardedIPs(r.Header.Get("X-Forwarded-For")); ok && len(forwarded) > 0 {
			ip = forwarded[0]
			for index := len(forwarded) - 1; index >= 0; index-- {
				if !s.isTrustedProxyIP(forwarded[index]) {
					ip = forwarded[index]
					break
				}
			}
		} else if realIP := parseForwardedIP(r.Header.Get("X-Real-IP")); realIP != nil {
			ip = realIP
		}
	}
	if ip == nil {
		return "unknown"
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return net.IPv4(ipv4[0], ipv4[1], ipv4[2], 0).String() + "/24"
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func remoteIP(value string) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		host = value
	}
	return net.ParseIP(strings.Trim(strings.TrimSpace(host), "[]"))
}

func parseForwardedIP(value string) net.IP {
	value = strings.Trim(strings.TrimSpace(value), "\"")
	if value == "" {
		return nil
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return ip
	}
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return nil
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func forwardedIPs(value string) ([]net.IP, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, false
	}
	parts := strings.Split(value, ",")
	result := make([]net.IP, 0, len(parts))
	for _, part := range parts {
		ip := parseForwardedIP(part)
		if ip == nil {
			return nil, false
		}
		result = append(result, ip)
	}
	return result, true
}

func lastForwardedValue(value string) string {
	parts := strings.Split(value, ",")
	for index := len(parts) - 1; index >= 0; index-- {
		if part := strings.TrimSpace(parts[index]); part != "" {
			return part
		}
	}
	return ""
}

func summarizeUserAgent(value string) string {
	browser := "Other browser"
	switch {
	case strings.Contains(value, "Edg/"):
		browser = "Edge"
	case strings.Contains(value, "Chrome/") || strings.Contains(value, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(value, "Firefox/") || strings.Contains(value, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(value, "Safari/"):
		browser = "Safari"
	}
	platform := "Unknown OS"
	switch {
	case strings.Contains(value, "iPhone") || strings.Contains(value, "iPad"):
		platform = "iOS"
	case strings.Contains(value, "Mac OS X") || strings.Contains(value, "Macintosh"):
		platform = "macOS"
	case strings.Contains(value, "Android"):
		platform = "Android"
	case strings.Contains(value, "Windows"):
		platform = "Windows"
	case strings.Contains(value, "Linux"):
		platform = "Linux"
	}
	return browser + " / " + platform
}

func safeReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\r\n") {
		return "/ui/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil {
		return "/ui/"
	}
	return value
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": map[string]any{"code": code, "message": message}})
}

func publicCodedMessage(err error, fallback string) string {
	var coded *core.CodedError
	if errors.As(err, &coded) && coded.Message != "" {
		return coded.Message
	}
	return fallback
}

func (s *Server) registerWebAuthRoutes(r chi.Router) {
	r.Get("/login", s.uiIndex)
	r.Get("/ui/assets/*", s.uiApp)
	r.With(s.withUIAccess).Get("/change-password", s.uiIndex)
	r.Get("/v1/auth/status", s.authStatus)
	r.Post("/v1/auth/login", s.login)

	allowCredentialUpdate := s.webSessionMiddleware(true)
	r.With(allowCredentialUpdate).Get("/v1/auth/session", s.currentSession)
	r.With(allowCredentialUpdate).Post("/v1/auth/logout", s.logout)
	r.With(allowCredentialUpdate).Post("/v1/auth/credential", s.updateCredential)

	requireReadySession := s.webSessionMiddleware(false)
	r.With(requireReadySession).Get("/v1/auth/sessions", s.listSessions)
	r.With(requireReadySession).Delete("/v1/auth/sessions/{sessionID}", s.revokeSession)
	r.With(requireReadySession).Post("/v1/auth/sessions/logout-others", s.logoutOtherSessions)
}
