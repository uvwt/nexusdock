package agentdock

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
	"mime"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/core"
)

const pairingCodeTTL = 10 * time.Minute

var (
	ErrNodeNotFound       = errors.New("AgentDock 节点不存在")
	ErrNodeExists         = errors.New("AgentDock 设备已经配对")
	ErrNodeDisabled       = errors.New("AgentDock 节点已停用")
	ErrPairingCodeInvalid = errors.New("AgentDock 配对码无效或已过期")

	deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{7,127}$`)
)

type ValidationError struct{ Message string }

func (e ValidationError) Error() string { return e.Message }

func invalid(message string) error { return ValidationError{Message: message} }

type Node struct {
	ID               string     `json:"id"`
	DeviceID         string     `json:"device_id"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	Version          string     `json:"version,omitempty"`
	ProtocolVersion  string     `json:"protocol_version,omitempty"`
	OS               string     `json:"os,omitempty"`
	Arch             string     `json:"arch,omitempty"`
	Capabilities     []string   `json:"capabilities"`
	ToolContractHash string     `json:"tool_contract_hash,omitempty"`
	Online           bool       `json:"online"`
	LastSeenAt       *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type PairingCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PairInput struct {
	Code     string `json:"code"`
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
}

type UpdateInput struct {
	Name    *string `json:"name,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("AgentDock 节点数据库不能为空")
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) List(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, device_id, name, enabled, version, protocol_version,
		os, arch, capabilities_json, tool_contract_hash, last_seen_at, created_at, updated_at
		FROM agentdock_devices ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("列出 AgentDock 节点: %w", err)
	}
	defer rows.Close()

	nodes := make([]Node, 0)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 AgentDock 节点: %w", err)
	}
	return nodes, nil
}

func (s *Store) Get(ctx context.Context, id string) (Node, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Node{}, invalid("节点 ID 不能为空")
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, device_id, name, enabled, version, protocol_version,
		os, arch, capabilities_json, tool_contract_hash, last_seen_at, created_at, updated_at
		FROM agentdock_devices WHERE id = ?`, id)
	node, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNodeNotFound
	}
	return node, err
}

