package core

// schemaMigration 是一个原子迁移：statements 与 user_version 推进在同一事务内完成，
// 任一语句失败整体回滚，不会留下半迁移状态。
type schemaMigration struct {
	version    int
	name       string
	statements []string
}

// schemaMigrations 按版本升序记录控制库 Schema 演进历史。
// 引入版本化之前（v0.1.0 到 287bffb）的库 user_version 全是 0，无法判断真实历史形态，
// 所以未版本化 baseline 从 v1 起从头重放：迁移全部使用 IF NOT EXISTS / DROP IF EXISTS，
// 对任意历史形态都幂等收敛到当前结构。新增 Schema 变更时在列表末尾追加新版本，
// 并同步递增 CurrentSchemaVersion。
var schemaMigrations = []schemaMigration{
	{
		version: 1,
		name:    "初始化控制面结构并清理历史遗留表",
		// v0.1.0 的真实初始结构，原样保留作为升级基准。
		// auth_tokens 的 CHECK 允许 'system'/'system_token' 是 v0.1.0 的历史形态：
		// 未版本化时期收窄 CHECK 时没有做表重建，升级库会一直保留更宽的 CHECK
		//（只是取值超集，代码不再写入这些值），因此不为收窄 CHECK 重建表。
		statements: []string{
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
			// 一次性清理更早时期（Task/Run、旧设备控制面和已移除的旧迁移系统）遗留的表。
			// 这组 DROP 原来是 EnsureSchema 每次启动都执行的 unusedTables，版本化后
			// 只在 v1 迁移里发生一次，启动路径不再有任何 DROP TABLE。
			`DROP TABLE IF EXISTS agentdock_node_secrets`,
			`DROP TABLE IF EXISTS agentdock_nodes`,
			`DROP TABLE IF EXISTS run_verifications`,
			`DROP TABLE IF EXISTS run_evidence`,
			`DROP TABLE IF EXISTS run_steps`,
			`DROP TABLE IF EXISTS runs`,
			`DROP TABLE IF EXISTS skills`,
			`DROP TABLE IF EXISTS tasks`,
			`DROP TABLE IF EXISTS agents`,
			`DROP TABLE IF EXISTS device_commands_v1`,
			`DROP TABLE IF EXISTS device_heartbeats`,
			`DROP TABLE IF EXISTS device_enrollment_tokens`,
			`DROP TABLE IF EXISTS device_records`,
			`DROP TABLE IF EXISTS devices`,
			`DROP TABLE IF EXISTS schema_migrations`,
		},
	},
	{
		version: 2,
		name:    "新增 agentdock_bridge_capabilities",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS agentdock_bridge_capabilities (
    node_id TEXT PRIMARY KEY,
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    updated_at TEXT NOT NULL,
    FOREIGN KEY (node_id) REFERENCES agentdock_devices(id) ON DELETE CASCADE
)`,
		},
	},
	{
		version: 3,
		name:    "新增 mcp_settings",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS mcp_settings (
    singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    mcp_apps_enabled INTEGER NOT NULL DEFAULT 1 CHECK (mcp_apps_enabled IN (0, 1)),
    updated_at TEXT NOT NULL
)`,
		},
	},
	{
		version: 4,
		name:    "新增 Runtime Workspace",
		statements: []string{
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
		},
	},
}
