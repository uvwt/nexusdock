package httpx

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/nexusdock/internal/auth"
	"github.com/uvwt/nexusdock/internal/core"
)

const oauthFormBodyLimit = 64 << 10

var oauthAuthorizeTemplate = template.Must(template.New("oauth-authorize").Parse(`<!doctype html>
<html lang="{{.Text.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Text.Title}}</title>
<style>
:root{color-scheme:light dark;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f6f7f9;color:#15171a}.card{width:min(520px,calc(100vw - 40px));padding:28px;border:1px solid #d9dde3;border-radius:16px;background:#fff;box-shadow:0 12px 32px rgba(0,0,0,.08)}h1{font-size:22px;margin:0 0 12px}p{line-height:1.6;color:#4b5563}.meta{padding:14px 16px;border-radius:10px;background:#f3f4f6;margin:18px 0}.meta strong,.meta span{display:block;overflow-wrap:anywhere}.meta span{font-size:13px;color:#6b7280;margin-top:5px}.actions{display:flex;gap:10px;justify-content:flex-end;margin-top:24px}button{border:0;border-radius:9px;padding:10px 16px;font:inherit;cursor:pointer}.deny{background:#eceff3;color:#252a31}.allow{background:#111827;color:#fff}@media(prefers-color-scheme:dark){body{background:#111317;color:#f4f4f5}.card{background:#191c21;border-color:#30343b}.meta{background:#22262d}.meta span,p{color:#a8b0bb}.deny{background:#2b3038;color:#f4f4f5}.allow{background:#f4f4f5;color:#111827}}
</style>
</head>
<body><main class="card">
<h1>{{.Text.Heading}}</h1>
<p>{{.Text.Description}}</p>
<div class="meta"><strong>{{.ClientName}}</strong><span>{{.RedirectURI}}</span></div>
<form method="post" action="/oauth/authorize" autocomplete="off">
{{range .Fields}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">{{end}}
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<div class="actions"><button class="deny" type="submit" name="decision" value="deny">{{.Text.Deny}}</button><button class="allow" type="submit" name="decision" value="allow">{{.Text.Allow}}</button></div>
</form>
</main></body></html>`))

type oauthHiddenField struct {
	Name  string
	Value string
}

type oauthAuthorizePage struct {
	ClientName  string
	RedirectURI string
	CSRFToken   string
	Fields      []oauthHiddenField
	Text        oauthAuthorizeText
}

type oauthAuthorizeText struct {
	Lang          string
	Title         string
	Heading       string
	Description   template.HTML
	Deny          string
	Allow         string
	DefaultClient string
}

var oauthAuthorizeEnglish = oauthAuthorizeText{
	Lang:          uiLocaleEnglish,
	Title:         "Authorize NexusDock",
	Heading:       "Authorize MCP client",
	Description:   template.HTML(`This client is requesting access to NexusDock MCP. After authorization, it can only call tools exposed by <code>/mcp</code> and will not receive access to the Nexus management API.`),
	Deny:          "Deny",
	Allow:         "Allow access",
	DefaultClient: "MCP client",
}

var oauthAuthorizeChinese = oauthAuthorizeText{
	Lang:          uiLocaleChinese,
	Title:         "授权 NexusDock",
	Heading:       "授权 MCP 客户端",
	Description:   template.HTML(`此客户端请求访问 NexusDock MCP。授权后，它只能调用 <code>/mcp</code> 提供的工具，不会获得 Nexus 管理 API 权限。`),
	Deny:          "拒绝",
	Allow:         "允许访问",
	DefaultClient: "MCP 客户端",
}

func oauthAuthorizeTextFor(header string) oauthAuthorizeText {
	if preferredUILocale(header) == uiLocaleChinese {
		return oauthAuthorizeChinese
	}
	return oauthAuthorizeEnglish
}

type oauthAuthorizationRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
	Scope               string
	State               string
}

type fixedWindowLimiter struct {
	mu      sync.Mutex
	maximum int
	window  time.Duration
	entries map[string]fixedWindowEntry
}

