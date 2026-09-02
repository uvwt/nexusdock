package core

import (
	"context"
	"database/sql"
	"fmt"
)

// 当前控制面表。历史 Task/Run/设备表不再创建，启动时若还在就丢掉。
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
    subject_type TEXT NOT NULL CHECK (subject_type IN ('user', 'agent', 'device', 'system')),
    subject_id TEXT NOT NULL,
    token_kind TEXT NOT NULL CHECK (token_kind IN ('session', 'agent_token', 'device_token', 'system_token')),
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
}

var unusedTables = []string{
	"agentdock_node_secrets",
	"agentdock_nodes",
	"run_verifications",
	"run_evidence",
	"run_steps",
	"runs",
	"skills",
	"tasks",
	"agents",
	"device_commands_v1",
	"device_heartbeats",
	"device_enrollment_tokens",
	"device_records",
	"devices",
	"schema_migrations",
}

func EnsureSchema(ctx context.Context, db *sql.DB) error {
	for _, statement := range currentSchema {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure schema: %w", err)
		}
	}
	for _, name := range unusedTables {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+name); err != nil {
			return fmt.Errorf("drop unused table %s: %w", name, err)
		}
	}
	return nil
}
