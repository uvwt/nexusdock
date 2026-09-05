package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/core"
)

const (
	oauthAuthorizationCodeTTL = 10 * time.Minute
	oauthAccessTokenTTL       = time.Hour
	oauthRefreshTokenTTL      = 90 * 24 * time.Hour
	oauthClientIdleTTL        = 180 * 24 * time.Hour
	oauthClientLimit          = 1000
)

var pkceValuePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

var (
	ErrInvalidOAuthClientMetadata = errors.New("invalid OAuth client metadata")
	ErrOAuthClientLimit           = errors.New("OAuth client registration limit reached")
)

type OAuthService struct {
	db  *sql.DB
	now func() time.Time
}

type OAuthClient struct {
	ID                      string    `json:"client_id"`
	Name                    string    `json:"client_name,omitempty"`
	RedirectURIs            []string  `json:"redirect_uris"`
	GrantTypes              []string  `json:"grant_types"`
	ResponseTypes           []string  `json:"response_types"`
	TokenEndpointAuthMethod string    `json:"token_endpoint_auth_method"`
	CreatedAt               time.Time `json:"-"`
	LastUsedAt              time.Time `json:"-"`
}

type OAuthClientRegistration struct {
	Name                    string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

type OAuthAuthorizationInput struct {
	ClientID      string
	UserID        string
	RedirectURI   string
	CodeChallenge string
	Resource      string
	Scope         string
}

type OAuthIssuedTokens struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	Scope            string
	UserID           string
	ClientID         string
	Resource         string
}

type OAuthAccess struct {
	GrantID   string
	UserID    string
	ClientID  string
	Resource  string
	Scope     string
	ExpiresAt time.Time
}

type oauthCodeRecord struct {
	ClientID      string
	UserID        string
	RedirectURI   string
	CodeChallenge string
	Resource      string
	Scope         string
	ExpiresAt     time.Time
}

func NewOAuthService(db *sql.DB) *OAuthService {
	if db == nil {
		return nil
	}
	return &OAuthService{db: db, now: time.Now}
}

func (s *OAuthService) RegisterClient(ctx context.Context, input OAuthClientRegistration) (OAuthClient, error) {
	if s == nil || s.db == nil {
		return OAuthClient{}, errors.New("OAuth store is unavailable")
	}
	redirects, err := normalizeOAuthRedirectURIs(input.RedirectURIs)
	if err != nil {
		return OAuthClient{}, fmt.Errorf("%w: %v", ErrInvalidOAuthClientMetadata, err)
	}
	grantTypes, err := normalizeOAuthValues(input.GrantTypes, []string{"authorization_code", "refresh_token"}, []string{"authorization_code", "refresh_token"}, "grant_types")
	if err != nil {
		return OAuthClient{}, fmt.Errorf("%w: %v", ErrInvalidOAuthClientMetadata, err)
	}
	if !containsOAuthValue(grantTypes, "authorization_code") {
		return OAuthClient{}, fmt.Errorf("%w: grant_types must include authorization_code", ErrInvalidOAuthClientMetadata)
	}
	responseTypes, err := normalizeOAuthValues(input.ResponseTypes, []string{"code"}, []string{"code"}, "response_types")
	if err != nil {
		return OAuthClient{}, fmt.Errorf("%w: %v", ErrInvalidOAuthClientMetadata, err)
	}
	method := strings.TrimSpace(input.TokenEndpointAuthMethod)
	if method == "" {
		method = "none"
	}
	if method != "none" {
		return OAuthClient{}, fmt.Errorf("%w: token_endpoint_auth_method must be none", ErrInvalidOAuthClientMetadata)
	}
	name := strings.TrimSpace(input.Name)
	if len([]rune(name)) > 120 {
		return OAuthClient{}, fmt.Errorf("%w: client_name is too long", ErrInvalidOAuthClientMetadata)
	}
	clientID, err := core.NewID("oauthc")
	if err != nil {
		return OAuthClient{}, err
	}
	now := s.now().UTC()
	redirectJSON, _ := json.Marshal(redirects)
	grantJSON, _ := json.Marshal(grantTypes)
	responseJSON, _ := json.Marshal(responseTypes)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthClient{}, fmt.Errorf("begin OAuth client registration: %w", err)
	}
	defer tx.Rollback()

	// 动态注册是公网入口。淘汰长期未使用 client，并顺手清理已过期的一次性状态，
	// 避免攻击者通过合法 DCR/OAuth 请求让 SQLite 永久增长。
	cutoff := now.Add(-oauthClientIdleTTL).Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_clients WHERE last_used_at < ?`, cutoff); err != nil {
		return OAuthClient{}, fmt.Errorf("prune idle OAuth clients: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_authorization_codes WHERE expires_at <= ?`, now.Format(time.RFC3339Nano)); err != nil {
		return OAuthClient{}, fmt.Errorf("prune OAuth authorization codes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_refresh_token_history WHERE expires_at <= ?`, now.Format(time.RFC3339Nano)); err != nil {
		return OAuthClient{}, fmt.Errorf("prune OAuth refresh history: %w", err)
	}

	result, err := tx.ExecContext(ctx, `INSERT INTO oauth_clients(
		id, client_name, redirect_uris_json, grant_types_json, response_types_json,
		token_endpoint_auth_method, created_at, last_used_at
	) SELECT ?, ?, ?, ?, ?, 'none', ?, ?
	WHERE (SELECT COUNT(*) FROM oauth_clients) < ?`, clientID, name, string(redirectJSON), string(grantJSON), string(responseJSON), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), oauthClientLimit)
	if err != nil {
		return OAuthClient{}, fmt.Errorf("register OAuth client: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return OAuthClient{}, fmt.Errorf("read OAuth client registration result: %w", err)
	}
	if changed != 1 {
		return OAuthClient{}, ErrOAuthClientLimit
	}
	if err := tx.Commit(); err != nil {
		return OAuthClient{}, fmt.Errorf("commit OAuth client registration: %w", err)
	}
	return OAuthClient{ID: clientID, Name: name, RedirectURIs: redirects, GrantTypes: grantTypes, ResponseTypes: responseTypes, TokenEndpointAuthMethod: method, CreatedAt: now, LastUsedAt: now}, nil
}