func (s *Store) CreatePairingCode(ctx context.Context) (PairingCode, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return PairingCode{}, fmt.Errorf("生成 AgentDock 配对码: %w", err)
	}
	code := "pair_" + base64.RawURLEncoding.EncodeToString(raw)
	id, err := core.NewID("pair")
	if err != nil {
		return PairingCode{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(pairingCodeTTL)
	_, err = s.db.ExecContext(ctx, `INSERT INTO agentdock_pairing_codes(id, code_hash, expires_at, created_at)
		VALUES(?, ?, ?, ?)`, id, hashSecret(code), expiresAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return PairingCode{}, fmt.Errorf("保存 AgentDock 配对码: %w", err)
	}
	return PairingCode{Code: code, ExpiresAt: expiresAt}, nil
}

func (s *Store) Pair(ctx context.Context, input PairInput) (Node, error) {
	code := strings.TrimSpace(input.Code)
	deviceID := strings.TrimSpace(input.DeviceID)
	name := strings.TrimSpace(input.Name)
	if code == "" {
		return Node{}, invalid("配对码不能为空")
	}
	if !deviceIDPattern.MatchString(deviceID) {
		return Node{}, invalid("设备 ID 格式无效")
	}
	if name == "" || len([]rune(name)) > 100 {
		return Node{}, invalid("节点名称必须为 1 到 100 个字符")
	}

	nodeID, err := core.NewID("node")
	if err != nil {
		return Node{}, err
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, fmt.Errorf("开始 AgentDock 配对事务: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE agentdock_pairing_codes SET used_at = ?
		WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now.Format(time.RFC3339Nano), hashSecret(code), now.Format(time.RFC3339Nano))
	if err != nil {
		return Node{}, fmt.Errorf("消费 AgentDock 配对码: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return Node{}, ErrPairingCodeInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agentdock_devices(
		id, device_id, name, enabled, created_at, updated_at
	) VALUES(?, ?, ?, 1, ?, ?)`, nodeID, deviceID, name, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		if core.IsSQLiteConflict(err) || strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Node{}, ErrNodeExists
		}
		return Node{}, fmt.Errorf("创建 AgentDock 节点: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Node{}, fmt.Errorf("提交 AgentDock 配对事务: %w", err)
	}
	return s.Get(ctx, nodeID)
}

func (s *Store) Update(ctx context.Context, id string, input UpdateInput) (Node, error) {
	if input.Name == nil && input.Enabled == nil {
		return Node{}, invalid("至少提交一个需要更新的节点字段")
	}
	node, err := s.Get(ctx, id)
	if err != nil {
		return Node{}, err
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 100 {
			return Node{}, invalid("节点名称必须为 1 到 100 个字符")
		}
		node.Name = name
	}
	if input.Enabled != nil {
		node.Enabled = *input.Enabled
	}
	node.UpdatedAt = s.now().UTC()
	enabled := 0
	if node.Enabled {
		enabled = 1
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agentdock_devices SET name = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		node.Name, enabled, node.UpdatedAt.Format(time.RFC3339Nano), node.ID)
	if err != nil {
		return Node{}, fmt.Errorf("更新 AgentDock 节点: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return Node{}, ErrNodeNotFound
	}
	return s.Get(ctx, node.ID)
}

func (s *Store) UpdateHello(ctx context.Context, nodeID string, hello Hello) (Node, error) {
	node, err := s.Get(ctx, nodeID)
	if err != nil {
		return Node{}, err
	}
	if !node.Enabled {
		return Node{}, ErrNodeDisabled
	}
	if strings.TrimSpace(hello.DeviceID) != node.DeviceID {
		return Node{}, invalid("设备身份与配对记录不一致")
	}
	capabilities, err := json.Marshal(normalizeCapabilities(hello.Capabilities))
	if err != nil {
		return Node{}, fmt.Errorf("编码 AgentDock 能力: %w", err)
	}
	bridgeCapabilities, err := json.Marshal(normalizeCapabilities(hello.BridgeCapabilities))
	if err != nil {
		return Node{}, fmt.Errorf("编码 AgentDock Bridge 能力: %w", err)
	}
	uiResources, err := validateUIResources(hello.UIResources)
	if err != nil {
		return Node{}, err
	}
	encodedUIResources, err := json.Marshal(uiResources)
	if err != nil {
		return Node{}, fmt.Errorf("编码 AgentDock UI resource 能力: %w", err)
	}
	now := s.now().UTC()
	descriptors, err := json.Marshal(hello.Tools)
	if err != nil {
		return Node{}, fmt.Errorf("编码 AgentDock 工具契约: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, fmt.Errorf("开始更新 AgentDock 握手事务: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE agentdock_devices SET version = ?, protocol_version = ?, os = ?, arch = ?,
		capabilities_json = ?, tool_contract_hash = ?, last_seen_at = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(hello.Version), strings.TrimSpace(hello.ProtocolVersion), strings.TrimSpace(hello.OS), strings.TrimSpace(hello.Arch),
		string(capabilities), strings.TrimSpace(hello.ToolContractHash), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), nodeID)
	if err != nil {
		return Node{}, fmt.Errorf("更新 AgentDock 节点握手信息: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agentdock_tool_contracts(node_id, descriptors_json, updated_at)
		VALUES(?, ?, ?) ON CONFLICT(node_id) DO UPDATE SET descriptors_json = excluded.descriptors_json, updated_at = excluded.updated_at`,
		nodeID, string(descriptors), now.Format(time.RFC3339Nano))
	if err != nil {
		return Node{}, fmt.Errorf("保存 AgentDock 工具契约: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agentdock_ui_resources(node_id, resources_json, updated_at)
		VALUES(?, ?, ?) ON CONFLICT(node_id) DO UPDATE SET resources_json = excluded.resources_json, updated_at = excluded.updated_at`,
		nodeID, string(encodedUIResources), now.Format(time.RFC3339Nano))
	if err != nil {
		return Node{}, fmt.Errorf("保存 AgentDock UI resource 能力: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agentdock_bridge_capabilities(node_id, capabilities_json, updated_at)
		VALUES(?, ?, ?) ON CONFLICT(node_id) DO UPDATE SET capabilities_json = excluded.capabilities_json, updated_at = excluded.updated_at`,
		nodeID, string(bridgeCapabilities), now.Format(time.RFC3339Nano))
	if err != nil {
		return Node{}, fmt.Errorf("保存 AgentDock Bridge 能力: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Node{}, fmt.Errorf("提交 AgentDock 握手事务: %w", err)
	}
	return s.Get(ctx, nodeID)
}

func (s *Store) ToolDescriptors(ctx context.Context, nodeID string) ([]ToolDescriptor, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT descriptors_json FROM agentdock_tool_contracts WHERE node_id = ?`, strings.TrimSpace(nodeID)).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return []ToolDescriptor{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 AgentDock 工具契约: %w", err)
	}
	var descriptors []ToolDescriptor
	if err := json.Unmarshal([]byte(encoded), &descriptors); err != nil {
		return nil, fmt.Errorf("解析 AgentDock 工具契约: %w", err)
	}
	return descriptors, nil
}

func (s *Store) UIResources(ctx context.Context, nodeID string) ([]UIResourceCapability, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT resources_json FROM agentdock_ui_resources WHERE node_id = ?`, strings.TrimSpace(nodeID)).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return []UIResourceCapability{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 AgentDock UI resource 能力: %w", err)
	}
	var resources []UIResourceCapability
	if err := json.Unmarshal([]byte(encoded), &resources); err != nil {
		return nil, fmt.Errorf("解析 AgentDock UI resource 能力: %w", err)
	}
	return resources, nil
}

func (s *Store) BridgeCapabilities(ctx context.Context, nodeID string) ([]string, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT capabilities_json FROM agentdock_bridge_capabilities WHERE node_id = ?`, strings.TrimSpace(nodeID)).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 AgentDock Bridge 能力: %w", err)
	}
	var capabilities []string
	if err := json.Unmarshal([]byte(encoded), &capabilities); err != nil {
		return nil, fmt.Errorf("解析 AgentDock Bridge 能力: %w", err)
	}
	return capabilities, nil
}

func (s *Store) Touch(ctx context.Context, nodeID string) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `UPDATE agentdock_devices SET last_seen_at = ?, updated_at = ? WHERE id = ? AND enabled = 1`, now, now, nodeID)
	if err != nil {
		return fmt.Errorf("更新 AgentDock 节点心跳: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrNodeNotFound
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始删除 AgentDock 节点事务: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM agentdock_tool_contracts WHERE node_id = ?`, id); err != nil {
		return fmt.Errorf("删除 AgentDock 工具契约: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agentdock_ui_resources WHERE node_id = ?`, id); err != nil {
		return fmt.Errorf("删除 AgentDock UI resource 能力: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agentdock_bridge_capabilities WHERE node_id = ?`, id); err != nil {
		return fmt.Errorf("删除 AgentDock Bridge 能力: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_tokens WHERE subject_type = 'device' AND subject_id = ?`, id); err != nil {
		return fmt.Errorf("删除 AgentDock Device Token: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM agentdock_devices WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除 AgentDock 节点: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrNodeNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交删除 AgentDock 节点事务: %w", err)
	}
	return nil
}

func scanNode(scanner interface{ Scan(...any) error }) (Node, error) {
	var node Node
	var enabled int
	var capabilitiesJSON, createdAt, updatedAt string
	var lastSeen sql.NullString
	if err := scanner.Scan(&node.ID, &node.DeviceID, &node.Name, &enabled, &node.Version, &node.ProtocolVersion,
		&node.OS, &node.Arch, &capabilitiesJSON, &node.ToolContractHash, &lastSeen, &createdAt, &updatedAt); err != nil {
		return Node{}, err
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &node.Capabilities); err != nil {
		return Node{}, fmt.Errorf("解析 AgentDock 节点能力: %w", err)
	}
	var err error
	if node.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return Node{}, fmt.Errorf("解析 AgentDock 节点创建时间: %w", err)
	}
	if node.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return Node{}, fmt.Errorf("解析 AgentDock 节点更新时间: %w", err)
	}
	if lastSeen.Valid {
		value, err := time.Parse(time.RFC3339Nano, lastSeen.String)
		if err != nil {
			return Node{}, fmt.Errorf("解析 AgentDock 节点在线时间: %w", err)
		}
		node.LastSeenAt = &value
	}
	node.Enabled = enabled == 1
	return node, nil
}

func validateUIResources(values []UIResourceCapability) ([]UIResourceCapability, error) {
	if values == nil {
		return nil, invalid("AgentDock Bridge v2 握手必须声明 ui_resources")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		uri := value.URI
		if strings.TrimSpace(uri) != uri {
			return nil, invalid("AgentDock UI resource URI 不能包含首尾空白: " + uri)
		}
		parsed, err := url.Parse(uri)
		if err != nil || parsed.Scheme != "ui" || parsed.Host == "" {
			return nil, invalid("AgentDock UI resource URI 无效: " + uri)
		}
		if value.Contract == "" || strings.TrimSpace(value.Contract) != value.Contract {
			return nil, invalid("AgentDock UI resource contract 无效: " + uri)
		}
		if value.MIMEType == "" || strings.TrimSpace(value.MIMEType) != value.MIMEType {
			return nil, invalid("AgentDock UI resource MIME type 无效: " + uri)
		}
		if _, _, err := mime.ParseMediaType(value.MIMEType); err != nil {
			return nil, invalid("AgentDock UI resource MIME type 无效: " + uri)
		}
		if _, duplicate := seen[uri]; duplicate {
			return nil, invalid("AgentDock UI resource 重复声明: " + uri)
		}
		seen[uri] = struct{}{}
	}
	// 握手层只验证结构，不按当前 Nexus renderer catalog 过滤或改写能力。
	// 这样新 URI / contract / MIME 可以被旧 Nexus 原样持久化，真正渲染时再做精确匹配。
	return append([]UIResourceCapability(nil), values...), nil
}

func normalizeCapabilities(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func hashSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
