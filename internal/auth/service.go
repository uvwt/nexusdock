package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/core"
)

type Principal struct {
	Actor     core.Actor `json:"actor"`
	TokenID   string     `json:"token_id"`
	TokenKind string     `json:"token_kind"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (p Principal) HasScope(required string) bool {
	for _, scope := range p.Scopes {
		if scope == "*" || scope == required {
			return true
		}
		if strings.HasSuffix(scope, ":*") && strings.HasPrefix(required, strings.TrimSuffix(scope, "*")) {
			return true
		}
	}
	return false
}

type IssuedToken struct {
	ID        string     `json:"id"`
	Token     string     `json:"token"`
	Principal Principal  `json:"principal"`
	IssuedAt  time.Time  `json:"issued_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type AuthService interface {
	IssueToken(context.Context, core.Actor, string, []string, time.Duration) (IssuedToken, error)
	Authenticate(context.Context, string) (Principal, error)
	Revoke(context.Context, string, core.Actor) error
	CleanupExpired(context.Context) (int64, error)
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service { return &Service{db: db, now: time.Now} }

func (s *Service) IssueToken(ctx context.Context, actor core.Actor, kind string, scopes []string, ttl time.Duration) (IssuedToken, error) {
	if !actor.Valid() {
		return IssuedToken{}, core.NewError(core.CodeValidation, "valid token subject is required", nil)
	}
	if !validKind(actor.Type, kind) {
		return IssuedToken{}, core.NewError(core.CodeValidation, "token kind does not match subject type", nil)
	}
	scopes = normalizeScopes(scopes)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return IssuedToken{}, fmt.Errorf("generate token: %w", err)
	}
	id, err := core.NewID("tok")
	if err != nil {
		return IssuedToken{}, err
	}
	secret := "nx_" + base64.RawURLEncoding.EncodeToString(raw)
	hash := tokenHash(secret)
	issuedAt := s.now().UTC()
	var expiresAt *time.Time
	if ttl > 0 {
		value := issuedAt.Add(ttl)
		expiresAt = &value
	}
	scopeJSON, _ := json.Marshal(scopes)
	var expiry any
	if expiresAt != nil {
		expiry = expiresAt.Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_tokens(
		id, subject_type, subject_id, token_kind, token_hash, scopes_json, issued_at, expires_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, id, actor.Type, actor.ID, kind, hash, string(scopeJSON), issuedAt.Format(time.RFC3339Nano), expiry)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("insert auth token: %w", err)
	}
	principal := Principal{Actor: actor, TokenID: id, TokenKind: kind, Scopes: scopes, ExpiresAt: expiresAt}
	return IssuedToken{ID: id, Token: secret, Principal: principal, IssuedAt: issuedAt, ExpiresAt: expiresAt}, nil
}

func (s *Service) Authenticate(ctx context.Context, secret string) (Principal, error) {
	if strings.TrimSpace(secret) == "" {
		return Principal{}, core.NewError(core.CodeAuthRequired, "bearer token is required", nil)
	}
	var principal Principal
	var actorType string
	var scopesJSON, expires, revoked sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, subject_type, subject_id, token_kind, scopes_json, expires_at, revoked_at
		FROM auth_tokens WHERE token_hash = ?`, tokenHash(secret)).Scan(
		&principal.TokenID, &actorType, &principal.Actor.ID, &principal.TokenKind, &scopesJSON, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, core.NewError(core.CodeInvalidToken, "token is invalid", err)
	}
	if err != nil {
		return Principal{}, fmt.Errorf("lookup auth token: %w", err)
	}
	principal.Actor.Type = core.ActorType(actorType)
	if revoked.Valid {
		return Principal{}, core.NewError(core.CodeTokenRevoked, "token has been revoked", nil)
	}
	if expires.Valid {
		value, err := time.Parse(time.RFC3339Nano, expires.String)
		if err != nil {
			return Principal{}, fmt.Errorf("parse token expiry: %w", err)
		}
		principal.ExpiresAt = &value
		if !s.now().UTC().Before(value) {
			return Principal{}, core.NewError(core.CodeInvalidToken, "token has expired", nil)
		}
	}
	if err := json.Unmarshal([]byte(scopesJSON.String), &principal.Scopes); err != nil {
		return Principal{}, fmt.Errorf("decode token scopes: %w", err)
	}
	return principal, nil
}

func (s *Service) Revoke(ctx context.Context, tokenID string, actor core.Actor) error {
	if tokenID == "" || !actor.Valid() {
		return core.NewError(core.CodeValidation, "token id and revoking actor are required", nil)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE auth_tokens SET revoked_at = ?, revoked_by_type = ?, revoked_by_id = ?
		WHERE id = ? AND revoked_at IS NULL`, s.now().UTC().Format(time.RFC3339Nano), actor.Type, actor.ID, tokenID)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return core.NewError(core.CodeNotFound, "active token not found", nil)
	}
	return nil
}

func (s *Service) CleanupExpired(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM auth_tokens WHERE expires_at IS NOT NULL AND expires_at < ?`, s.now().UTC().Add(-24*time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("cleanup expired tokens: %w", err)
	}
	return result.RowsAffected()
}

func tokenHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func normalizeScopes(scopes []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	sort.Strings(result)
	return result
}

func validKind(actorType core.ActorType, kind string) bool {
	switch actorType {
	case core.ActorUser:
		return kind == "session"
	case core.ActorAgent:
		return kind == "agent_token"
	case core.ActorDevice:
		return kind == "device_token"
	default:
		return false
	}
}
