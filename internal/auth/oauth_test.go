package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func TestOAuthAuthorizationCodeAndRefreshRotation(t *testing.T) {
	service, _, _ := newWebAuthTestService(t)
	ctx := t.Context()
	if err := service.InitializeAdmin(ctx, "owner", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	session, err := service.Login(ctx, "owner", "correct horse battery staple", "192.0.2.0/24", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	oauth := NewOAuthService(service.db)
	client, err := oauth.RegisterClient(ctx, OAuthClientRegistration{
		Name: "Test MCP", RedirectURIs: []string{"https://client.example/callback"},
		GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	resource := "https://nexus.example/mcp"
	code, err := oauth.IssueAuthorizationCode(ctx, OAuthAuthorizationInput{
		ClientID: client.ID, UserID: session.Session.UserID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: challenge, Resource: resource, Scope: "mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oauth.ExchangeAuthorizationCode(ctx, client.ID, code, client.RedirectURIs[0], "wrong-verifier-value-that-is-long-enough-ABCDEFG", resource); err == nil {
		t.Fatal("wrong PKCE verifier was accepted")
	}
	issued, err := oauth.ExchangeAuthorizationCode(ctx, client.ID, code, client.RedirectURIs[0], verifier, resource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oauth.ExchangeAuthorizationCode(ctx, client.ID, code, client.RedirectURIs[0], "wrong-verifier-value-that-is-long-enough-ABCDEFG", resource); err == nil {
		t.Fatal("authorization code replay with wrong PKCE verifier was accepted")
	}
	if _, err := oauth.AuthenticateAccess(ctx, issued.AccessToken, resource); err != nil {
		t.Fatalf("invalid replay attempt revoked token family: %v", err)
	}
	if _, err := oauth.ExchangeAuthorizationCode(ctx, client.ID, code, client.RedirectURIs[0], verifier, resource); err == nil {
		t.Fatal("authorization code was reusable")
	}
	if _, err := oauth.AuthenticateAccess(ctx, issued.AccessToken, resource); err == nil {
		t.Fatal("authorization code replay did not revoke the issued token family")
	}

	// 使用新的授权码继续验证正常 refresh 生命周期，避免 replay 吊销影响后续断言。
	code, err = oauth.IssueAuthorizationCode(ctx, OAuthAuthorizationInput{
		ClientID: client.ID, UserID: session.Session.UserID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: challenge, Resource: resource, Scope: "mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err = oauth.ExchangeAuthorizationCode(ctx, client.ID, code, client.RedirectURIs[0], verifier, resource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oauth.AuthenticateAccess(ctx, issued.AccessToken, "https://nexus.example/v1/system/status"); err == nil {
		t.Fatal("MCP token escaped its resource binding")
	}
	rotated, err := oauth.Refresh(ctx, client.ID, issued.RefreshToken, resource)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RefreshToken == issued.RefreshToken || rotated.AccessToken == issued.AccessToken {
		t.Fatal("refresh did not rotate both tokens")
	}
	if _, err := oauth.AuthenticateAccess(ctx, rotated.AccessToken, resource); err != nil {
		t.Fatalf("rotated access token rejected before replay: %v", err)
	}
	if _, err := oauth.Refresh(ctx, client.ID, issued.RefreshToken, resource); err == nil {
		t.Fatal("old refresh token remained usable")
	}
	if _, err := oauth.AuthenticateAccess(ctx, issued.AccessToken, resource); err == nil {
		t.Fatal("old access token remained usable after refresh rotation")
	}
	if _, err := oauth.AuthenticateAccess(ctx, rotated.AccessToken, resource); err == nil {
		t.Fatal("refresh token replay did not revoke the rotated access token")
	}
	if _, err := oauth.Refresh(ctx, client.ID, rotated.RefreshToken, resource); err == nil {
		t.Fatal("refresh token replay did not revoke the token family")
	}
}

func TestOAuthRegistrationPrunesIdleClientsAndEnforcesLimit(t *testing.T) {
	service, _, _ := newWebAuthTestService(t)
	oauth := NewOAuthService(service.db)
	ctx := t.Context()
	fixed := time.Date(2026, 8, 24, 6, 0, 0, 0, time.UTC)
	oauth.now = func() time.Time { return fixed }

	idle, err := oauth.RegisterClient(ctx, OAuthClientRegistration{RedirectURIs: []string{"https://idle.example/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	oauth.now = func() time.Time { return fixed.Add(oauthClientIdleTTL + time.Second) }
	if _, err := oauth.RegisterClient(ctx, OAuthClientRegistration{RedirectURIs: []string{"https://active.example/callback"}}); err != nil {
		t.Fatal(err)
	}
	var idleCount int
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM oauth_clients WHERE id = ?`, idle.ID).Scan(&idleCount); err != nil {
		t.Fatal(err)
	}
	if idleCount != 0 {
		t.Fatal("idle OAuth client was not pruned")
	}

	for i := 1; i < oauthClientLimit; i++ {
		if _, err := oauth.RegisterClient(ctx, OAuthClientRegistration{RedirectURIs: []string{"https://client.example/callback"}}); err != nil {
			t.Fatalf("fill OAuth client capacity at %d: %v", i, err)
		}
	}
	if _, err := oauth.RegisterClient(ctx, OAuthClientRegistration{RedirectURIs: []string{"https://overflow.example/callback"}}); !errors.Is(err, ErrOAuthClientLimit) {
		t.Fatalf("registration over capacity err=%v want=%v", err, ErrOAuthClientLimit)
	}
}

func TestOAuthRegistrationRedirectPolicy(t *testing.T) {
	service, _, _ := newWebAuthTestService(t)
	oauth := NewOAuthService(service.db)
	for _, redirect := range []string{"http://example.com/callback", "javascript:alert(1)", "https://user@example.com/callback", "otherapp://mcp/oauth/callback"} {
		if _, err := oauth.RegisterClient(t.Context(), OAuthClientRegistration{RedirectURIs: []string{redirect}}); err == nil {
			t.Fatalf("unsafe redirect accepted: %s", redirect)
		}
	}
	if _, err := oauth.RegisterClient(t.Context(), OAuthClientRegistration{RedirectURIs: []string{"http://127.0.0.1:8787/callback"}}); err != nil {
		t.Fatalf("loopback redirect rejected: %v", err)
	}
	for _, redirect := range []string{"grokbot://mcp/oauth/callback", "cursor://anysphere.cursor-mcp/oauth/callback"} {
		if _, err := oauth.RegisterClient(t.Context(), OAuthClientRegistration{RedirectURIs: []string{redirect}}); err != nil {
			t.Fatalf("known desktop callback rejected: %s: %v", redirect, err)
		}
	}
}

func TestEquivalentResourceURINormalizesHostAndDefaultPort(t *testing.T) {
	for _, pair := range [][2]string{
		{"HTTPS://NEXUS.EXAMPLE/mcp", "https://nexus.example/mcp"},
		{"https://nexus.example:443/mcp", "https://nexus.example/mcp"},
		{"http://LOCALHOST:80/mcp", "http://localhost/mcp"},
	} {
		if !EquivalentResourceURI(pair[0], pair[1]) {
			t.Fatalf("resources should be equivalent: %q %q", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{
		{"https://nexus.example/mcp", "https://nexus.example/other"},
		{"https://user@nexus.example/mcp", "https://nexus.example/mcp"},
		{"https://nexus.example/mcp?a=1", "https://nexus.example/mcp"},
	} {
		if EquivalentResourceURI(pair[0], pair[1]) {
			t.Fatalf("resources should differ: %q %q", pair[0], pair[1])
		}
	}
}

func TestPasswordChangeRevokesOAuthGrant(t *testing.T) {
	service, _, _ := newWebAuthTestService(t)
	ctx := t.Context()
	if err := service.InitializeAdmin(ctx, "owner", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	loggedIn, err := service.Login(ctx, "owner", "correct horse battery staple", "192.0.2.0/24", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	oauth := NewOAuthService(service.db)
	client, err := oauth.RegisterClient(ctx, OAuthClientRegistration{RedirectURIs: []string{"https://client.example/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	verifier := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	digest := sha256.Sum256([]byte(verifier))
	code, err := oauth.IssueAuthorizationCode(ctx, OAuthAuthorizationInput{ClientID: client.ID, UserID: loggedIn.Session.UserID, RedirectURI: client.RedirectURIs[0], CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]), Resource: "https://nexus.example/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := oauth.ExchangeAuthorizationCode(ctx, client.ID, code, client.RedirectURIs[0], verifier, "https://nexus.example/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSecret(ctx, loggedIn.Session.UserID, "correct horse battery staple", "another correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if _, err := oauth.AuthenticateAccess(ctx, issued.AccessToken, "https://nexus.example/mcp"); err == nil {
		t.Fatal("OAuth grant survived administrator password change")
	}
}
