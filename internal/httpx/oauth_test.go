package httpx

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/auth"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/core"
)

func newOAuthHTTPTestServer(t *testing.T) (*Server, *auth.Service) {
	t.Helper()
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db)
	if err := authService.InitializeAdmin(t.Context(), "owner", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	mcpTokenStore, err := auth.NewMCPTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(config.Config{AuthToken: "ops-secret"}, nil, slog.Default(), WithSystemDatabase(db), WithWebAuthentication(authService), WithMCPTokenStore(mcpTokenStore))
	return server, authService
}

func TestMCPAccessAdvertisesOAuthDiscovery(t *testing.T) {
	server, _ := newOAuthHTTPTestServer(t)
	handler := server.Handler()
	req := httptest.NewRequest(http.MethodPost, "https://nexus.example/mcp", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	want := `Bearer resource_metadata="https://nexus.example/.well-known/oauth-protected-resource/mcp"`
	if got := res.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate=%q want=%q", got, want)
	}

	metadata := httptest.NewRecorder()
	handler.ServeHTTP(metadata, httptest.NewRequest(http.MethodGet, "https://nexus.example/.well-known/oauth-authorization-server", nil))
	if metadata.Code != http.StatusOK || !strings.Contains(metadata.Body.String(), `"registration_endpoint":"https://nexus.example/register"`) {
		t.Fatalf("metadata status=%d body=%s", metadata.Code, metadata.Body.String())
	}
}

