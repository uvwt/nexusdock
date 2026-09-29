package httpx

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/auth"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/core"
)

// trustedPrefixes 在测试中按与 LoadFromEnv 相同的规则解析可信代理配置，
// 保证测试构造的 Config 与生产启动路径的规范化形式一致。
func trustedPrefixes(t *testing.T, entries ...string) []netip.Prefix {
	t.Helper()
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		prefix, err := config.ParseTrustedProxy(entry)
		if err != nil {
			t.Fatalf("parse trusted proxy %q: %v", entry, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

func TestSafeReturnToRejectsExternalAndControlValues(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty", value: "", want: "/ui/"},
		{name: "relative path", value: "/ui/#recall", want: "/ui/#recall"},
		{name: "absolute URL", value: "https://evil.example/ui/", want: "/ui/"},
		{name: "protocol relative", value: "//evil.example/ui/", want: "/ui/"},
		{name: "newline injection", value: "/ui/\r\nSet-Cookie: bad=1", want: "/ui/"},
		{name: "missing slash", value: "ui/#recall", want: "/ui/"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeReturnTo(tt.value); got != tt.want {
				t.Fatalf("safeReturnTo(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestSameOriginHonorsTrustedProxyHeadersOnly(t *testing.T) {
	access := newAccessControl(trustedPrefixes(t, "10.0.0.0/8"), nil)

	directTLS := httptest.NewRequest(http.MethodPost, "https://nexus.example/v1/auth/login", nil)
	directTLS.Header.Set("Origin", "https://nexus.example")
	if !access.sameOrigin(directTLS) {
		t.Fatalf("direct TLS same-origin request was rejected")
	}

	trustedProxy := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/auth/login", nil)
	trustedProxy.Host = "127.0.0.1"
	trustedProxy.RemoteAddr = "10.1.2.3:4567"
	trustedProxy.Header.Set("Origin", "https://nexus.example")
	trustedProxy.Header.Set("X-Forwarded-Proto", "https")
	trustedProxy.Header.Set("X-Forwarded-Host", "nexus.example")
	if !access.sameOrigin(trustedProxy) {
		t.Fatalf("trusted reverse proxy same-origin request was rejected")
	}

	untrustedProxy := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/auth/login", nil)
	untrustedProxy.RemoteAddr = "203.0.113.8:4567"
	untrustedProxy.Header.Set("Origin", "https://nexus.example")
	untrustedProxy.Header.Set("X-Forwarded-Proto", "https")
	untrustedProxy.Header.Set("X-Forwarded-Host", "nexus.example")
	if access.sameOrigin(untrustedProxy) {
		t.Fatalf("untrusted reverse proxy headers were accepted as same-origin")
	}
}

func TestSecureRequestRequiresTLSOrTrustedForwardedProto(t *testing.T) {
	access := newAccessControl(trustedPrefixes(t, "10.0.0.0/8"), nil)

	directTLS := httptest.NewRequest(http.MethodGet, "https://nexus.example/", nil)
	directTLS.TLS = &tls.ConnectionState{}
	if !access.secureRequest(directTLS) {
		t.Fatalf("TLS request was not treated as secure")
	}

	trustedProxy := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	trustedProxy.RemoteAddr = "10.1.2.3:4567"
	trustedProxy.Header.Set("X-Forwarded-Proto", "https")
	if !access.secureRequest(trustedProxy) {
		t.Fatalf("trusted proxy https request was not treated as secure")
	}

	untrustedProxy := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	untrustedProxy.RemoteAddr = "203.0.113.8:4567"
	untrustedProxy.Header.Set("X-Forwarded-Proto", "https")
	if access.secureRequest(untrustedProxy) {
		t.Fatalf("untrusted forwarded proto marked request secure")
	}
}

func TestLoginTransportAllowsHTTPSOrDirectLoopbackOnly(t *testing.T) {
	access := newAccessControl(trustedPrefixes(t, "10.0.0.0/8", "127.0.0.1", "::1"), nil)

	directTLS := httptest.NewRequest(http.MethodPost, "https://nexus.example/v1/auth/login", nil)
	if !access.loginTransportAllowed(directTLS) {
		t.Fatal("direct HTTPS login was rejected")
	}

	trustedHTTPSProxy := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/auth/login", nil)
	trustedHTTPSProxy.RemoteAddr = "10.1.2.3:4567"
	trustedHTTPSProxy.Header.Set("X-Forwarded-Proto", "https")
	if !access.loginTransportAllowed(trustedHTTPSProxy) {
		t.Fatal("trusted HTTPS reverse proxy login was rejected")
	}

	directLoopback := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18777/v1/auth/login", nil)
	directLoopback.RemoteAddr = "127.0.0.1:4567"
	if !access.loginTransportAllowed(directLoopback) {
		t.Fatal("direct loopback HTTP login was rejected")
	}

	directIPv6Loopback := httptest.NewRequest(http.MethodPost, "http://localhost:18777/v1/auth/login", nil)
	directIPv6Loopback.RemoteAddr = "[::1]:4567"
	if !access.loginTransportAllowed(directIPv6Loopback) {
		t.Fatal("direct IPv6 loopback HTTP login was rejected")
	}

	remoteWithLoopbackHost := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18777/v1/auth/login", nil)
	remoteWithLoopbackHost.RemoteAddr = "203.0.113.8:4567"
	if access.loginTransportAllowed(remoteWithLoopbackHost) {
		t.Fatal("remote HTTP client bypassed the HTTPS requirement with a loopback Host")
	}

	proxiedHTTP := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18777/v1/auth/login", nil)
	proxiedHTTP.RemoteAddr = "127.0.0.1:4567"
	proxiedHTTP.Header.Set("X-Forwarded-For", "203.0.113.8")
	proxiedHTTP.Header.Set("X-Forwarded-Host", "nexus.example")
	proxiedHTTP.Header.Set("X-Forwarded-Proto", "http")
	if access.loginTransportAllowed(proxiedHTTP) {
		t.Fatal("proxied HTTP login was mistaken for a direct loopback request")
	}

	localClientToLANHost := httptest.NewRequest(http.MethodPost, "http://192.168.1.10:18777/v1/auth/login", nil)
	localClientToLANHost.RemoteAddr = "127.0.0.1:4567"
	if access.loginTransportAllowed(localClientToLANHost) {
		t.Fatal("HTTP login to a non-loopback Host was allowed")
	}
}

func TestAPIAccessDoesNotTrustClientControlledHost(t *testing.T) {
	access := newAccessControl(nil, slog.Default())
	next := access.withAPIAccess(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.RemoteAddr = "203.0.113.8:4567"
	req.Host = "localhost"
	res := httptest.NewRecorder()
	next.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("external request with localhost Host bypassed API access: status=%d", res.Code)
	}
}

func TestConfiguredWebAuthenticationDisablesLoopbackBypass(t *testing.T) {
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	access := newAccessControl(nil, slog.Default())
	access.auth = auth.NewService(db)
	next := access.withAPIAccess(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.RemoteAddr = "127.0.0.1:4567"
	res := httptest.NewRecorder()
	next.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("loopback request bypassed configured web authentication: status=%d", res.Code)
	}
}

func TestUnconfiguredLocalAPIStillRequiresLoopbackRemoteAddress(t *testing.T) {
	access := newAccessControl(nil, slog.Default())
	next := access.withAPIAccess(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.RemoteAddr = "127.0.0.1:4567"
	req.Host = "nexus.example"
	res := httptest.NewRecorder()
	next.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("direct loopback API request was rejected: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestSameOriginRejectsSpoofedLeadingForwardedValues(t *testing.T) {
	access := newAccessControl(trustedPrefixes(t, "10.0.0.0/8"), nil)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/auth/login", nil)
	req.RemoteAddr = "10.1.2.3:4567"
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("X-Forwarded-Proto", "http, https")
	req.Header.Set("X-Forwarded-Host", "evil.example, nexus.example")
	if access.sameOrigin(req) {
		t.Fatal("client-controlled leading forwarded values bypassed same-origin validation")
	}

	req.Header.Set("Origin", "https://nexus.example")
	if !access.sameOrigin(req) {
		t.Fatal("nearest trusted forwarded host and proto were not honored")
	}
}

func TestClientIPPrefixUsesNearestUntrustedForwardedHop(t *testing.T) {
	access := newAccessControl(trustedPrefixes(t, "10.0.0.0/8", "192.168.0.0/16"), nil)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/auth/login", nil)
	req.RemoteAddr = "10.1.2.3:4567"
	req.Header.Set("X-Forwarded-For", "198.51.100.99, 203.0.113.72, 192.168.1.10")
	if got := access.clientIPPrefix(req); got != "203.0.113.0/24" {
		t.Fatalf("client IP prefix=%q want=%q", got, "203.0.113.0/24")
	}
}

func TestLoginLogsInternalSessionFailureWithoutExposingIt(t *testing.T) {
	db, err := core.OpenSQLite(t.Context(), ":memory:", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := core.EnsureSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db)
	if err := authService.InitializeAdmin(t.Context(), "owner", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_session_insert BEFORE INSERT ON user_sessions BEGIN SELECT RAISE(ABORT, 'test session insert failure'); END`); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	access := newAccessControl(nil, slog.New(slog.NewTextHandler(&logs, nil)))
	access.auth = authService
	req := httptest.NewRequest(http.MethodPost, "https://nexus.example/v1/auth/login", strings.NewReader(`{"username":"owner","password":"correct horse battery staple"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://nexus.example")
	res := httptest.NewRecorder()

	access.login(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("login status=%d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(logs.String(), "test session insert failure") {
		t.Fatalf("internal login failure was not logged: %s", logs.String())
	}
	if strings.Contains(res.Body.String(), "test session insert failure") || strings.Contains(res.Body.String(), "correct horse battery staple") {
		t.Fatalf("internal login failure leaked to response: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "LOGIN_FAILED") {
		t.Fatalf("generic login error missing: %s", res.Body.String())
	}
}
