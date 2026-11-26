package settings

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/core"
)

func newRuntimeSettingsTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := core.OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "nexus.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	store, err := NewStore(db, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	keyInfo, err := os.Stat(filepath.Join(dataDir, "secrets", "runtime-ai-settings.key"))
	if err != nil || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("runtime settings key permission = %v, err=%v", keyInfo, err)
	}
	return store, db
}

func TestRuntimeSettingsUsesFixedDefaultsBeforeFirstSave(t *testing.T) {
	store, _ := newRuntimeSettingsTestStore(t)
	cfg, view, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.Persisted || cfg != DefaultRuntimeAIConfig() {
		t.Fatalf("unexpected default settings: cfg=%#v view=%#v", cfg, view)
	}
	if cfg.EmbeddingEnabled || view.Embedding.APIKeyConfigured {
		t.Fatalf("product settings should be disabled and secret-free before first save: %#v", view)
	}
}

func TestRuntimeSettingsEncryptsSecretsAndSupportsKeepReplaceClear(t *testing.T) {
	store, db := newRuntimeSettingsTestStore(t)
	input := UpdateInput{
		Embedding: EmbeddingInput{Enabled: true, Endpoint: "http://embedding.local/v1/embeddings", Model: "bge-m3", TimeoutSeconds: 20, APIKey: SecretInput{Action: "replace", Value: "embedding-secret-value"}},
	}
	cfg, view, err := store.Update(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmbeddingAPIKey != "embedding-secret-value" || !view.Persisted {
		t.Fatalf("updated settings mismatch: cfg=%#v view=%#v", cfg, view)
	}
	if !view.Embedding.APIKeyConfigured {
		t.Fatalf("configured flags missing: %#v", view)
	}

	rows, err := db.QueryContext(t.Context(), `SELECT ciphertext FROM runtime_ai_setting_secrets ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var ciphertext []byte
		if err := rows.Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		text := string(ciphertext)
		if strings.Contains(text, "embedding-secret-value") {
			t.Fatal("database contains plaintext runtime AI secret")
		}
	}

	input.Embedding.APIKey = SecretInput{Action: "keep"}
	cfg, view, err = store.Update(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmbeddingAPIKey != "embedding-secret-value" || !view.Embedding.APIKeyConfigured {
		t.Fatalf("keep semantics failed: embedding=%q view=%#v", cfg.EmbeddingAPIKey, view)
	}

	input.Embedding.APIKey = SecretInput{Action: "clear"}
	cfg, view, err = store.Update(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmbeddingAPIKey != "" || view.Embedding.APIKeyConfigured {
		t.Fatalf("clear semantics failed: embedding=%q view=%#v", cfg.EmbeddingAPIKey, view)
	}
}

func TestRuntimeSettingsRejectsInvalidInput(t *testing.T) {
	store, _ := newRuntimeSettingsTestStore(t)
	_, _, err := store.Update(context.Background(), UpdateInput{
		Embedding: EmbeddingInput{Enabled: true, Endpoint: "file:///tmp/embed", Model: "bge", TimeoutSeconds: 30, APIKey: SecretInput{Action: "keep"}},
	})
	var validation ValidationError
	if err == nil || !strings.Contains(err.Error(), "HTTP") || !strings.Contains(err.Error(), "向量") {
		t.Fatalf("invalid endpoint accepted: %v", err)
	}
	_ = validation
}

func TestRuntimeSettingsKeyRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.key")
	if err := os.WriteFile(outside, []byte(strings.Repeat("x", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "runtime-ai-settings.key")
	if err := os.Symlink(outside, keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateKey(keyPath); err == nil || !strings.Contains(err.Error(), "普通文件") {
		t.Fatalf("symlink key error = %v", err)
	}
}
