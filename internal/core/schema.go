package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CurrentSchemaVersion 是当前程序对应的控制库 Schema 版本。
// PRAGMA user_version 是唯一的应用 Schema 版本来源，不引入 schema_migrations 表，
// 避免恢复 a923781 已移除的旧文件式迁移系统（SQL 文件 + checksum 校验 + 备份钩子的复杂度）。
const CurrentSchemaVersion = 4

// currentSchema 是全新空库初始化用的当前结构。
// 空库没有历史数据需要逐版本变换，直接建当前结构并写入版本号：
// 一是省掉对空库逐版本重放 IF NOT EXISTS 的空转，二是保证未来不可重放的数据变换类
// migration（拆列、回填等）永远不会作用在新库上。已存在的库一律走 schemaMigrations
// 逐版本升级，两边结构收敛一致由 tests/migration 的收敛测试保证。
var currentSchema = []string{
	`CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS auth_tokens (
    id TEXT PRIMARY KEY,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('user', 'agent', 'device')),
    subject_id TEXT NOT NULL,
    token_kind TEXT NOT NULL CHECK (token_kind IN ('session', 'agent_token', 'device_token')),
    token_hash TEXT NOT NULL UNIQUE,
    scopes_json TEXT NOT NULL,
    issued_at TEXT NOT NULL,
    expires_at TEXT,
    revoked_at TEXT,
    revoked_by_type TEXT,
    revoked_by_id TEXT
)`,
	`CREATE INDEX IF NOT EXISTS idx_auth_tokens_subject ON auth_tokens(subject_type, subject_id)`,
	`CREATE INDEX IF NOT EXISTS idx_auth_tokens_active ON auth_tokens(token_hash, revoked_at, expires_at)`,
	`CREATE TABLE IF NOT EXISTS audit_events (
    id TEXT PRIMARY KEY,
    occurred_at TEXT NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('user', 'agent', 'device', 'system')),
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    result TEXT NOT NULL,
    risk TEXT NOT NULL DEFAULT 'low',
    approval TEXT NOT NULL DEFAULT 'not_required',
    run_id TEXT,
    request_id TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}'
)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_events_time ON audit_events(occurred_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_events_object ON audit_events(object_type, object_id, occurred_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_events_actor ON audit_events(actor_type, actor_id, occurred_at DESC)`,
	`CREATE TRIGGER IF NOT EXISTS audit_events_no_update
BEFORE UPDATE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit_events are append-only');
END`,
	`CREATE TRIGGER IF NOT EXISTS audit_events_no_delete
BEFORE DELETE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit_events are append-only');
END`,
	`CREATE TABLE IF NOT EXISTS user_credentials (
    user_id TEXT PRIMARY KEY,
    password_hash TEXT NOT NULL,
    password_algorithm TEXT NOT NULL,
    must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1)),
    password_changed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
)`,
	`CREATE TABLE IF NOT EXISTS user_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    csrf_salt TEXT NOT NULL,
    remember_me INTEGER NOT NULL DEFAULT 0 CHECK (remember_me IN (0, 1)),
    ip_prefix TEXT NOT NULL DEFAULT '',
    user_agent_summary TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    idle_expires_at TEXT NOT NULL,
    absolute_expires_at TEXT NOT NULL,
    revoked_at TEXT,
    revoke_reason TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
)`,
	`CREATE INDEX IF NOT EXISTS idx_user_sessions_user_active
    ON user_sessions(user_id, revoked_at, absolute_expires_at)`,
	`CREATE INDEX IF NOT EXISTS idx_user_sessions_token
    ON user_sessions(token_hash, revoked_at)`,
	`CREATE TABLE IF NOT EXISTS oauth_clients (
    id TEXT PRIMARY KEY,
    client_name TEXT NOT NULL DEFAULT '',
    redirect_uris_json TEXT NOT NULL,
    grant_types_json TEXT NOT NULL,
    response_types_json TEXT NOT NULL,
    token_endpoint_auth_method TEXT NOT NULL DEFAULT 'none' CHECK (token_endpoint_auth_method = 'none'),
    created_at TEXT NOT NULL,
    last_used_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    code_hash TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    redirect_uri TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    resource TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'mcp',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at TEXT,
    grant_id TEXT,
    FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
)`,
	`CREATE INDEX IF NOT EXISTS idx_oauth_authorization_codes_expiry
    ON oauth_authorization_codes(expires_at, used_at)`,
	`CREATE TABLE IF NOT EXISTS oauth_grants (
    id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    resource TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'mcp',
    access_token_hash TEXT NOT NULL UNIQUE,
    refresh_token_hash TEXT NOT NULL UNIQUE,
    access_expires_at TEXT NOT NULL,
    refresh_expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    revoked_at TEXT,
    FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
)`,
	`CREATE INDEX IF NOT EXISTS idx_oauth_grants_access
    ON oauth_grants(access_token_hash, revoked_at, access_expires_at)`,
	`CREATE INDEX IF NOT EXISTS idx_oauth_grants_refresh
    ON oauth_grants(refresh_token_hash, revoked_at, refresh_expires_at)`,
	`CREATE INDEX IF NOT EXISTS idx_oauth_grants_user
    ON oauth_grants(user_id, revoked_at)`,
	`CREATE TABLE IF NOT EXISTS oauth_refresh_token_history (
    token_hash TEXT PRIMARY KEY,
    grant_id TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    FOREIGN KEY (grant_id) REFERENCES oauth_grants(id) ON DELETE CASCADE
)`,
	`CREATE INDEX IF NOT EXISTS idx_oauth_refresh_token_history_expiry
    ON oauth_refresh_token_history(expires_at)`,
	`CREATE TABLE IF NOT EXISTS login_throttles (
    key_type TEXT NOT NULL CHECK (key_type IN ('account', 'ip')),
    key_value TEXT NOT NULL,
    failures INTEGER NOT NULL DEFAULT 0 CHECK (failures >= 0),
    blocked_until TEXT,
    last_failed_at TEXT NOT NULL,
    PRIMARY KEY (key_type, key_value)
)`,
	`CREATE TABLE IF NOT EXISTS agentdock_devices (
    id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    version TEXT NOT NULL DEFAULT '',
    protocol_version TEXT NOT NULL DEFAULT '',
    os TEXT NOT NULL DEFAULT '',
    arch TEXT NOT NULL DEFAULT '',
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    tool_contract_hash TEXT NOT NULL DEFAULT '',
    last_seen_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS agentdock_pairing_codes (
    id TEXT PRIMARY KEY,
    code_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    used_at TEXT,
    created_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS agentdock_tool_contracts (
    node_id TEXT PRIMARY KEY,
    descriptors_json TEXT NOT NULL DEFAULT '[]',
    updated_at TEXT NOT NULL,
    FOREIGN KEY (node_id) REFERENCES agentdock_devices(id) ON DELETE CASCADE
)`,
	`CREATE TABLE IF NOT EXISTS agentdock_ui_resources (
    node_id TEXT PRIMARY KEY,
    resources_json TEXT NOT NULL DEFAULT '[]',
    updated_at TEXT NOT NULL,
    FOREIGN KEY (node_id) REFERENCES agentdock_devices(id) ON DELETE CASCADE
)`,
	`CREATE TABLE IF NOT EXISTS agentdock_bridge_capabilities (
    node_id TEXT PRIMARY KEY,
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    updated_at TEXT NOT NULL,
    FOREIGN KEY (node_id) REFERENCES agentdock_devices(id) ON DELETE CASCADE
)`,
	`CREATE TABLE IF NOT EXISTS agentdock_published_tool_contracts (
    tool_name TEXT PRIMARY KEY,
    descriptor_json TEXT NOT NULL,
    source_node_id TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS agentdock_published_tool_variants (
    tool_name TEXT NOT NULL,
    semantic_hash TEXT NOT NULL,
    PRIMARY KEY (tool_name, semantic_hash),
    FOREIGN KEY (tool_name) REFERENCES agentdock_published_tool_contracts(tool_name) ON DELETE CASCADE
)`,
	`CREATE TABLE IF NOT EXISTS mcp_settings (
    singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    mcp_apps_enabled INTEGER NOT NULL DEFAULT 1 CHECK (mcp_apps_enabled IN (0, 1)),
    updated_at TEXT NOT NULL
)`,
	`CREATE INDEX IF NOT EXISTS idx_agentdock_pairing_codes_active
    ON agentdock_pairing_codes(code_hash, used_at, expires_at)`,
	`CREATE TABLE IF NOT EXISTS runtime_ai_settings (
    singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    embedding_enabled INTEGER NOT NULL CHECK (embedding_enabled IN (0, 1)),
    embedding_endpoint TEXT NOT NULL,
    embedding_model TEXT NOT NULL,
    embedding_timeout_seconds INTEGER NOT NULL CHECK (embedding_timeout_seconds BETWEEN 1 AND 300),
    stage3_enabled INTEGER NOT NULL CHECK (stage3_enabled IN (0, 1)),
    stage3_endpoint TEXT NOT NULL,
    stage3_model TEXT NOT NULL,
    stage3_timeout_seconds INTEGER NOT NULL CHECK (stage3_timeout_seconds BETWEEN 1 AND 300),
    stage3_interval_minutes INTEGER NOT NULL CHECK (stage3_interval_minutes BETWEEN 60 AND 10080),
    updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS runtime_ai_setting_secrets (
    name TEXT PRIMARY KEY CHECK (name IN ('embedding_api_key', 'stage3_api_key')),
    ciphertext BLOB NOT NULL,
    updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS runtime_workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    node_id TEXT NOT NULL,
    project_root TEXT NOT NULL,
    domain TEXT NOT NULL DEFAULT '',
    allowed_mcp_json TEXT NOT NULL DEFAULT '[]',
    context_roots_json TEXT NOT NULL DEFAULT '[]',
    design_authorities_json TEXT NOT NULL DEFAULT '[]',
    route_authority TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (node_id) REFERENCES agentdock_devices(id) ON DELETE CASCADE
)`,
	`CREATE INDEX IF NOT EXISTS idx_runtime_workspaces_node ON runtime_workspaces(node_id, name COLLATE NOCASE)`,
}

