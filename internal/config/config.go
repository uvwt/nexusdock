package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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
	TrustedProxies []netip.Prefix
	LogLevelName   string
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
	value := strings.TrimSpace(os.Getenv("NEXUS_TRUSTED_PROXIES"))
	if value != "" {
		return parseTrustedProxyEntries(splitCSV(value))
	}

	entries := []string{"127.0.0.1", "::1"}
	// 官方容器默认只把宿主机端口绑定到 loopback。宿主机 Nginx/Caddy 访问该端口时，
	// 容器内看到的来源通常是 bridge 默认网关；只自动追加这个单一 RFC1918 地址，
	// 不扩大到整个 Docker 私网。显式 NEXUS_TRUSTED_PROXIES 会完全覆盖这套自动默认值。
	if gateway, ok := discoverContainerGateway(); ok {
		entries = append(entries, gateway.String())
	}
	return parseTrustedProxyEntries(entries)
}

func parseTrustedProxyEntries(entries []string) ([]netip.Prefix, error) {
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

// discoverContainerGateway 只在 Linux 容器运行时读取 /proc/net/route。
// Docker/Podman 的默认 bridge 网关代表“宿主机这一跳”，适合宿主机反代到 loopback
// 发布端口的推荐场景；特殊网络拓扑应通过显式 NEXUS_TRUSTED_PROXIES 覆盖自动默认值。
func discoverContainerGateway() (netip.Addr, bool) {
	if runtime.GOOS != "linux" || !hasContainerRuntimeMarker() {
		return netip.Addr{}, false
	}
	routeTable, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return netip.Addr{}, false
	}
	return parseLinuxDefaultGateway(string(routeTable))
}

func hasContainerRuntimeMarker() bool {
	for _, path := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// parseLinuxDefaultGateway 解析 /proc/net/route 中 IPv4 默认路由的 little-endian gateway。
// 自动信任只接受 RFC1918 地址；其他容器网络仍可通过 NEXUS_TRUSTED_PROXIES 显式声明。
func parseLinuxDefaultGateway(routeTable string) (netip.Addr, bool) {
	for _, line := range strings.Split(routeTable, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] == "Iface" || fields[0] == "lo" {
			continue
		}
		if !strings.EqualFold(fields[1], "00000000") || strings.EqualFold(fields[2], "00000000") {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 64)
		if err != nil || flags&0x3 != 0x3 { // RTF_UP | RTF_GATEWAY
			continue
		}
		gateway, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			continue
		}
		addr := netip.AddrFrom4([4]byte{
			byte(gateway),
			byte(gateway >> 8),
			byte(gateway >> 16),
			byte(gateway >> 24),
		})
		if addr.IsPrivate() {
			return addr, true
		}
	}
	return netip.Addr{}, false
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