type fixedWindowEntry struct {
	started time.Time
	count   int
}

func newFixedWindowLimiter(maximum int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{maximum: maximum, window: window, entries: make(map[string]fixedWindowEntry)}
}

func (l *fixedWindowLimiter) Allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, exists := l.entries[key]
	if !exists && len(l.entries) >= 4096 {
		for candidate, value := range l.entries {
			if now.Sub(value.started) >= l.window {
				delete(l.entries, candidate)
			}
		}
		if len(l.entries) >= 4096 {
			return false
		}
	}
	if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
		l.entries[key] = fixedWindowEntry{started: now, count: 1}
		return true
	}
	if entry.count >= l.maximum {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}

func (s *Server) registerOAuthRoutes(mux *http.ServeMux) {
	if s.oauth == nil || s.auth == nil {
		return
	}
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.oauthAuthorizationServerMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.oauthProtectedResourceMetadata)
	mux.HandleFunc("POST /register", s.oauthRegisterClient)
	mux.HandleFunc("GET /oauth/authorize", s.oauthAuthorize)
	mux.HandleFunc("POST /oauth/authorize", s.oauthAuthorize)
	mux.HandleFunc("POST /oauth/token", s.oauthToken)
}

func (s *Server) oauthAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	issuer := s.oauthIssuer(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/oauth/authorize",
		"token_endpoint":                        issuer + "/oauth/token",
		"registration_endpoint":                 issuer + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp"},
		"resource_indicators_supported":         true,
	})
}

func (s *Server) oauthProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	issuer := s.oauthIssuer(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 issuer + "/mcp",
		"authorization_servers":    []string{issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{"mcp"},
	})
}

func (s *Server) oauthRegisterClient(w http.ResponseWriter, r *http.Request) {
	if !s.oauthRegisterLimiter.Allow(s.clientIPPrefix(r), time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeOAuthJSONError(w, http.StatusTooManyRequests, "temporarily_unavailable", "too many client registrations")
		return
	}
	if !oauthJSONRequest(w, r) {
		return
	}
	var input auth.OAuthClientRegistration
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, oauthFormBodyLimit))
	if err := decoder.Decode(&input); err != nil {
		writeOAuthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid client registration")
		return
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeOAuthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "client registration must contain exactly one JSON object")
		return
	}
	client, err := s.oauth.RegisterClient(r.Context(), input)
	if err != nil {
		if errors.Is(err, auth.ErrOAuthClientLimit) {
			w.Header().Set("Retry-After", "60")
			writeOAuthJSONError(w, http.StatusTooManyRequests, "temporarily_unavailable", "OAuth client registration capacity reached")
			return
		}
		if errors.Is(err, auth.ErrInvalidOAuthClientMetadata) {
			writeOAuthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid client metadata")
			return
		}
		if s.logger != nil {
			s.logger.Error("OAuth client registration failed", "error", err)
		}
		writeOAuthJSONError(w, http.StatusInternalServerError, "server_error", "OAuth client registration failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  client.ID,
		"client_id_issued_at":        client.CreatedAt.Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                client.GrantTypes,
		"response_types":             client.ResponseTypes,
		"token_endpoint_auth_method": client.TokenEndpointAuthMethod,
	})
}