func (s *OAuthService) Client(ctx context.Context, clientID string) (OAuthClient, error) {
	if s == nil || s.db == nil {
		return OAuthClient{}, errors.New("OAuth store is unavailable")
	}
	var client OAuthClient
	var redirectsJSON, grantsJSON, responsesJSON, created, lastUsed string
	err := s.db.QueryRowContext(ctx, `SELECT id, client_name, redirect_uris_json, grant_types_json,
		response_types_json, token_endpoint_auth_method, created_at, last_used_at
		FROM oauth_clients WHERE id = ?`, strings.TrimSpace(clientID)).Scan(
		&client.ID, &client.Name, &redirectsJSON, &grantsJSON, &responsesJSON,
		&client.TokenEndpointAuthMethod, &created, &lastUsed,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthClient{}, core.NewError(core.CodeNotFound, "OAuth client not found", nil)
	}
	if err != nil {
		return OAuthClient{}, fmt.Errorf("read OAuth client: %w", err)
	}
	if err := json.Unmarshal([]byte(redirectsJSON), &client.RedirectURIs); err != nil {
		return OAuthClient{}, fmt.Errorf("decode OAuth redirects: %w", err)
	}
	if err := json.Unmarshal([]byte(grantsJSON), &client.GrantTypes); err != nil {
		return OAuthClient{}, fmt.Errorf("decode OAuth grants: %w", err)
	}
	if err := json.Unmarshal([]byte(responsesJSON), &client.ResponseTypes); err != nil {
		return OAuthClient{}, fmt.Errorf("decode OAuth responses: %w", err)
	}
	client.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	client.LastUsedAt, _ = time.Parse(time.RFC3339Nano, lastUsed)
	return client, nil
}

func (s *OAuthService) ClientAllows(ctx context.Context, clientID, redirectURI string) (OAuthClient, bool) {
	client, err := s.Client(ctx, clientID)
	if err != nil {
		return OAuthClient{}, false
	}
	for _, candidate := range client.RedirectURIs {
		if candidate == redirectURI {
			return client, true
		}
	}
	return client, false
}