func TestOAuthHTTPAuthorizationCodeFlowAndMCPIsolation(t *testing.T) {
	server, authService := newOAuthHTTPTestServer(t)
	handler := server.Handler()

	registrationBody := `{"client_name":"Test MCP","redirect_uris":["https://client.example/callback"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}`
	registerReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/register", strings.NewReader(registrationBody))
	registerReq.Header.Set("Content-Type", "application/json")
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", registerRes.Code, registerRes.Body.String())
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(registerRes.Body.Bytes(), &registered); err != nil || registered.ClientID == "" {
		t.Fatalf("decode registration: %v body=%s", err, registerRes.Body.String())
	}

	verifier := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	query := url.Values{
		"response_type": {"code"}, "client_id": {registered.ClientID}, "redirect_uri": {"https://client.example/callback"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {"https://nexus.example/mcp"}, "scope": {"mcp"}, "state": {"client-state"},
	}
	authorizeURL := "https://nexus.example/oauth/authorize?" + query.Encode()
	unauthReq := httptest.NewRequest(http.MethodGet, authorizeURL, nil)
	unauthRes := httptest.NewRecorder()
	handler.ServeHTTP(unauthRes, unauthReq)
	if unauthRes.Code != http.StatusFound || !strings.HasPrefix(unauthRes.Header().Get("Location"), "/login?return_to=") {
		t.Fatalf("unauth authorize status=%d location=%s", unauthRes.Code, unauthRes.Header().Get("Location"))
	}

	login, err := authService.Login(t.Context(), "owner", "correct horse battery staple", "192.0.2.0/24", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookieName, Value: login.Token}
	pageReq := httptest.NewRequest(http.MethodGet, authorizeURL, nil)
	pageReq.Header.Set("Accept-Language", "zh-CN, en;q=0.8")
	pageReq.AddCookie(cookie)
	pageRes := httptest.NewRecorder()
	handler.ServeHTTP(pageRes, pageReq)
	if pageRes.Code != http.StatusOK || !strings.Contains(pageRes.Body.String(), "允许访问") {
		t.Fatalf("authorize page status=%d body=%s", pageRes.Code, pageRes.Body.String())
	}
	if vary := pageRes.Header().Get("Vary"); vary != "Accept-Language" {
		t.Fatalf("OAuth authorization page Vary=%q want=Accept-Language", vary)
	}
	if csp := pageRes.Header().Get("Content-Security-Policy"); strings.Contains(csp, "form-action") {
		t.Fatalf("OAuth authorization page must not constrain callback redirect with form-action: %s", csp)
	}
	if policy := pageRes.Header().Get("Referrer-Policy"); policy != "same-origin" {
		t.Fatalf("OAuth authorization page Referrer-Policy=%q want=same-origin", policy)
	}

	form := query
	form.Set("csrf_token", login.Session.CSRFToken)
	form.Set("decision", "allow")
	approveReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/oauth/authorize", strings.NewReader(form.Encode()))
	approveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	approveReq.Header.Set("Origin", "https://nexus.example")
	approveReq.AddCookie(cookie)
	approveRes := httptest.NewRecorder()
	handler.ServeHTTP(approveRes, approveReq)
	if approveRes.Code != http.StatusFound {
		t.Fatalf("approve status=%d body=%s", approveRes.Code, approveRes.Body.String())
	}
	callback, err := url.Parse(approveRes.Header().Get("Location"))
	if err != nil || callback.Host != "client.example" || callback.Query().Get("state") != "client-state" || callback.Query().Get("code") == "" {
		t.Fatalf("invalid callback: %s err=%v", approveRes.Header().Get("Location"), err)
	}

	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {registered.ClientID}, "code": {callback.Query().Get("code")},
		"redirect_uri": {"https://client.example/callback"}, "code_verifier": {verifier}, "resource": {"https://nexus.example/mcp"},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/oauth/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRes := httptest.NewRecorder()
	handler.ServeHTTP(tokenRes, tokenReq)
	if tokenRes.Code != http.StatusOK {
		t.Fatalf("token status=%d body=%s", tokenRes.Code, tokenRes.Body.String())
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(tokenRes.Body.Bytes(), &tokens); err != nil || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("decode token: %v body=%s", err, tokenRes.Body.String())
	}

	mcpAuthorized := false
	wrapped := server.withMCPAccess(func(w http.ResponseWriter, _ *http.Request) {
		mcpAuthorized = true
		w.WriteHeader(http.StatusNoContent)
	})
	mcpReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/mcp", nil)
	mcpReq.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	mcpRes := httptest.NewRecorder()
	wrapped(mcpRes, mcpReq)
	if !mcpAuthorized || mcpRes.Code != http.StatusNoContent {
		t.Fatalf("OAuth MCP bearer rejected status=%d", mcpRes.Code)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "https://nexus.example/v1/system/status", nil)
	apiReq.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	apiRes := httptest.NewRecorder()
	handler.ServeHTTP(apiRes, apiReq)
	if apiRes.Code != http.StatusUnauthorized {
		body, _ := io.ReadAll(apiRes.Result().Body)
		t.Fatalf("MCP OAuth token escaped into admin API: status=%d body=%s", apiRes.Code, body)
	}
}

func TestOAuthAuthorizeTextFollowsLanguagePreference(t *testing.T) {
	tests := []struct {
		header  string
		lang    string
		heading string
	}{
		{header: "", lang: "en", heading: "Authorize MCP client"},
		{header: "zh-Hans-CN, en;q=0.8", lang: "zh-CN", heading: "授权 MCP 客户端"},
		{header: "zh-TW, en-US;q=0.8", lang: "en", heading: "Authorize MCP client"},
	}
	for _, test := range tests {
		text := oauthAuthorizeTextFor(test.header)
		if text.Lang != test.lang || text.Heading != test.heading {
			t.Fatalf("oauthAuthorizeTextFor(%q)=(%q,%q) want=(%q,%q)", test.header, text.Lang, text.Heading, test.lang, test.heading)
		}
	}
}

func TestOAuthDesktopCustomRedirectRegistrationAndAuthorize(t *testing.T) {
	server, _ := newOAuthHTTPTestServer(t)
	handler := server.Handler()
	verifier := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])

	for _, redirectURI := range []string{"grokbot://mcp/oauth/callback", "cursor://anysphere.cursor-mcp/oauth/callback"} {
		body := `{"redirect_uris":["` + redirectURI + `"]}`
		registerReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/register", strings.NewReader(body))
		registerReq.Header.Set("Content-Type", "application/json")
		registerRes := httptest.NewRecorder()
		handler.ServeHTTP(registerRes, registerReq)
		if registerRes.Code != http.StatusCreated {
			t.Fatalf("register %s: status=%d body=%s", redirectURI, registerRes.Code, registerRes.Body.String())
		}
		var registered struct {
			ClientID string `json:"client_id"`
		}
		if err := json.Unmarshal(registerRes.Body.Bytes(), &registered); err != nil || registered.ClientID == "" {
			t.Fatalf("decode registration: %v body=%s", err, registerRes.Body.String())
		}

		query := url.Values{
			"response_type": {"code"}, "client_id": {registered.ClientID}, "redirect_uri": {redirectURI},
			"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {"https://nexus.example/mcp"}, "scope": {"mcp"},
		}
		authorizeRes := httptest.NewRecorder()
		handler.ServeHTTP(authorizeRes, httptest.NewRequest(http.MethodGet, "https://nexus.example/oauth/authorize?"+query.Encode(), nil))
		if authorizeRes.Code != http.StatusFound || !strings.HasPrefix(authorizeRes.Header().Get("Location"), "/login?return_to=") {
			t.Fatalf("authorize %s: status=%d location=%s", redirectURI, authorizeRes.Code, authorizeRes.Header().Get("Location"))
		}
	}
}

