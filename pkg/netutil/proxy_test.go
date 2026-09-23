package netutil

import (
	"net/http"
	"net/url"
	"testing"
)

func TestParseProxyURL(t *testing.T) {
	cases := []struct {
		input       string
		expected    string
		expectError bool
	}{
		{"", "", false},
		{"   ", "", false},
		{"http://127.0.0.1:7890", "http://127.0.0.1:7890", false},
		{"127.0.0.1:7890", "http://127.0.0.1:7890", false},
		{"socks5://10.0.0.1:1080", "socks5://10.0.0.1:1080", false},
		{"https://proxy.corp.com:8443", "https://proxy.corp.com:8443", false},
		{"http://", "", true},
	}

	for _, c := range cases {
		u, err := ParseProxyURL(c.input)
		if c.expectError {
			if err == nil {
				t.Fatalf("expected error for input '%s', got nil", c.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error for input '%s': %v", c.input, err)
		}
		if c.expected == "" {
			if u != nil {
				t.Fatalf("expected nil url for '%s', got %v", c.input, u)
			}
		} else {
			if u == nil || u.String() != c.expected {
				t.Fatalf("for input '%s', expected '%s', got '%v'", c.input, c.expected, u)
			}
		}
	}
}

func TestResolveProxyFunc_NoProxy(t *testing.T) {
	cfg := ProxyConfig{
		NoProxy: true,
		Proxy:   "http://127.0.0.1:7890", // 即使混入代理地址，NoProxy 也拥有最高优先级
	}
	fn, err := ResolveProxyFunc(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fn != nil {
		t.Fatalf("expected nil proxy func for NoProxy=true, got non-nil")
	}
}

func TestResolveProxyFunc_ExplicitProxy(t *testing.T) {
	cfg := ProxyConfig{
		Proxy: "http://127.0.0.1:8888",
	}
	fn, err := ResolveProxyFunc(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fn == nil {
		t.Fatal("expected non-nil proxy func for explicit Proxy")
	}

	req, _ := http.NewRequest(http.MethodGet, "http://target.node:19000", nil)
	u, err := fn(req)
	if err != nil {
		t.Fatalf("proxy func returned error: %v", err)
	}
	if u == nil || u.Host != "127.0.0.1:8888" {
		t.Fatalf("expected proxy host 127.0.0.1:8888, got %v", u)
	}
}

func TestResolveProxyFunc_Default_RegistryPriority(t *testing.T) {
	origReader := registryProxyReader
	defer func() { registryProxyReader = origReader }()

	mockURL, _ := url.Parse("http://127.0.0.1:7777")
	registryProxyReader = func() (*url.URL, string) {
		return mockURL, "<local>;192.168.*"
	}

	cfg := ProxyConfig{}
	fn, err := ResolveProxyFunc(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. 命中 ProxyOverride 直连分流
	reqDirect, _ := http.NewRequest(http.MethodGet, "http://192.168.1.10:19000", nil)
	uDirect, err := fn(reqDirect)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uDirect != nil {
		t.Fatalf("expected nil proxy for bypassed IP, got: %v", uDirect)
	}

	// 2. 外部公网地址走注册表代理
	reqProxy, _ := http.NewRequest(http.MethodGet, "http://public-node.com:19000", nil)
	uProxy, err := fn(reqProxy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uProxy == nil || uProxy.Host != "127.0.0.1:7777" {
		t.Fatalf("expected proxy 127.0.0.1:7777, got: %v", uProxy)
	}
}

func TestResolveProxyFunc_Default_EnvFallback(t *testing.T) {
	origReader := registryProxyReader
	defer func() { registryProxyReader = origReader }()

	// 模拟注册表未配置系统代理，平滑降级至环境变量
	registryProxyReader = func() (*url.URL, string) {
		return nil, ""
	}

	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9999")
	t.Setenv("NO_PROXY", "bypass.internal")

	cfg := ProxyConfig{}
	fn, err := ResolveProxyFunc(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fn == nil {
		t.Fatal("expected non-nil proxy func")
	}

	// 1. 命中 NO_PROXY 时应当直连
	reqBypass, _ := http.NewRequest(http.MethodGet, "http://bypass.internal/api", nil)
	uBypass, err := fn(reqBypass)
	if err != nil {
		t.Fatalf("proxy func error: %v", err)
	}
	if uBypass != nil {
		t.Fatalf("expected nil proxy for NO_PROXY, got %v", uBypass)
	}

	// 2. 未命中 NO_PROXY 时走环境变量代理
	reqProxied, _ := http.NewRequest(http.MethodGet, "http://example.com/api", nil)
	uProxied, err := fn(reqProxied)
	if err != nil {
		t.Fatalf("proxy func error: %v", err)
	}
	if uProxied == nil || uProxied.Host != "127.0.0.1:9999" {
		t.Fatalf("expected proxy 127.0.0.1:9999, got %v", uProxied)
	}
}

func TestIsBypassedByOverride(t *testing.T) {
	cases := []struct {
		url      string
		override string
		expected bool
	}{
		// 环回地址始终直连
		{"http://127.0.0.1:19000", "", true},
		{"http://localhost:19000", "", true},
		{"http://[::1]:19000", "", true},

		// <local> 匹配单名主机与环回
		{"http://desktop-4090:19000", "<local>", true},
		{"http://my-worker:19000", "<local>;192.168.*", true},
		{"http://worker.domain.com:19000", "<local>", false},

		// 通配符匹配
		{"http://192.168.1.50:19000", "192.168.*", true},
		{"http://10.10.1.2:19000", "192.168.*", false},
		{"http://node.corp.internal:19000", "*.corp.internal", true},
		{"http://node.other.com:19000", "*.corp.internal", false},

		// 精确匹配与域名后缀
		{"http://10.0.0.1:19000", "10.0.0.1;10.0.0.2", true},
		{"http://10.0.0.3:19000", "10.0.0.1;10.0.0.2", false},
		{"http://api.service.local:19000", ".local", true},
	}

	for _, c := range cases {
		parsedURL, err := url.Parse(c.url)
		if err != nil {
			t.Fatalf("invalid test url %s: %v", c.url, err)
		}
		res := isBypassedByOverride(parsedURL, c.override)
		if res != c.expected {
			t.Errorf("url: %s, override: '%s': expected %v, got %v", c.url, c.override, c.expected, res)
		}
	}
}
