package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host                  string
	Port                  int
	PublicURL             string
	NexusDataDir          string
	RecallRepoDir         string
	AuthToken             string
	RequireAuth           bool
	AuthAllowInsecureHTTP bool
	TrustedProxies        []string
	LogLevelName          string
	MCPAppsEnabled        bool
	EmbeddingEnabled      bool
	EmbeddingEndpoint     string
	EmbeddingModel        string
	EmbeddingAPIKey       string
	EmbeddingIndexFile    string
	EmbeddingTimeout      time.Duration
	EvolutionEnabled      bool
	ModelEndpoint         string
	ModelName             string
	ModelAPIKey           string
	ModelTimeout          time.Duration
	EvolutionInterval     time.Duration
}

func FromEnv() Config {
	recallRepoDir := getenv("RECALL_REPO_DIR", "recall")
	nexusDataDir := getenv("NEXUS_DATA_DIR", filepath.Join(".", "nexus-data"))
	defaultEmbeddingIndexFile := filepath.Join(recallRepoDir, ".recall", "embedding-index.json")
	modelEndpoint := strings.TrimSpace(os.Getenv("NEXUS_MODEL_ENDPOINT"))
	modelName := strings.TrimSpace(os.Getenv("NEXUS_MODEL_NAME"))
	cfg := Config{
		Host:                  getenv("NEXUS_HOST", "127.0.0.1"),
		Port:                  getenvInt("NEXUS_PORT", 18777),
		PublicURL:             strings.TrimRight(strings.TrimSpace(os.Getenv("NEXUS_PUBLIC_URL")), "/"),
		NexusDataDir:          nexusDataDir,
		RecallRepoDir:         recallRepoDir,
		AuthToken:             strings.TrimSpace(os.Getenv("NEXUS_AUTH_TOKEN")),
		RequireAuth:           getenvBool("NEXUS_REQUIRE_AUTH", false),
		AuthAllowInsecureHTTP: getenvBool("NEXUS_AUTH_ALLOW_INSECURE_HTTP", false),
		TrustedProxies:        splitCSV(getenv("NEXUS_TRUSTED_PROXIES", "127.0.0.1,::1")),
		LogLevelName:          getenv("NEXUS_LOG_LEVEL", "info"),
		MCPAppsEnabled:        getenvBool("NEXUS_MCP_APPS_ENABLED", true),
		EmbeddingEnabled:      getenvBool("RECALL_EMBEDDING_ENABLED", false),
		EmbeddingEndpoint:     strings.TrimSpace(os.Getenv("RECALL_EMBEDDING_ENDPOINT")),
		EmbeddingModel:        getenv("RECALL_EMBEDDING_MODEL", "BAAI/bge-m3"),
		EmbeddingAPIKey:       strings.TrimSpace(os.Getenv("RECALL_EMBEDDING_API_KEY")),
		EmbeddingIndexFile:    getenv("RECALL_EMBEDDING_INDEX_FILE", defaultEmbeddingIndexFile),
		EmbeddingTimeout:      time.Duration(getenvInt("RECALL_EMBEDDING_TIMEOUT_SECONDS", 30)) * time.Second,
		EvolutionEnabled:      getenvBool("NEXUS_EVOLUTION_ASSIST_ENABLED", modelEndpoint != "" && modelName != ""),
		ModelEndpoint:         modelEndpoint,
		ModelName:             modelName,
		ModelAPIKey:           strings.TrimSpace(os.Getenv("NEXUS_MODEL_API_KEY")),
		ModelTimeout:          time.Duration(getenvInt("NEXUS_MODEL_TIMEOUT_SECONDS", 60)) * time.Second,
		EvolutionInterval:     time.Duration(getenvInt("NEXUS_EVOLUTION_INTERVAL_MINUTES", 360)) * time.Minute,
	}
	return cfg
}

func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func (c Config) LogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(c.LogLevelName)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func (c Config) ValidateStartup() error {
	if c.PublicURL != "" {
		parsed, err := url.Parse(c.PublicURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return fmt.Errorf("NEXUS_PUBLIC_URL must be an HTTPS origin without path, query, fragment, or user info: %q", c.PublicURL)
		}
	}
	if !c.RequireAuth {
		return nil
	}
	if strings.TrimSpace(c.AuthToken) == "" {
		return errors.New("NEXUS_REQUIRE_AUTH=true requires NEXUS_AUTH_TOKEN for programmatic API access")
	}
	return nil
}
