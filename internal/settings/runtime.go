package settings

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/stage3"
)

const (
	secretVersion  = byte(1)
	maxSecretBytes = 64 * 1024
)

var ErrUnavailable = errors.New("运行时 AI 设置存储不可用")

type ValidationError struct{ Message string }

func (e ValidationError) Error() string { return e.Message }

type SecretInput struct {
	Action string `json:"action"`
	Value  string `json:"value,omitempty"`
}

type EmbeddingInput struct {
	Enabled        bool        `json:"enabled"`
	Endpoint       string      `json:"endpoint"`
	Model          string      `json:"model"`
	TimeoutSeconds int         `json:"timeout_seconds"`
	APIKey         SecretInput `json:"api_key"`
}

type Stage3Input struct {
	Enabled         bool        `json:"enabled"`
	Endpoint        string      `json:"endpoint"`
	Model           string      `json:"model"`
	TimeoutSeconds  int         `json:"timeout_seconds"`
	IntervalMinutes int         `json:"interval_minutes"`
	APIKey          SecretInput `json:"api_key"`
}

type UpdateInput struct {
	Embedding EmbeddingInput `json:"embedding"`
	Stage3    Stage3Input    `json:"stage3"`
}

type EmbeddingView struct {
	Enabled          bool   `json:"enabled"`
	Endpoint         string `json:"endpoint"`
	Model            string `json:"model"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
	APIKeyConfigured bool   `json:"api_key_configured"`
}

type Stage3View struct {
	Enabled          bool   `json:"enabled"`
	Endpoint         string `json:"endpoint"`
	Model            string `json:"model"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
	IntervalMinutes  int    `json:"interval_minutes"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	Configured       bool   `json:"configured"`
}

type View struct {
	Embedding EmbeddingView `json:"embedding"`
	Stage3    Stage3View    `json:"stage3"`
	Persisted bool          `json:"persisted"`
	UpdatedAt string        `json:"updated_at,omitempty"`
}

type RuntimeAIConfig struct {
	EmbeddingEnabled  bool
	EmbeddingEndpoint string
	EmbeddingModel    string
	EmbeddingAPIKey   string
	EmbeddingTimeout  time.Duration
	Stage3Enabled     bool
	Stage3Endpoint    string
	Stage3Model       string
	Stage3APIKey      string
	Stage3Timeout     time.Duration
	Stage3Interval    time.Duration
}

const DefaultStage3Interval = 6 * time.Hour

func DefaultRuntimeAIConfig() RuntimeAIConfig {
	return RuntimeAIConfig{
		EmbeddingModel:   recall.DefaultEmbeddingModel,
		EmbeddingTimeout: recall.DefaultEmbeddingTimeout,
		Stage3Timeout:    stage3.DefaultRequestTimeout,
		Stage3Interval:   DefaultStage3Interval,
	}
}

type Store struct {
	db     *sql.DB
	cipher cipher.AEAD
	now    func() time.Time
}

func NewStore(db *sql.DB, dataDir string) (*Store, error) {
	if db == nil {
		return nil, ErrUnavailable
	}
	key, err := loadOrCreateKey(filepath.Join(dataDir, "secrets", "runtime-ai-settings.key"))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化运行时 AI 设置加密器: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化运行时 AI 设置 GCM: %w", err)
	}
	return &Store{db: db, cipher: aead, now: time.Now}, nil
}

func (s *Store) Load(ctx context.Context) (RuntimeAIConfig, View, error) {
	if s == nil || s.db == nil {
		return RuntimeAIConfig{}, View{}, ErrUnavailable
	}
	cfg := DefaultRuntimeAIConfig()
	var embeddingEnabled, stage3Enabled int
	var embeddingTimeout, stage3Timeout, interval int
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT embedding_enabled, embedding_endpoint, embedding_model, embedding_timeout_seconds,
		stage3_enabled, stage3_endpoint, stage3_model, stage3_timeout_seconds, stage3_interval_minutes, updated_at
		FROM runtime_ai_settings WHERE singleton_id = 1`).Scan(
		&embeddingEnabled, &cfg.EmbeddingEndpoint, &cfg.EmbeddingModel, &embeddingTimeout,
		&stage3Enabled, &cfg.Stage3Endpoint, &cfg.Stage3Model, &stage3Timeout, &interval, &updatedAt,
	)
	persisted := true
	if errors.Is(err, sql.ErrNoRows) {
		persisted = false
	} else if err != nil {
		return RuntimeAIConfig{}, View{}, fmt.Errorf("读取运行时 AI 设置: %w", err)
	} else {
		cfg.EmbeddingEnabled = embeddingEnabled == 1
		cfg.EmbeddingTimeout = time.Duration(embeddingTimeout) * time.Second
		cfg.Stage3Enabled = stage3Enabled == 1
		cfg.Stage3Timeout = time.Duration(stage3Timeout) * time.Second
		cfg.Stage3Interval = time.Duration(interval) * time.Minute
	}

	if value, found, err := s.loadSecret(ctx, "embedding_api_key"); err != nil {
		return RuntimeAIConfig{}, View{}, err
	} else if found {
		cfg.EmbeddingAPIKey = value
	}
	if value, found, err := s.loadSecret(ctx, "stage3_api_key"); err != nil {
		return RuntimeAIConfig{}, View{}, err
	} else if found {
		cfg.Stage3APIKey = value
	}
	return cfg, viewOf(cfg, persisted, updatedAt), nil
}

