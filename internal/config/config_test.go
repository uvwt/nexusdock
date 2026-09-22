package config

import (
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
)

// clearStartupEnv 清空可能影响默认值的启动变量，让"未设置 → 默认值"的语义可断言。
// 空串与未设置等价，这是 LoadFromEnv 对部署脚本中空变量的明确语义。
func clearStartupEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"NEXUS_HOST", "NEXUS_PORT", "NEXUS_PUBLIC_URL", "NEXUS_DATA_DIR", "RECALL_REPO_DIR", "NEXUS_TRUSTED_PROXIES", "NEXUS_LOG_LEVEL"} {
		t.Setenv(key, "")
	}
}

func TestLoadFromEnvUsesDefaultsWhenVariablesMissing(t *testing.T) {
	clearStartupEnv(t)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv with empty env failed: %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 18777 || cfg.PublicURL != "" {
		t.Fatalf("default endpoint = %s:%d public=%q", cfg.Host, cfg.Port, cfg.PublicURL)
	}
	if cfg.NexusDataDir != filepath.Join(".", "nexus-data") || cfg.RecallRepoDir != "recall" {
		t.Fatalf("default dirs = %q / %q", cfg.NexusDataDir, cfg.RecallRepoDir)
	}
	if cfg.LogLevelName != "info" {
		t.Fatalf("default log level = %q", cfg.LogLevelName)
	}
	// 默认可信代理必须覆盖本机回环（IPv4 与 IPv6），并以规范化前缀形式存在。
	want := []netip.Prefix{
		netip.PrefixFrom(netip.MustParseAddr("127.0.0.1"), 32),
		netip.MustParsePrefix("::1/128"),
	}
	if len(cfg.TrustedProxies) < len(want) || len(cfg.TrustedProxies) > len(want)+1 {
		t.Fatalf("default trusted proxies = %v", cfg.TrustedProxies)
	}
	for index, prefix := range want {
		if cfg.TrustedProxies[index] != prefix {
			t.Fatalf("default trusted proxies = %v, want %v", cfg.TrustedProxies, want)
		}
	}
	if len(cfg.TrustedProxies) == len(want)+1 {
		gateway, ok := discoverContainerGateway()
		if !ok {
			t.Fatalf("unexpected auto trusted proxy: %v", cfg.TrustedProxies)
		}
		if got := cfg.TrustedProxies[len(want)]; got != netip.PrefixFrom(gateway, 32) {
			t.Fatalf("auto trusted gateway = %v, want %v", got, gateway)
		}
	}
}

func TestLoadFromEnvAcceptsValidOverrides(t *testing.T) {
	clearStartupEnv(t)
	t.Setenv("NEXUS_HOST", "0.0.0.0")
	t.Setenv("NEXUS_PORT", "18000")
	t.Setenv("NEXUS_PUBLIC_URL", "https://nexus.example.com/")
	t.Setenv("NEXUS_LOG_LEVEL", "warn")
	t.Setenv("NEXUS_TRUSTED_PROXIES", "10.5.3.7/8, 192.168.227.0/24, fd00::1")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv with valid overrides failed: %v", err)
	}
	if cfg.Host != "0.0.0.0" || cfg.Port != 18000 || cfg.PublicURL != "https://nexus.example.com" {
		t.Fatalf("endpoint settings = %s:%d public=%q", cfg.Host, cfg.Port, cfg.PublicURL)
	}
	if cfg.LogLevelName != "warn" {
		t.Fatalf("log level = %q", cfg.LogLevelName)
	}
	// CIDR 掩码归一后必须落在网络地址上，单个 IP 规范成整段前缀。
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.168.227.0/24"),
		netip.PrefixFrom(netip.MustParseAddr("fd00::1"), 128),
	}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("trusted proxies = %v", cfg.TrustedProxies)
	}
	for index, prefix := range want {
		if cfg.TrustedProxies[index] != prefix {
			t.Fatalf("trusted proxies = %v, want %v", cfg.TrustedProxies, want)
		}
	}
}

func TestLoadFromEnvRejectsInvalidValuesWithVariableName(t *testing.T) {
	cases := []struct {
		name   string
		key    string
		value  string
		expect string // 错误信息里必须出现的非法值；列表变量以出错的条目为准。
	}{
		{name: "port is not a number", key: "NEXUS_PORT", value: "18abc", expect: "18abc"},
		{name: "port below range", key: "NEXUS_PORT", value: "0", expect: "0"},
		{name: "port above range", key: "NEXUS_PORT", value: "65536", expect: "65536"},
		{name: "unknown log level", key: "NEXUS_LOG_LEVEL", value: "verbose", expect: "verbose"},
		{name: "trusted proxy is not IP or CIDR", key: "NEXUS_TRUSTED_PROXIES", value: "10.0.0.0/8,not-an-ip", expect: "not-an-ip"},
		{name: "trusted proxy prefix out of family range", key: "NEXUS_TRUSTED_PROXIES", value: "10.0.0.0/33", expect: "10.0.0.0/33"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			clearStartupEnv(t)
			t.Setenv(tt.key, tt.value)

			_, err := LoadFromEnv()
			if err == nil {
				t.Fatalf("%s=%q was accepted", tt.key, tt.value)
			}
			// 错误必须能直接定位到变量与非法值，而不是泛化的加载失败。
			if !strings.Contains(err.Error(), tt.key) || !strings.Contains(err.Error(), tt.expect) {
				t.Fatalf("error should mention %s and %q, got: %v", tt.key, tt.expect, err)
			}
		})
	}
}

func TestParseLinuxDefaultGateway(t *testing.T) {
	routeTable := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t010012AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"eth0\t000012AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"

	got, ok := parseLinuxDefaultGateway(routeTable)
	if !ok {
		t.Fatal("Docker bridge default gateway was not detected")
	}
	if want := netip.MustParseAddr("172.18.0.1"); got != want {
		t.Fatalf("gateway = %v, want %v", got, want)
	}
}

func TestParseLinuxDefaultGatewayRejectsUnsafeOrInvalidRoutes(t *testing.T) {
	for name, routeTable := range map[string]string{
		"public gateway": "eth0\t00000000\t017100CB\t0003\t0\t0\t0\t00000000\n",
		"route is down":  "eth0\t00000000\t010012AC\t0002\t0\t0\t0\t00000000\n",
		"not default":    "eth0\t000012AC\t010012AC\t0003\t0\t0\t0\t0000FFFF\n",
		"malformed":      "eth0\t00000000\tnot-hex\t0003\n",
	} {
		t.Run(name, func(t *testing.T) {
			if gateway, ok := parseLinuxDefaultGateway(routeTable); ok {
				t.Fatalf("unexpected gateway %v", gateway)
			}
		})
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
