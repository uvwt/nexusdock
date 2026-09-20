package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Host          string
	Port          int
	PublicURL     string
	NexusDataDir  string
	RecallRepoDir string
	// TrustedProxies 在 LoadFromEnv 阶段一次解析并规范化：单个 IP 规范为整段前缀
	// （IPv4 /32、IPv6 /128），HTTP 层只做前缀包含判断，不再每请求重复解析字符串配置。
	TrustedProxies              []netip.Prefix
	LogLevelName                string
	ToolConcurrencyGlobal       int
	ToolConcurrencyPerNode      int
	ToolConcurrencyPerWorkspace int
	ToolQueueTimeoutSeconds     int
}

// LoadFromEnv 从环境变量加载启动配置并做 fail-fast 校验。
// 变量不存在或为空串（含纯空白）时使用默认值；变量存在但非法时直接返回错误，
// 错误信息包含变量名与非法值，避免配置错误被静默回退成难以排查的默认行为。
func LoadFromEnv() (Config, error) {
	cfg := Config{
		Host:          getenv("NEXUS_HOST", "127.0.0.1"),
		PublicURL:     strings.TrimRight(strings.TrimSpace(os.Getenv("NEXUS_PUBLIC_URL")), "/"),
		NexusDataDir:  getenv("NEXUS_DATA_DIR", filepath.Join(".", "nexus-data")),
		RecallRepoDir: getenv("RECALL_REPO_DIR", "recall"),
		LogLevelName:  getenv("NEXUS_LOG_LEVEL", "info"),
	}
	port, err := loadPort()
	if err != nil {
		return Config{}, err
	}
	cfg.Port = port
	if cfg.ToolConcurrencyGlobal, err = loadBoundedInt("NEXUS_TOOL_CONCURRENCY_GLOBAL", 16, 1, 256); err != nil {
		return Config{}, err
	}
	if cfg.ToolConcurrencyPerNode, err = loadBoundedInt("NEXUS_TOOL_CONCURRENCY_PER_NODE", 4, 1, 64); err != nil {
		return Config{}, err
	}
	if cfg.ToolConcurrencyPerWorkspace, err = loadBoundedInt("NEXUS_TOOL_CONCURRENCY_PER_WORKSPACE", 3, 1, 64); err != nil {
		return Config{}, err
	}
	if cfg.ToolQueueTimeoutSeconds, err = loadBoundedInt("NEXUS_TOOL_QUEUE_TIMEOUT_SECONDS", 30, 1, 300); err != nil {
		return Config{}, err
	}
	if err := validateLogLevel(cfg.LogLevelName); err != nil {
		return Config{}, err
	}
	trustedProxies, err := loadTrustedProxies()
	if err != nil {
		return Config{}, err
	}
	cfg.TrustedProxies = trustedProxies
	return cfg, nil
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

func loadBoundedInt(key string, fallback, minimum, maximum int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d, got %q", key, minimum, maximum, value)
	}
	return parsed, nil
}

func loadPort() (int, error) {
	value := strings.TrimSpace(os.Getenv("NEXUS_PORT"))
	if value == "" {
		return 18777, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("NEXUS_PORT must be an integer between 1 and 65535, got %q", value)
	}
	return port, nil
}

// validateLogLevel 校验 NEXUS_LOG_LEVEL，只接受 slog 可表达的级别；
// warn 与 warning 等价，保留两种写法是为了兼容已有部署配置。
func validateLogLevel(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug", "info", "warn", "warning", "error":
		return nil
	default:
		return fmt.Errorf("NEXUS_LOG_LEVEL must be one of debug, info, warn, warning, error, got %q", value)
	}
}

// ParseTrustedProxy 把单个可信代理条目解析成规范化的前缀：CIDR 做掩码归一，
// 单个 IP 规范为整段前缀，让 HTTP 层统一用 Prefix.Contains 判断。
func ParseTrustedProxy(entry string) (netip.Prefix, error) {
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, err
		}
		// IPv4-mapped 形式（如 ::ffff:192.168.227.0/120）统一还原成纯 IPv4 网段，
		// 否则与请求侧还原出的 IPv4 地址族不一致，会导致可信判断永不命中。
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return netip.Prefix{}, fmt.Errorf("IPv4-mapped prefix must have a prefix length of at least /96")
			}
			return netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96), nil
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, err
	}
	// 单个 IP 也可能出现 ::ffff:a.b.c.d 的映射写法，还原成纯 IPv4 再生成 /32 前缀。
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func loadTrustedProxies() ([]netip.Prefix, error) {
	entries := splitCSV(getenv("NEXUS_TRUSTED_PROXIES", "127.0.0.1,::1"))
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		prefix, err := ParseTrustedProxy(entry)
		if err != nil {
			return nil, fmt.Errorf("NEXUS_TRUSTED_PROXIES entry %q is not a valid IP or CIDR: %w", entry, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

// getenv 只把"不存在或为空串"视为未设置并回退默认值；
// 显式配置的空白值同样视为未设置，避免部署脚本里的空变量直接导致启动失败。
func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
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

// ValidateStartup 校验跨变量的语义约束（当前只有 PublicURL 的形状要求），
// 与 LoadFromEnv 的单变量语法校验分开，保证 admin 等本地命令不因对外 URL 配置错误而无法执行。
func (c Config) ValidateStartup() error {
	if c.PublicURL != "" {
		parsed, err := url.Parse(c.PublicURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return fmt.Errorf("NEXUS_PUBLIC_URL must be an HTTPS origin without path, query, fragment, or user info: %q", c.PublicURL)
		}
	}
	return nil
}