// EnsureSchema 把控制库升级到 CurrentSchemaVersion，失败时启动流程必须中止。
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	return migrateSchema(ctx, db, schemaMigrations)
}

// migrateSchema 按 PRAGMA user_version 把库推进到当前版本。
// migrations 参数化而不是直接读包级变量，是为了让测试能注入会失败的迁移
// 验证事务回滚；生产调用方固定传 schemaMigrations。
func migrateSchema(ctx context.Context, db *sql.DB, migrations []schemaMigration) error {
	version, err := readSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	// user_version=0 同时对应"全新空库"和"未版本化旧库"（引入版本化之前的库版本号都是 0），
	// 必须用核心表是否存在来区分：users 从最早的 0001_core.sql 起就是每个历史版本
	// 第一张创建的表，旧的非事务 EnsureSchema 也最先建它，任何真实 NexusDock 库都有 users。
	hasCoreTables, err := tableExists(ctx, db, "users")
	if err != nil {
		return err
	}
	if version == 0 && !hasCoreTables {
		return initializeSchema(ctx, db)
	}
	// 旧二进制遇到更高版本的库必须拒绝启动，避免把新结构当旧结构读写破坏数据。
	if version > CurrentSchemaVersion {
		return fmt.Errorf("control database schema version %d is newer than supported version %d", version, CurrentSchemaVersion)
	}
	for _, migration := range migrations {
		if migration.version <= version {
			continue
		}
		if err := applyMigration(ctx, db, migration); err != nil {
			return err
		}
	}
	return nil
}

