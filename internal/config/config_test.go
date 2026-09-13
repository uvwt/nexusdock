package config

import (
	"testing"
)

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

	cfg := FromEnv()

	if cfg.Host != "0.0.0.0" || cfg.Port != 18000 || cfg.PublicURL != "https://nexus.example.com" {
		t.Fatalf("Nexus endpoint settings should be used, got %s:%d public=%q", cfg.Host, cfg.Port, cfg.PublicURL)
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