func (s *Store) Update(ctx context.Context, input UpdateInput) (RuntimeAIConfig, View, error) {
	if s == nil || s.db == nil {
		return RuntimeAIConfig{}, View{}, ErrUnavailable
	}
	normalized, err := normalizeInput(input)
	if err != nil {
		return RuntimeAIConfig{}, View{}, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RuntimeAIConfig{}, View{}, fmt.Errorf("开始更新运行时 AI 设置: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_ai_settings(
		singleton_id, embedding_enabled, embedding_endpoint, embedding_model, embedding_timeout_seconds,
		stage3_enabled, stage3_endpoint, stage3_model, stage3_timeout_seconds, stage3_interval_minutes, updated_at
	) VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(singleton_id) DO UPDATE SET
		embedding_enabled=excluded.embedding_enabled, embedding_endpoint=excluded.embedding_endpoint,
		embedding_model=excluded.embedding_model, embedding_timeout_seconds=excluded.embedding_timeout_seconds,
		stage3_enabled=excluded.stage3_enabled, stage3_endpoint=excluded.stage3_endpoint,
		stage3_model=excluded.stage3_model, stage3_timeout_seconds=excluded.stage3_timeout_seconds,
		stage3_interval_minutes=excluded.stage3_interval_minutes, updated_at=excluded.updated_at`,
		boolInt(normalized.Embedding.Enabled), normalized.Embedding.Endpoint, normalized.Embedding.Model, normalized.Embedding.TimeoutSeconds,
		boolInt(normalized.Stage3.Enabled), normalized.Stage3.Endpoint, normalized.Stage3.Model, normalized.Stage3.TimeoutSeconds,
		normalized.Stage3.IntervalMinutes, now)
	if err != nil {
		return RuntimeAIConfig{}, View{}, fmt.Errorf("保存运行时 AI 设置: %w", err)
	}
	for name, secret := range map[string]SecretInput{
		"embedding_api_key": normalized.Embedding.APIKey,
		"stage3_api_key":    normalized.Stage3.APIKey,
	} {
		if err := s.applySecret(ctx, tx, name, secret, now); err != nil {
			return RuntimeAIConfig{}, View{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RuntimeAIConfig{}, View{}, fmt.Errorf("提交运行时 AI 设置: %w", err)
	}
	return s.Load(ctx)
}

func normalizeInput(input UpdateInput) (UpdateInput, error) {
	input.Embedding.Endpoint = strings.TrimSpace(input.Embedding.Endpoint)
	input.Embedding.Model = strings.TrimSpace(input.Embedding.Model)
	input.Stage3.Endpoint = strings.TrimSpace(input.Stage3.Endpoint)
	input.Stage3.Model = strings.TrimSpace(input.Stage3.Model)
	if input.Embedding.Enabled {
		if err := validateEndpoint(input.Embedding.Endpoint, "向量服务地址"); err != nil {
			return UpdateInput{}, err
		}
		if input.Embedding.Model == "" {
			return UpdateInput{}, ValidationError{Message: "向量模型不能为空"}
		}
	}
	if input.Stage3.Enabled {
		if err := validateEndpoint(input.Stage3.Endpoint, "Stage 3 模型地址"); err != nil {
			return UpdateInput{}, err
		}
		if input.Stage3.Model == "" {
			return UpdateInput{}, ValidationError{Message: "Stage 3 模型不能为空"}
		}
	}
	if input.Embedding.TimeoutSeconds < 1 || input.Embedding.TimeoutSeconds > 300 {
		return UpdateInput{}, ValidationError{Message: "向量请求超时必须在 1 到 300 秒之间"}
	}
	if input.Stage3.TimeoutSeconds < 1 || input.Stage3.TimeoutSeconds > 300 {
		return UpdateInput{}, ValidationError{Message: "Stage 3 请求超时必须在 1 到 300 秒之间"}
	}
	if input.Stage3.IntervalMinutes < 60 || input.Stage3.IntervalMinutes > 10080 {
		return UpdateInput{}, ValidationError{Message: "Stage 3 执行间隔必须在 60 到 10080 分钟之间"}
	}
	for _, secret := range []SecretInput{input.Embedding.APIKey, input.Stage3.APIKey} {
		action := strings.ToLower(strings.TrimSpace(secret.Action))
		if action != "keep" && action != "replace" && action != "clear" {
			return UpdateInput{}, ValidationError{Message: "API Key 操作必须是 keep、replace 或 clear"}
		}
		if action == "replace" {
			value := strings.TrimSpace(secret.Value)
			if value == "" {
				return UpdateInput{}, ValidationError{Message: "替换 API Key 时新值不能为空"}
			}
			if len(value) > maxSecretBytes {
				return UpdateInput{}, ValidationError{Message: "API Key 过长"}
			}
		}
	}
	input.Embedding.APIKey.Action = strings.ToLower(strings.TrimSpace(input.Embedding.APIKey.Action))
	input.Stage3.APIKey.Action = strings.ToLower(strings.TrimSpace(input.Stage3.APIKey.Action))
	return input, nil
}

func validateEndpoint(value, label string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ValidationError{Message: label + "必须是有效的 HTTP 或 HTTPS URL，且不能包含用户凭据"}
	}
	return nil
}

func (s *Store) applySecret(ctx context.Context, tx *sql.Tx, name string, input SecretInput, updatedAt string) error {
	switch input.Action {
	case "keep":
		return nil
	case "clear", "replace":
		value := ""
		if input.Action == "replace" {
			value = strings.TrimSpace(input.Value)
		}
		sealed, err := s.seal(name, value)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO runtime_ai_setting_secrets(name, ciphertext, updated_at) VALUES(?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext, updated_at=excluded.updated_at`, name, sealed, updatedAt)
		if err != nil {
			return fmt.Errorf("保存运行时 AI 密钥: %w", err)
		}
		return nil
	default:
		return ValidationError{Message: "未知 API Key 操作"}
	}
}

