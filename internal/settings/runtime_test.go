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
	if cfg.EmbeddingEnabled || cfg.Stage3Enabled || view.Embedding.APIKeyConfigured || view.Stage3.APIKeyConfigured {
		t.Fatalf("product settings should be disabled and secret-free before first save: %#v", view)
	}
}

func TestRuntimeSettingsEncryptsSecretsAndSupportsKeepReplaceClear(t *testing.T) {
	store, db := newRuntimeSettingsTestStore(t)
	input := UpdateInput{
		Embedding: EmbeddingInput{Enabled: true, Endpoint: "http://embedding.local/v1/embeddings", Model: "bge-m3", TimeoutSeconds: 20, APIKey: SecretInput{Action: "replace", Value: "embedding-secret-value"}},
		Stage3:    Stage3Input{Enabled: true, Endpoint: "https://model.local/v1/chat/completions", Model: "gpt-example", TimeoutSeconds: 40, IntervalMinutes: 360, APIKey: SecretInput{Action: "replace", Value: "model-secret-value"}},
	}
	cfg, view, err := store.Update(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmbeddingAPIKey != "embedding-secret-value" || cfg.Stage3APIKey != "model-secret-value" || !view.Persisted {
		t.Fatalf("updated settings mismatch: cfg=%#v view=%#v", cfg, view)
	}
	if !view.Embedding.APIKeyConfigured || !view.Stage3.APIKeyConfigured {
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
		if strings.Contains(text, "embedding-secret-value") || strings.Contains(text, "model-secret-value") {
			t.Fatal("database contains plaintext runtime AI secret")
		}
	}

	input.Embedding.APIKey = SecretInput{Action: "keep"}
	input.Stage3.APIKey = SecretInput{Action: "clear"}
	cfg, view, err = store.Update(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmbeddingAPIKey != "embedding-secret-value" || cfg.Stage3APIKey != "" {
		t.Fatalf("keep/clear semantics failed: embedding=%q model=%q", cfg.EmbeddingAPIKey, cfg.Stage3APIKey)
	}
	if !view.Embedding.APIKeyConfigured || view.Stage3.APIKeyConfigured {
		t.Fatalf("configured flags after clear mismatch: %#v", view)
	}
}

func TestRuntimeSettingsRejectsInvalidInput(t *testing.T) {
	store, _ := newRuntimeSettingsTestStore(t)
	_, _, err := store.Update(context.Background(), UpdateInput{
		Embedding: EmbeddingInput{Enabled: true, Endpoint: "file:///tmp/embed", Model: "bge", TimeoutSeconds: 30, APIKey: SecretInput{Action: "keep"}},
		Stage3:    Stage3Input{Enabled: false, TimeoutSeconds: 60, IntervalMinutes: 360, APIKey: SecretInput{Action: "keep"}},
	})
	var validation ValidationError
	if err == nil || !strings.Contains(err.Error(), "HTTP") || !strings.Contains(err.Error(), "向量") {
		t.Fatalf("invalid endpoint accepted: %v", err)
	}
	_ = validation
}