func TestDedicatedMCPTokenAuthorizesOnlyMCP(t *testing.T) {
	server, _ := newOAuthHTTPTestServer(t)
	called := false
	wrapped := server.withMCPAccess(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	mcpReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/mcp", nil)
	mcpReq.Header.Set("Authorization", "Bearer "+server.mcpToken.Token())
	mcpRes := httptest.NewRecorder()
	wrapped(mcpRes, mcpReq)
	if !called || mcpRes.Code != http.StatusNoContent {
		t.Fatalf("dedicated MCP token rejected: status=%d", mcpRes.Code)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "https://nexus.example/v1/system/status", nil)
	apiReq.Header.Set("Authorization", "Bearer "+server.mcpToken.Token())
	apiRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(apiRes, apiReq)
	if apiRes.Code != http.StatusUnauthorized {
		t.Fatalf("MCP token escaped into admin API: status=%d body=%s", apiRes.Code, apiRes.Body.String())
	}

	called = false
	opsReq := httptest.NewRequest(http.MethodPost, "https://nexus.example/mcp", nil)
	opsReq.Header.Set("Authorization", "Bearer ops-secret")
	opsRes := httptest.NewRecorder()
	wrapped(opsRes, opsReq)
	if called || opsRes.Code != http.StatusUnauthorized {
		t.Fatalf("operations token should not authorize MCP: called=%v status=%d", called, opsRes.Code)
	}
}

func TestOAuthTokenRejectsRepeatedParameters(t *testing.T) {
	server, _ := newOAuthHTTPTestServer(t)
	client, err := server.oauth.RegisterClient(t.Context(), auth.OAuthClientRegistration{RedirectURIs: []string{"https://client.example/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"grant_type": {"authorization_code"},
		"client_id":  {client.ID, client.ID},
	}
	req := httptest.NewRequest(http.MethodPost, "https://nexus.example/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"error":"invalid_request"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestOAuthRegistrationDoesNotExposeStoreErrors(t *testing.T) {
	server, _ := newOAuthHTTPTestServer(t)
	if err := server.db.Close(); err != nil {
		t.Fatal(err)
	}
	body := `{"redirect_uris":["https://client.example/callback"]}`
	req := httptest.NewRequest(http.MethodPost, "https://nexus.example/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), `"error":"server_error"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(strings.ToLower(res.Body.String()), "database") || strings.Contains(strings.ToLower(res.Body.String()), "closed") {
		t.Fatalf("store error leaked to OAuth client: %s", res.Body.String())
	}
}
