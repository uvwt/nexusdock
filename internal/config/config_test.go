package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRequireAuthNeedsAPIToken(t *testing.T) {
	cfg := Config{RequireAuth: true}
	if err := cfg.ValidateStartup(); err == nil {
		t.Fatalf("expected missing bearer token to be rejected")
	}
	cfg.AuthToken = "token"
	if err := cfg.ValidateStartup(); err != nil {
		t.Fatalf("valid require auth config rejected: %v", err)
	}
}

func TestFromEnvMCPAppsEnabledDefaultsAndOverride(t *testing.T) {
	cfg := FromEnv()
	if !cfg.MCPAppsEnabled {
		t.Fatal("MCPAppsEnabled = false, want true by default")
	}

	t.Setenv("NEXUS_MCP_APPS_ENABLED", "false")
	cfg = FromEnv()
	if cfg.MCPAppsEnabled {
		t.Fatal("MCPAppsEnabled = true, want false from environment override")
	}
}

func TestFromEnvUsesNexusDataDirAndRecallRepoDir(t *testing.T) {
	t.Setenv("NEXUS_DATA_DIR", "/tmp/nexus-data")
	t.Setenv("RECALL_REPO_DIR", "/tmp/recall-repo")

	cfg := FromEnv()

	if cfg.NexusDataDir != "/tmp/nexus-data" {
		t.Fatalf("NexusDataDir = %q", cfg.NexusDataDir)
	}
	if cfg.RecallRepoDir != "/tmp/recall-repo" {
		t.Fatalf("RecallRepoDir = %q", cfg.RecallRepoDir)
	}
}

func TestFromEnvUsesNexusAndRecallVariables(t *testing.T) {
	t.Setenv("NEXUS_HOST", "0.0.0.0")
	t.Setenv("NEXUS_PORT", "18000")
	t.Setenv("NEXUS_PUBLIC_URL", "https://nexus.example.com/")
	t.Setenv("NEXUS_AUTH_TOKEN", "nexus-token")
	t.Setenv("NEXUS_REQUIRE_AUTH", "true")
	t.Setenv("RECALL_EMBEDDING_INDEX_FILE", "/tmp/recall-index.json")

	cfg := FromEnv()

	if cfg.Host != "0.0.0.0" || cfg.Port != 18000 || cfg.PublicURL != "https://nexus.example.com" {
		t.Fatalf("Nexus endpoint settings should be used, got %s:%d public=%q", cfg.Host, cfg.Port, cfg.PublicURL)
	}
	if cfg.AuthToken != "nexus-token" || !cfg.RequireAuth {
		t.Fatalf("Nexus auth settings should be used, token=%q require=%v", cfg.AuthToken, cfg.RequireAuth)
	}
	if cfg.EmbeddingIndexFile != "/tmp/recall-index.json" {
		t.Fatalf("Recall embedding index should be used, got %q", cfg.EmbeddingIndexFile)
	}
}

func TestValidateStartupRejectsInvalidPublicURL(t *testing.T) {
	for _, publicURL := range []string{
		"http://nexus.example.com",
		"https://nexus.example.com/path",
		"https://user@nexus.example.com",
		"https://nexus.example.com?query=1",
	} {
		cfg := Config{PublicURL: publicURL}
		if err := cfg.ValidateStartup(); err == nil {
			t.Fatalf("invalid NEXUS_PUBLIC_URL %q was accepted", publicURL)
		}
	}

	if err := (Config{PublicURL: "https://nexus.example.com"}).ValidateStartup(); err != nil {
		t.Fatalf("valid NEXUS_PUBLIC_URL rejected: %v", err)
	}
}

func TestFromEnvDefaultsRecallEmbeddingIndexUnderRecallDirectory(t *testing.T) {
	t.Setenv("RECALL_REPO_DIR", "/tmp/recall-repo")

	cfg := FromEnv()

	want := filepath.Join("/tmp/recall-repo", ".recall", "embedding-index.json")
	if cfg.EmbeddingIndexFile != want {
		t.Fatalf("EmbeddingIndexFile = %q, want %q", cfg.EmbeddingIndexFile, want)
	}
}

func TestFromEnvReadsStageThreeModelConfiguration(t *testing.T) {
	t.Setenv("NEXUS_MODEL_ENDPOINT", "https://model.example.com")
	t.Setenv("NEXUS_MODEL_NAME", "example-model")
	t.Setenv("NEXUS_MODEL_API_KEY", "model-secret")
	t.Setenv("NEXUS_MODEL_TIMEOUT_SECONDS", "45")
	t.Setenv("NEXUS_EVOLUTION_ASSIST_ENABLED", "true")
	t.Setenv("NEXUS_EVOLUTION_INTERVAL_MINUTES", "720")

	cfg := FromEnv()
	if cfg.ModelEndpoint != "https://model.example.com" || cfg.ModelName != "example-model" || cfg.ModelAPIKey != "model-secret" || !cfg.EvolutionEnabled {
		t.Fatalf("unexpected Stage 3 config: endpoint=%q model=%q api_key=%q", cfg.ModelEndpoint, cfg.ModelName, cfg.ModelAPIKey)
	}
	if cfg.ModelTimeout != 45*time.Second {
		t.Fatalf("ModelTimeout = %s", cfg.ModelTimeout)
	}
	if cfg.EvolutionInterval != 12*time.Hour {
		t.Fatalf("EvolutionInterval = %s", cfg.EvolutionInterval)
	}
}

func TestFromEnvReadsEmbeddingAPIKeyAndExplicitStage3Disable(t *testing.T) {
	t.Setenv("RECALL_EMBEDDING_API_KEY", "embedding-secret")
	t.Setenv("NEXUS_MODEL_ENDPOINT", "https://model.example.com")
	t.Setenv("NEXUS_MODEL_NAME", "example-model")
	t.Setenv("NEXUS_EVOLUTION_ASSIST_ENABLED", "false")

	cfg := FromEnv()
	if cfg.EmbeddingAPIKey != "embedding-secret" {
		t.Fatalf("EmbeddingAPIKey = %q", cfg.EmbeddingAPIKey)
	}
	if cfg.EvolutionEnabled {
		t.Fatal("explicit Stage 3 disable was ignored")
	}
}