func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Vary", "Accept-Language")
	// 授权表单依赖 Origin 做 CSRF 同源校验。全局 no-referrer 会让 Chromium 的表单 POST
	// 发送 Origin: null；same-origin 只保留站内来源，同时不会向跨源 callback 泄露 Referer。
	w.Header().Set("Referrer-Policy", "same-origin")
	// OAuth 表单成功提交后会 302 到已注册的跨源 callback。Chromium 会把 form-action
	// 应用于整条重定向链，因此这里不能继承全局 form-action 'self'。
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'none'; style-src 'unsafe-inline'")

	values := r.URL.Query()
	if r.Method == http.MethodPost {
		if !s.sameOrigin(r) {
			http.Error(w, "origin rejected", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, oauthFormBodyLimit)
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/x-www-form-urlencoded" {
			writeOAuthJSONError(w, http.StatusBadRequest, "invalid_request", "authorization form must be urlencoded")
			return
		}
		if err := r.ParseForm(); err != nil {
			writeOAuthJSONError(w, http.StatusBadRequest, "invalid_request", "invalid authorization form")
			return
		}
		values = r.PostForm
	}
	params, err := s.parseOAuthAuthorizationRequest(r, values)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	client, redirectOK := s.oauth.ClientAllows(r.Context(), params.ClientID, params.RedirectURI)
	if !redirectOK {
		http.Error(w, "invalid client_id or redirect_uri", http.StatusBadRequest)
		return
	}

	session, sessionErr := s.authenticateCookie(r)
	if sessionErr != nil {
		returnTo := safeReturnTo(r.URL.RequestURI())
		if r.Method == http.MethodPost {
			returnTo = "/oauth/authorize?" + authorizationValues(params).Encode()
		}
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape(returnTo), http.StatusFound)
		return
	}
	if session.MustChangePassword {
		returnTo := "/oauth/authorize?" + authorizationValues(params).Encode()
		http.Redirect(w, r, "/change-password?return_to="+url.QueryEscape(returnTo), http.StatusFound)
		return
	}

	if r.Method == http.MethodGet {
		text := oauthAuthorizeTextFor(r.Header.Get("Accept-Language"))
		name := client.Name
		if name == "" {
			name = text.DefaultClient
		}
		fields := authorizationValues(params)
		page := oauthAuthorizePage{ClientName: name, RedirectURI: params.RedirectURI, CSRFToken: session.CSRFToken, Text: text}
		for _, key := range []string{"response_type", "client_id", "redirect_uri", "code_challenge", "code_challenge_method", "resource", "scope", "state"} {
			if value := fields.Get(key); value != "" {
				page.Fields = append(page.Fields, oauthHiddenField{Name: key, Value: value})
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := oauthAuthorizeTemplate.Execute(w, page); err != nil && s.logger != nil {
			s.logger.Warn("render OAuth authorization page failed", "error", err)
		}
		return
	}

	if !s.auth.VerifySessionCSRF(session, strings.TrimSpace(values.Get("csrf_token"))) {
		http.Error(w, "csrf rejected", http.StatusForbidden)
		return
	}
	decision := strings.TrimSpace(values.Get("decision"))
	if decision == "deny" {
		redirectOAuthResult(w, r, params.RedirectURI, url.Values{"error": {"access_denied"}, "state": {params.State}})
		return
	}
	if decision != "allow" {
		redirectOAuthResult(w, r, params.RedirectURI, url.Values{"error": {"invalid_request"}, "state": {params.State}})
		return
	}
	code, err := s.oauth.IssueAuthorizationCode(r.Context(), auth.OAuthAuthorizationInput{
		ClientID: params.ClientID, UserID: session.UserID, RedirectURI: params.RedirectURI,
		CodeChallenge: params.CodeChallenge, Resource: params.Resource, Scope: params.Scope,
	})
	if err != nil {
		redirectOAuthResult(w, r, params.RedirectURI, url.Values{"error": {"server_error"}, "state": {params.State}})
		return
	}
	redirectOAuthResult(w, r, params.RedirectURI, url.Values{"code": {code}, "state": {params.State}})
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	r.Body = http.MaxBytesReader(w, r.Body, oauthFormBodyLimit)
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		writeOAuthJSONError(w, http.StatusBadRequest, "invalid_request", "token request must be urlencoded")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthJSONError(w, http.StatusBadRequest, "invalid_request", "invalid token request")
		return
	}
	for _, key := range []string{"grant_type", "client_id", "client_secret", "code", "redirect_uri", "code_verifier", "refresh_token", "resource"} {
		if len(r.PostForm[key]) > 1 {
			writeOAuthJSONError(w, http.StatusBadRequest, "invalid_request", "OAuth token parameter must not be repeated: "+key)
			return
		}
	}
	if _, _, ok := r.BasicAuth(); ok || strings.TrimSpace(r.PostForm.Get("client_secret")) != "" {
		writeOAuthJSONError(w, http.StatusUnauthorized, "invalid_client", "public clients must not use a client secret")
		return
	}
	clientID := strings.TrimSpace(r.PostForm.Get("client_id"))
	client, err := s.oauth.Client(r.Context(), clientID)
	if err != nil || client.TokenEndpointAuthMethod != "none" {
		writeOAuthJSONError(w, http.StatusBadRequest, "invalid_client", "invalid client")
		return
	}
	resource := strings.TrimSpace(r.PostForm.Get("resource"))
	if resource == "" {
		resource = s.oauthResource(r)
	}
	if !auth.EquivalentResourceURI(resource, s.oauthResource(r)) {
		writeOAuthJSONError(w, http.StatusBadRequest, "invalid_target", "resource must be the NexusDock MCP endpoint")
		return
	}

	var issued auth.OAuthIssuedTokens
	switch strings.TrimSpace(r.PostForm.Get("grant_type")) {
	case "authorization_code":
		issued, err = s.oauth.ExchangeAuthorizationCode(r.Context(), clientID, strings.TrimSpace(r.PostForm.Get("code")), strings.TrimSpace(r.PostForm.Get("redirect_uri")), strings.TrimSpace(r.PostForm.Get("code_verifier")), resource)
	case "refresh_token":
		if !oauthClientHasGrant(client, "refresh_token") {
			writeOAuthJSONError(w, http.StatusBadRequest, "unauthorized_client", "client is not registered for refresh_token")
			return
		}
		issued, err = s.oauth.Refresh(r.Context(), clientID, strings.TrimSpace(r.PostForm.Get("refresh_token")), resource)
	default:
		writeOAuthJSONError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type")
		return
	}
	if err != nil {
		writeOAuthJSONError(w, http.StatusBadRequest, "invalid_grant", "invalid or expired OAuth grant")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  issued.AccessToken,
		"token_type":    "Bearer",
		"expires_in":    int(time.Until(issued.AccessExpiresAt).Round(time.Second).Seconds()),
		"refresh_token": issued.RefreshToken,
		"scope":         issued.Scope,
	})
}