func (s *OAuthService) IssueAuthorizationCode(ctx context.Context, input OAuthAuthorizationInput) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("OAuth store is unavailable")
	}
	if strings.TrimSpace(input.UserID) == "" || strings.TrimSpace(input.ClientID) == "" || strings.TrimSpace(input.Resource) == "" {
		return "", fmt.Errorf("OAuth authorization identity is incomplete")
	}
	if !ValidPKCEValue(input.CodeChallenge) {
		return "", fmt.Errorf("invalid PKCE code challenge")
	}
	if _, ok := s.ClientAllows(ctx, input.ClientID, input.RedirectURI); !ok {
		return "", fmt.Errorf("OAuth redirect is not registered")
	}
	raw, err := randomToken(32)
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	expires := now.Add(oauthAuthorizationCodeTTL)
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_authorization_codes(
		code_hash, client_id, user_id, redirect_uri, code_challenge, resource, scope,
		created_at, expires_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, digestString(raw), input.ClientID, input.UserID,
		input.RedirectURI, input.CodeChallenge, input.Resource, normalizeOAuthScope(input.Scope),
		now.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano))
	if err != nil {
		return "", fmt.Errorf("store OAuth authorization code: %w", err)
	}
	return raw, nil
}

func (s *OAuthService) ExchangeAuthorizationCode(ctx context.Context, clientID, rawCode, redirectURI, verifier, resource string) (OAuthIssuedTokens, error) {
	if s == nil || s.db == nil {
		return OAuthIssuedTokens{}, errors.New("OAuth store is unavailable")
	}
	if !ValidPKCEValue(verifier) {
		return OAuthIssuedTokens{}, fmt.Errorf("invalid PKCE verifier")
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("begin OAuth code exchange: %w", err)
	}
	defer tx.Rollback()
	var record oauthCodeRecord
	var expires string
	var usedAt, grantID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT client_id, user_id, redirect_uri, code_challenge, resource, scope, expires_at, used_at, grant_id
		FROM oauth_authorization_codes WHERE code_hash = ?`, digestString(rawCode)).Scan(
		&record.ClientID, &record.UserID, &record.RedirectURI, &record.CodeChallenge, &record.Resource, &record.Scope, &expires, &usedAt, &grantID,
	)
	if err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("invalid authorization code")
	}
	record.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil || !now.Before(record.ExpiresAt) || record.ClientID != clientID || record.RedirectURI != redirectURI || !EquivalentResourceURI(record.Resource, resource) || !verifyPKCE(verifier, record.CodeChallenge) {
		return OAuthIssuedTokens{}, fmt.Errorf("invalid authorization code")
	}
	if usedAt.Valid {
		// RFC 6749 要求拒绝授权码重放，并建议在可关联时吊销基于该授权码签发的 token。
		// 只有完整 PKCE/redirect/resource 校验都通过后才执行吊销，避免仅泄露 code 就能触发拒绝服务。
		if grantID.Valid && grantID.String != "" {
			nowText := now.Format(time.RFC3339Nano)
			if _, revokeErr := tx.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at = COALESCE(revoked_at, ?), updated_at = ? WHERE id = ?`, nowText, nowText, grantID.String); revokeErr != nil {
				return OAuthIssuedTokens{}, fmt.Errorf("revoke replayed OAuth authorization grant: %w", revokeErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return OAuthIssuedTokens{}, fmt.Errorf("commit OAuth authorization replay revocation: %w", commitErr)
			}
		}
		return OAuthIssuedTokens{}, fmt.Errorf("invalid authorization code")
	}

	issued, issuedGrantID, err := issueOAuthGrant(ctx, tx, now, record.ClientID, record.UserID, record.Resource, record.Scope)
	if err != nil {
		return OAuthIssuedTokens{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE oauth_authorization_codes SET used_at = ?, grant_id = ? WHERE code_hash = ? AND used_at IS NULL`, now.Format(time.RFC3339Nano), issuedGrantID, digestString(rawCode))
	if err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("consume OAuth authorization code: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return OAuthIssuedTokens{}, fmt.Errorf("invalid authorization code")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_clients SET last_used_at = ? WHERE id = ?`, now.Format(time.RFC3339Nano), clientID); err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("touch OAuth client: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("commit OAuth code exchange: %w", err)
	}
	return issued, nil
}

