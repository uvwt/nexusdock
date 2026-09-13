package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/uvwt/nexusdock/internal/core"
)

func TestIssueAuthenticateScopeAndRevoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := core.OpenSQLite(ctx, filepath.Join(t.TempDir(), "nexus.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := core.EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	issued, err := service.IssueToken(ctx, core.Actor{Type: core.ActorAgent, ID: "agent-1"}, "agent_token", []string{"runs:read", "runs:*", "runs:read"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.HasScope("runs:write") || principal.HasScope("audit:read") {
		t.Fatalf("unexpected scope behavior: %#v", principal.Scopes)
	}
	if err := service.Revoke(ctx, issued.ID, core.Actor{Type: core.ActorUser, ID: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, issued.Token); core.ErrorCodeOf(err) != core.CodeTokenRevoked {
		t.Fatalf("authenticate revoked token error = %v, want TOKEN_REVOKED", err)
	}
}