func (s *Server) withMCPAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if s.mcpToken != nil && mcpTokenMatches(header, s.mcpToken.Token()) {
			actor := core.Actor{Type: core.ActorSystem, ID: "mcp_token"}
			next(w, r.WithContext(withMCPActorContext(r.Context(), actor)))
			return
		}
		if strings.HasPrefix(strings.ToLower(header), "bearer ") && s.oauth != nil {
			if access, err := s.oauth.AuthenticateAccess(r.Context(), bearerToken(header), s.oauthResource(r)); err == nil {
				actor := core.Actor{Type: core.ActorUser, ID: access.UserID}
				next(w, r.WithContext(withMCPActorContext(r.Context(), actor)))
				return
			}
			s.writeMCPBearerChallenge(w, r, true)
			return
		}
		if header == "" && s.auth != nil {
			if _, err := s.authenticateCookie(r); err == nil {
				s.withWebSession(next, false)(w, r)
				return
			}
		}
		if s.auth == nil && s.isLocalAPIRequest(r) {
			actor := core.Actor{Type: core.ActorSystem, ID: "local_mcp"}
			next(w, r.WithContext(withMCPActorContext(r.Context(), actor)))
			return
		}
		s.writeMCPBearerChallenge(w, r, false)
	}
}

func mcpTokenMatches(header, expected string) bool {
	if expected == "" || !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return false
	}
	actual := strings.TrimSpace(header[7:])
	return len(actual) == len(expected) && subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func (s *Server) writeMCPBearerChallenge(w http.ResponseWriter, r *http.Request, invalid bool) {
	challenge := "Bearer"
	if s.oauth != nil {
		challenge += ` resource_metadata="` + s.oauthIssuer(r) + `/.well-known/oauth-protected-resource/mcp"`
	}
	if invalid {
		challenge += `, error="invalid_token"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "OAuth authorization is required for MCP access")
}

func (s *Server) parseOAuthAuthorizationRequest(r *http.Request, values url.Values) (oauthAuthorizationRequest, error) {
	for _, key := range []string{"response_type", "client_id", "redirect_uri", "code_challenge", "code_challenge_method", "resource", "scope", "state"} {
		if len(values[key]) > 1 {
			return oauthAuthorizationRequest{}, fmt.Errorf("OAuth parameter must not be repeated: %s", key)
		}
	}
	params := oauthAuthorizationRequest{
		ResponseType: strings.TrimSpace(values.Get("response_type")), ClientID: strings.TrimSpace(values.Get("client_id")),
		RedirectURI: strings.TrimSpace(values.Get("redirect_uri")), CodeChallenge: strings.TrimSpace(values.Get("code_challenge")),
		CodeChallengeMethod: strings.TrimSpace(values.Get("code_challenge_method")), Resource: strings.TrimSpace(values.Get("resource")),
		Scope: strings.TrimSpace(values.Get("scope")), State: values.Get("state"),
	}
	if params.ResponseType != "code" || params.ClientID == "" || params.RedirectURI == "" {
		return oauthAuthorizationRequest{}, fmt.Errorf("invalid OAuth authorization request")
	}
	if params.CodeChallengeMethod != "S256" || !auth.ValidPKCEValue(params.CodeChallenge) {
		return oauthAuthorizationRequest{}, fmt.Errorf("PKCE S256 is required")
	}
	if params.Resource == "" {
		params.Resource = s.oauthResource(r)
	}
	if !auth.EquivalentResourceURI(params.Resource, s.oauthResource(r)) {
		return oauthAuthorizationRequest{}, fmt.Errorf("invalid OAuth resource")
	}
	params.Resource = s.oauthResource(r)
	if params.Scope == "" {
		params.Scope = "mcp"
	}
	if strings.Join(strings.Fields(params.Scope), " ") != "mcp" {
		return oauthAuthorizationRequest{}, fmt.Errorf("unsupported OAuth scope")
	}
	return params, nil
}

func (s *Server) oauthIssuer(r *http.Request) string {
	scheme := "http"
	host := r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	if s.isTrustedProxy(r) {
		if forwarded := strings.ToLower(lastForwardedValue(r.Header.Get("X-Forwarded-Proto"))); forwarded == "http" || forwarded == "https" {
			scheme = forwarded
		}
		if forwarded := strings.TrimSpace(lastForwardedValue(r.Header.Get("X-Forwarded-Host"))); forwarded != "" && !strings.ContainsAny(forwarded, "\r\n/@") {
			host = forwarded
		}
	}
	return scheme + "://" + host
}

func (s *Server) oauthResource(r *http.Request) string { return s.oauthIssuer(r) + "/mcp" }

func authorizationValues(params oauthAuthorizationRequest) url.Values {
	values := url.Values{}
	values.Set("response_type", params.ResponseType)
	values.Set("client_id", params.ClientID)
	values.Set("redirect_uri", params.RedirectURI)
	values.Set("code_challenge", params.CodeChallenge)
	values.Set("code_challenge_method", params.CodeChallengeMethod)
	values.Set("resource", params.Resource)
	values.Set("scope", params.Scope)
	if params.State != "" {
		values.Set("state", params.State)
	}
	return values
}

func redirectOAuthResult(w http.ResponseWriter, r *http.Request, redirectURI string, values url.Values) {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect", http.StatusBadRequest)
		return
	}
	query := parsed.Query()
	for key, items := range values {
		for _, value := range items {
			if value != "" {
				query.Set(key, value)
			}
		}
	}
	parsed.RawQuery = query.Encode()
	w.Header().Set("Location", parsed.String())
	w.WriteHeader(http.StatusFound)
}

func oauthClientHasGrant(client auth.OAuthClient, grant string) bool {
	for _, value := range client.GrantTypes {
		if value == grant {
			return true
		}
	}
	return false
}

func oauthJSONRequest(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeOAuthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "client registration must be JSON")
		return false
	}
	return true
}

func writeOAuthJSONError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]any{"error": code, "error_description": description})
}
