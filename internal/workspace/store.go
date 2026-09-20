package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/core"
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("Runtime Workspace 数据库不能为空")
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Workspace, error) {
	input, err := normalizeCreate(input)
	if err != nil {
		return Workspace{}, err
	}
	allowedMCP, _ := json.Marshal(input.AllowedMCP)
	contextRoots, _ := json.Marshal(input.ContextRoots)
	designAuthorities, _ := json.Marshal(input.DesignAuthorities)
	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO runtime_workspaces(
		id, name, node_id, project_root, domain, allowed_mcp_json, context_roots_json,
		design_authorities_json, route_authority, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.ID, input.Name, input.NodeID, input.ProjectRoot, input.Domain,
		string(allowedMCP), string(contextRoots), string(designAuthorities), input.RouteAuthority,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		if core.IsSQLiteConflict(err) || strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Workspace{}, ErrExists
		}
		return Workspace{}, fmt.Errorf("创建 Runtime Workspace: %w", err)
	}
	return s.Get(ctx, input.ID)
}

func (s *Store) Get(ctx context.Context, id string) (Workspace, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return Workspace{}, ValidationError{Message: "workspace id 不能为空"}
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, name, node_id, project_root, domain,
		allowed_mcp_json, context_roots_json, design_authorities_json, route_authority,
		created_at, updated_at FROM runtime_workspaces WHERE id = ?`, id)
	item, err := scanWorkspace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	return item, err
}

func (s *Store) List(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, node_id, project_root, domain,
		allowed_mcp_json, context_roots_json, design_authorities_json, route_authority,
		created_at, updated_at FROM runtime_workspaces ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("列出 Runtime Workspace: %w", err)
	}
	defer rows.Close()
	items := make([]Workspace, 0)
	for rows.Next() {
		item, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 Runtime Workspace: %w", err)
	}
	return items, nil
}

func (s *Store) Update(ctx context.Context, id string, input UpdateInput) (Workspace, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Workspace{}, err
	}
	create := CreateInput{
		ID: current.ID, Name: current.Name, NodeID: current.NodeID, ProjectRoot: current.ProjectRoot,
		Domain: current.Domain, AllowedMCP: current.AllowedMCP, ContextRoots: current.ContextRoots,
		DesignAuthorities: current.DesignAuthorities, RouteAuthority: current.RouteAuthority,
	}
	if input.Name != nil {
		create.Name = *input.Name
	}
	if input.NodeID != nil {
		create.NodeID = *input.NodeID
	}
	if input.ProjectRoot != nil {
		create.ProjectRoot = *input.ProjectRoot
	}
	if input.Domain != nil {
		create.Domain = *input.Domain
	}
	if input.AllowedMCP != nil {
		create.AllowedMCP = *input.AllowedMCP
	}
	if input.ContextRoots != nil {
		create.ContextRoots = *input.ContextRoots
	}
	if input.DesignAuthorities != nil {
		create.DesignAuthorities = *input.DesignAuthorities
	}
	if input.RouteAuthority != nil {
		create.RouteAuthority = *input.RouteAuthority
	}
	create, err = normalizeCreate(create)
	if err != nil {
		return Workspace{}, err
	}
	allowedMCP, _ := json.Marshal(create.AllowedMCP)
	contextRoots, _ := json.Marshal(create.ContextRoots)
	designAuthorities, _ := json.Marshal(create.DesignAuthorities)
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE runtime_workspaces SET
		name = ?, node_id = ?, project_root = ?, domain = ?, allowed_mcp_json = ?,
		context_roots_json = ?, design_authorities_json = ?, route_authority = ?, updated_at = ?
		WHERE id = ?`, create.Name, create.NodeID, create.ProjectRoot, create.Domain,
		string(allowedMCP), string(contextRoots), string(designAuthorities), create.RouteAuthority,
		now.Format(time.RFC3339Nano), current.ID)
	if err != nil {
		return Workspace{}, fmt.Errorf("更新 Runtime Workspace: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return Workspace{}, ErrNotFound
	}
	return s.Get(ctx, current.ID)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return ValidationError{Message: "workspace id 不能为空"}
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM runtime_workspaces WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除 Runtime Workspace: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanWorkspace(scanner rowScanner) (Workspace, error) {
	var item Workspace
	var allowedMCP, contextRoots, designAuthorities, createdAt, updatedAt string
	if err := scanner.Scan(&item.ID, &item.Name, &item.NodeID, &item.ProjectRoot, &item.Domain,
		&allowedMCP, &contextRoots, &designAuthorities, &item.RouteAuthority, &createdAt, &updatedAt); err != nil {
		return Workspace{}, err
	}
	if err := json.Unmarshal([]byte(allowedMCP), &item.AllowedMCP); err != nil {
		return Workspace{}, fmt.Errorf("解析 Workspace allowed_mcp: %w", err)
	}
	if err := json.Unmarshal([]byte(contextRoots), &item.ContextRoots); err != nil {
		return Workspace{}, fmt.Errorf("解析 Workspace context_roots: %w", err)
	}
	if err := json.Unmarshal([]byte(designAuthorities), &item.DesignAuthorities); err != nil {
		return Workspace{}, fmt.Errorf("解析 Workspace design_authorities: %w", err)
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return item, nil
}
