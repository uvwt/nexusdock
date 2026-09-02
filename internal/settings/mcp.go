package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrMCPUnavailable = errors.New("MCP 设置存储不可用")

type MCPView struct {
	MCPAppsEnabled bool   `json:"mcp_apps_enabled"`
	Persisted      bool   `json:"persisted"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type MCPStore struct {
	db             *sql.DB
	defaultEnabled bool
	now            func() time.Time
}

func NewMCPStore(db *sql.DB, defaultEnabled bool) (*MCPStore, error) {
	if db == nil {
		return nil, ErrMCPUnavailable
	}
	return &MCPStore{db: db, defaultEnabled: defaultEnabled, now: time.Now}, nil
}

func (s *MCPStore) Load(ctx context.Context) (bool, MCPView, error) {
	if s == nil || s.db == nil {
		return false, MCPView{}, ErrMCPUnavailable
	}
	var enabled int
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT mcp_apps_enabled, updated_at FROM mcp_settings WHERE singleton_id = 1`).Scan(&enabled, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return s.defaultEnabled, MCPView{MCPAppsEnabled: s.defaultEnabled}, nil
	}
	if err != nil {
		return false, MCPView{}, fmt.Errorf("读取 MCP 设置: %w", err)
	}
	value := enabled == 1
	return value, MCPView{MCPAppsEnabled: value, Persisted: true, UpdatedAt: updatedAt}, nil
}

func (s *MCPStore) Update(ctx context.Context, enabled bool) (MCPView, error) {
	if s == nil || s.db == nil {
		return MCPView{}, ErrMCPUnavailable
	}
	updatedAt := s.now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO mcp_settings(singleton_id, mcp_apps_enabled, updated_at) VALUES(1, ?, ?)
		ON CONFLICT(singleton_id) DO UPDATE SET mcp_apps_enabled=excluded.mcp_apps_enabled, updated_at=excluded.updated_at`, boolInt(enabled), updatedAt)
	if err != nil {
		return MCPView{}, fmt.Errorf("保存 MCP 设置: %w", err)
	}
	return MCPView{MCPAppsEnabled: enabled, Persisted: true, UpdatedAt: updatedAt}, nil
}
