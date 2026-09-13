package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Host           string
	Port           int
	PublicURL      string
	NexusDataDir   string
	RecallRepoDir  string
	TrustedProxies []string
	LogLevelName   string
}

func FromEnv() Config {
	recallRepoDir := getenv("RECALL_REPO_DIR", "recall")
	nexusDataDir := getenv("NEXUS_DATA_DIR", filepath.Join(".", "nexus-data"))
	cfg := Config{
		Host:           getenv("NEXUS_HOST", "127.0.0.1"),
		Port:           getenvInt("NEXUS_PORT", 18777),
		PublicURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("NEXUS_PUBLIC_URL")), "/"),
		NexusDataDir:   nexusDataDir,
		RecallRepoDir:  recallRepoDir,
		TrustedProxies: splitCSV(getenv("NEXUS_TRUSTED_PROXIES", "127.0.0.1,::1")),
		LogLevelName:   getenv("NEXUS_LOG_LEVEL", "info"),
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
	return nil
}