func (s *Store) loadSecret(ctx context.Context, name string) (string, bool, error) {
	var sealed []byte
	if err := s.db.QueryRowContext(ctx, `SELECT ciphertext FROM runtime_ai_setting_secrets WHERE name = ?`, name).Scan(&sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("读取运行时 AI 密钥: %w", err)
	}
	plain, err := s.open(name, sealed)
	if err != nil {
		return "", false, err
	}
	return plain, true, nil
}

func (s *Store) seal(name, value string) ([]byte, error) {
	nonce := make([]byte, s.cipher.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("生成运行时 AI 密钥 nonce: %w", err)
	}
	sealed := s.cipher.Seal(nil, nonce, []byte(value), []byte(name))
	out := make([]byte, 1+len(nonce)+len(sealed))
	out[0] = secretVersion
	copy(out[1:], nonce)
	copy(out[1+len(nonce):], sealed)
	return out, nil
}

func (s *Store) open(name string, sealed []byte) (string, error) {
	if len(sealed) <= 1+s.cipher.NonceSize() || sealed[0] != secretVersion {
		return "", errors.New("运行时 AI 密钥格式无效")
	}
	nonceEnd := 1 + s.cipher.NonceSize()
	plain, err := s.cipher.Open(nil, sealed[1:nonceEnd], sealed[nonceEnd:], []byte(name))
	if err != nil {
		return "", errors.New("运行时 AI 密钥无法解密")
	}
	return string(plain), nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建运行时 AI 密钥目录: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("设置运行时 AI 密钥目录权限: %w", err)
	}
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != 32 {
			return nil, errors.New("运行时 AI 主密钥长度无效")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("设置运行时 AI 主密钥权限: %w", err)
		}
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取运行时 AI 主密钥: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("生成运行时 AI 主密钥: %w", err)
	}
	file, err := os.CreateTemp(dir, ".runtime-ai-key-*")
	if err != nil {
		return nil, fmt.Errorf("创建运行时 AI 临时主密钥: %w", err)
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(temp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadOrCreateKey(path)
		}
		return nil, fmt.Errorf("发布运行时 AI 主密钥: %w", err)
	}
	return key, nil
}

func viewOf(cfg RuntimeAIConfig, persisted bool, updatedAt string) View {
	return View{
		Embedding: EmbeddingView{
			Enabled: cfg.EmbeddingEnabled, Endpoint: cfg.EmbeddingEndpoint, Model: cfg.EmbeddingModel,
			TimeoutSeconds: int(cfg.EmbeddingTimeout / time.Second), APIKeyConfigured: strings.TrimSpace(cfg.EmbeddingAPIKey) != "",
		},
		Stage3: Stage3View{
			Enabled: cfg.Stage3Enabled, Endpoint: cfg.Stage3Endpoint, Model: cfg.Stage3Model,
			TimeoutSeconds: int(cfg.Stage3Timeout / time.Second), IntervalMinutes: int(cfg.Stage3Interval / time.Minute),
			APIKeyConfigured: strings.TrimSpace(cfg.Stage3APIKey) != "",
			Configured:       cfg.Stage3Enabled && strings.TrimSpace(cfg.Stage3Endpoint) != "" && strings.TrimSpace(cfg.Stage3Model) != "",
		},
		Persisted: persisted,
		UpdatedAt: updatedAt,
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