func (s *OAuthService) Refresh(ctx context.Context, clientID, rawRefresh, resource string) (OAuthIssuedTokens, error) {
	if s == nil || s.db == nil {
		return OAuthIssuedTokens{}, errors.New("OAuth store is unavailable")
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("begin OAuth refresh: %w", err)
	}
	defer tx.Rollback()
	refreshHash := digestString(rawRefresh)
	var grantID, storedClientID, userID, storedResource, scope, refreshExpires string
	err = tx.QueryRowContext(ctx, `SELECT id, client_id, user_id, resource, scope, refresh_expires_at
		FROM oauth_grants WHERE refresh_token_hash = ? AND revoked_at IS NULL`, refreshHash).Scan(
		&grantID, &storedClientID, &userID, &storedResource, &scope, &refreshExpires,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// 已轮换 refresh token 再次出现，说明同一 token family 发生重放。
		// 只有 client/resource 也匹配时才吊销，避免错误 client_id 造成拒绝服务。
		var replayGrantID, replayClientID, replayResource string
		replayErr := tx.QueryRowContext(ctx, `SELECT h.grant_id, g.client_id, g.resource
			FROM oauth_refresh_token_history h
			JOIN oauth_grants g ON g.id = h.grant_id
			WHERE h.token_hash = ? AND h.expires_at > ?`, refreshHash, now.Format(time.RFC3339Nano)).Scan(&replayGrantID, &replayClientID, &replayResource)
		if replayErr == nil && replayClientID == clientID && EquivalentResourceURI(replayResource, resource) {
			nowText := now.Format(time.RFC3339Nano)
			if _, revokeErr := tx.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at = COALESCE(revoked_at, ?), updated_at = ? WHERE id = ?`, nowText, nowText, replayGrantID); revokeErr != nil {
				return OAuthIssuedTokens{}, fmt.Errorf("revoke replayed OAuth grant: %w", revokeErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return OAuthIssuedTokens{}, fmt.Errorf("commit OAuth replay revocation: %w", commitErr)
			}
		}
		return OAuthIssuedTokens{}, fmt.Errorf("invalid refresh token")
	}
	if err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("read OAuth refresh grant: %w", err)
	}
	if storedClientID != clientID || !EquivalentResourceURI(storedResource, resource) {
		return OAuthIssuedTokens{}, fmt.Errorf("invalid refresh token")
	}
	refreshExpiry, err := time.Parse(time.RFC3339Nano, refreshExpires)
	if err != nil || !now.Before(refreshExpiry) {
		return OAuthIssuedTokens{}, fmt.Errorf("invalid refresh token")
	}
	access, err := randomToken(32)
	if err != nil {
		return OAuthIssuedTokens{}, err
	}
	refresh, err := randomToken(32)
	if err != nil {
		return OAuthIssuedTokens{}, err
	}
	accessExpiry := now.Add(oauthAccessTokenTTL)
	if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_refresh_token_history(token_hash, grant_id, expires_at) VALUES(?, ?, ?)`, refreshHash, grantID, refreshExpiry.Format(time.RFC3339Nano)); err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("remember rotated OAuth refresh token: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE oauth_grants SET access_token_hash = ?, refresh_token_hash = ?, access_expires_at = ?, updated_at = ?
		WHERE id = ? AND refresh_token_hash = ? AND revoked_at IS NULL`, digestString(access), digestString(refresh), accessExpiry.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), grantID, refreshHash)
	if err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("rotate OAuth refresh token: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return OAuthIssuedTokens{}, fmt.Errorf("invalid refresh token")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_clients SET last_used_at = ? WHERE id = ?`, now.Format(time.RFC3339Nano), clientID); err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("touch OAuth client: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return OAuthIssuedTokens{}, fmt.Errorf("commit OAuth refresh: %w", err)
	}
	return OAuthIssuedTokens{AccessToken: access, RefreshToken: refresh, AccessExpiresAt: accessExpiry, RefreshExpiresAt: refreshExpiry, Scope: scope, UserID: userID, ClientID: clientID, Resource: storedResource}, nil
}

func (s *OAuthService) AuthenticateAccess(ctx context.Context, raw, resource string) (OAuthAccess, error) {
	if s == nil || s.db == nil || strings.TrimSpace(raw) == "" {
		return OAuthAccess{}, fmt.Errorf("invalid OAuth access token")
	}
	var access OAuthAccess
	var expires string
	err := s.db.QueryRowContext(ctx, `SELECT g.id, g.user_id, g.client_id, g.resource, g.scope, g.access_expires_at
		FROM oauth_grants g JOIN users u ON u.id = g.user_id
		WHERE g.access_token_hash = ? AND g.revoked_at IS NULL AND u.status = 'active'`, digestString(raw)).Scan(
		&access.GrantID, &access.UserID, &access.ClientID, &access.Resource, &access.Scope, &expires,
	)
	if err != nil || !EquivalentResourceURI(access.Resource, resource) {
		return OAuthAccess{}, fmt.Errorf("invalid OAuth access token")
	}
	access.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil || !s.now().UTC().Before(access.ExpiresAt) {
		return OAuthAccess{}, fmt.Errorf("invalid OAuth access token")
	}
	return access, nil
}

func (s *OAuthService) RevokeUserGrants(ctx context.Context, userID string) error {
	if s == nil || s.db == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at = COALESCE(revoked_at, ?), updated_at = ? WHERE user_id = ?`, now, now, userID)
	if err != nil {
		return fmt.Errorf("revoke OAuth grants: %w", err)
	}
	return nil
}

func issueOAuthGrant(ctx context.Context, tx *sql.Tx, now time.Time, clientID, userID, resource, scope string) (OAuthIssuedTokens, string, error) {
	access, err := randomToken(32)
	if err != nil {
		return OAuthIssuedTokens{}, "", err
	}
	refresh, err := randomToken(32)
	if err != nil {
		return OAuthIssuedTokens{}, "", err
	}
	grantID, err := core.NewID("oauthg")
	if err != nil {
		return OAuthIssuedTokens{}, "", err
	}
	accessExpiry := now.Add(oauthAccessTokenTTL)
	refreshExpiry := now.Add(oauthRefreshTokenTTL)
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_grants(
		id, client_id, user_id, resource, scope, access_token_hash, refresh_token_hash,
		access_expires_at, refresh_expires_at, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, grantID, clientID, userID, resource, normalizeOAuthScope(scope),
		digestString(access), digestString(refresh), accessExpiry.Format(time.RFC3339Nano), refreshExpiry.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return OAuthIssuedTokens{}, "", fmt.Errorf("create OAuth grant: %w", err)
	}
	return OAuthIssuedTokens{AccessToken: access, RefreshToken: refresh, AccessExpiresAt: accessExpiry, RefreshExpiresAt: refreshExpiry, Scope: normalizeOAuthScope(scope), UserID: userID, ClientID: clientID, Resource: resource}, grantID, nil
}

func ValidPKCEValue(value string) bool { return pkceValuePattern.MatchString(value) }

func verifyPKCE(verifier, challenge string) bool {
	digest := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(digest[:])
	return len(got) == len(challenge) && subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

func EquivalentResourceURI(left, right string) bool {
	leftCanonical, leftOK := canonicalOAuthResourceURI(left)
	rightCanonical, rightOK := canonicalOAuthResourceURI(right)
	return leftOK && rightOK && leftCanonical == rightCanonical
}

func canonicalOAuthResourceURI(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(hostname, ":") {
		hostname = "[" + hostname + "]"
	}
	parsed.Host = hostname
	if port != "" {
		parsed.Host += ":" + port
	}
	return parsed.String(), true
}

func ValidOAuthRedirectURI(raw string) bool {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "grokbot://mcp/oauth/callback", "cursor://anysphere.cursor-mcp/oauth/callback":
		// 桌面客户端的私有 scheme 无法像 HTTPS 一样按 origin 校验，因此只允许已知客户端的完整固定回调。
		return true
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return true
	case "http":
		host := strings.ToLower(parsed.Hostname())
		if host == "localhost" {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	default:
		return false
	}
}

func normalizeOAuthRedirectURIs(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 16 {
		return nil, fmt.Errorf("redirect_uris must contain 1 to 16 entries")
	}
	seen := make(map[string]struct{}, len(values))
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !ValidOAuthRedirectURI(value) {
			return nil, fmt.Errorf("invalid redirect_uri")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		clean = append(clean, value)
	}
	return clean, nil
}

func normalizeOAuthValues(values, supported, defaults []string, label string) ([]string, error) {
	if len(values) == 0 {
		return append([]string(nil), defaults...), nil
	}
	allowed := make(map[string]struct{}, len(supported))
	for _, value := range supported {
		allowed[value] = struct{}{}
	}
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := allowed[value]; !ok {
			return nil, fmt.Errorf("unsupported %s value", label)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		clean = append(clean, value)
	}
	sort.Strings(clean)
	return clean, nil
}

func normalizeOAuthScope(scope string) string {
	if strings.TrimSpace(scope) == "" {
		return "mcp"
	}
	return strings.Join(strings.Fields(scope), " ")
}

func containsOAuthValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