// initializeSchema 在单个事务内把全新空库建到当前结构并写入版本号。
func initializeSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema initialization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range currentSchema {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize schema: %w", err)
		}
	}
	if err := writeSchemaVersion(ctx, tx, CurrentSchemaVersion); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema initialization: %w", err)
	}
	return nil
}

// applyMigration 在单个事务内执行一个迁移并推进版本号。
// SQLite 的 user_version 修改参与事务，任一语句失败时表结构变更和版本号一起回滚；
// journal_mode/synchronous 这类不能进事务的 PRAGMA 仍由 OpenSQLite 在连接层设置。
func applyMigration(ctx context.Context, db *sql.DB, migration schemaMigration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration v%d (%s): %w", migration.version, migration.name, err)
	}
	defer func() { _ = tx.Rollback() }()
	for i, statement := range migration.statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration v%d (%s) statement %d: %w", migration.version, migration.name, i+1, err)
		}
	}
	if err := writeSchemaVersion(ctx, tx, migration.version); err != nil {
		return fmt.Errorf("migration v%d (%s): %w", migration.version, migration.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration v%d (%s): %w", migration.version, migration.name, err)
	}
	return nil
}

// writeSchemaVersion 与建表语句同事务执行；PRAGMA 不支持参数绑定，
// 版本号是程序内部的 int 常量，直接拼接是安全的。
func writeSchemaVersion(ctx context.Context, tx *sql.Tx, version int) error {
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("write schema version %d: %w", version, err)
	}
	return nil
}

func readSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check table %s: %w", name, err)
	}
	return true, nil
}
