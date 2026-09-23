package netutil

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// ProxyConfig 统一代理配置
type ProxyConfig struct {
	Proxy   string // 显式指定的代理地址，如 "http://127.0.0.1:7890", "socks5://127.0.0.1:1080"
	NoProxy bool   // 显式禁用所有代理 (强制物理直连)
}

// ResolveProxyFunc 根据统一配置解析出 http.Transport 使用的 Proxy 函数
// 优先级法则：
// 1. NoProxy == true: 最高优先级，返回 nil (http.Transport.Proxy = nil，彻底物理直连)
// 2. Proxy != "": 用户显式指定代理，返回 http.ProxyURL(...)
// 3. 默认缺省探测：
//    a. 首选 Windows 注册表系统代理 (HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings)
//       若 ProxyEnable == 1 且配置了 ProxyServer，尊重 ProxyOverride 规则后路由
//    b. 次选平滑降级：若注册表未启用代理，遵循环境变量 (http.ProxyFromEnvironment，支持 HTTP_PROXY, HTTPS_PROXY, NO_PROXY)
//    c. 若均未配置，返回 nil (物理直连)
func ResolveProxyFunc(cfg ProxyConfig) (func(*http.Request) (*url.URL, error), error) {
	// 优先级 1: 显式禁止代理
	if cfg.NoProxy {
		return nil, nil
	}

	// 优先级 2: 用户显式传入指定代理
	if strings.TrimSpace(cfg.Proxy) != "" {
		parsedURL, err := ParseProxyURL(cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy url '%s': %w", cfg.Proxy, err)
		}
		return http.ProxyURL(parsedURL), nil
	}

	// 优先级 3: 默认缺省级联探测 (Windows 注册表优先 -> 环境变量降级 -> 直连)
	return defaultProxyResolver, nil
}

// ParseProxyURL 解析代理字符串，自动补全协议前缀并验证
func ParseProxyURL(proxyStr string) (*url.URL, error) {
	proxyStr = strings.TrimSpace(proxyStr)
	if proxyStr == "" {
		return nil, nil
	}

	lower := strings.ToLower(proxyStr)
	if !strings.HasPrefix(lower, "http://") &&
		!strings.HasPrefix(lower, "https://") &&
		!strings.HasPrefix(lower, "socks5://") &&
		!strings.HasPrefix(lower, "socks5h://") {
		proxyStr = "http://" + proxyStr
	}

	u, err := url.Parse(proxyStr)
	if err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, fmt.Errorf("missing host in proxy url '%s'", proxyStr)
	}
	return u, nil
}

var registryProxyReader = readWindowsRegistryProxy

// defaultProxyResolver 默认级联代理分流解析器
func defaultProxyResolver(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}

	host := req.URL.Hostname()
	if host == "" {
		host = req.URL.Host
	}
	// 本地环回地址 (localhost / 127.0.0.1 / [::1]) 始终物理直连，防止本地 Worker 探测发生黑洞死锁
	if isLoopback(host) {
		return nil, nil
	}

	// 1. 首选 Windows 注册表系统代理
	if regProxy, override := registryProxyReader(); regProxy != nil {
		// 校验是否命中 ProxyOverride 直连分流列表
		if isBypassedByOverride(req.URL, override) {
			return nil, nil
		}
		return regProxy, nil
	}

	// 2. 次选平滑回退至标准环境变量 (HTTP_PROXY / HTTPS_PROXY / NO_PROXY)
	return http.ProxyFromEnvironment(req)
}

// readWindowsRegistryProxy 读取 Windows 注册表 Internet Settings 代理设置
func readWindowsRegistryProxy() (*url.URL, string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil, ""
	}
	defer k.Close()

	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enable != 1 {
		return nil, ""
	}

	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return nil, ""
	}

	override, _, _ := k.GetStringValue("ProxyOverride")

	target := server
	// 支持多协议指定格式，如 "ftp=...;http=127.0.0.1:7890;https=127.0.0.1:7890"
	if strings.Contains(target, ";") {
		parts := strings.Split(target, ";")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(strings.ToLower(p), "https=") {
				target = strings.TrimPrefix(p, "https=")
				break
			} else if strings.HasPrefix(strings.ToLower(p), "http=") {
				target = strings.TrimPrefix(p, "http=")
			}
		}
	}

	parsed, err := ParseProxyURL(target)
	if err != nil {
		return nil, ""
	}
	return parsed, override
}

// isBypassedByOverride 判定目标 URL 是否匹配 Windows ProxyOverride 直连分流规则
func isBypassedByOverride(targetURL *url.URL, override string) bool {
	if targetURL == nil {
		return false
	}

	host := targetURL.Hostname()
	if host == "" {
		host = targetURL.Host
	}

	// 始终保护标准环回地址防死锁
	if isLoopback(host) {
		return true
	}

	if strings.TrimSpace(override) == "" {
		return false
	}

	// ProxyOverride 支持分号或空格分隔
	entries := strings.FieldsFunc(override, func(r rune) bool {
		return r == ';' || r == ' '
	})

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// <local>: 匹配无点主机名 (局域网单名机器，如 "desktop-4090") 及环回
		if strings.EqualFold(entry, "<local>") {
			if !strings.Contains(host, ".") || isLoopback(host) {
				return true
			}
			continue
		}

		// <loopback>: 明确匹配本机环回
		if strings.EqualFold(entry, "<loopback>") || strings.EqualFold(entry, "<-loopback>") {
			if isLoopback(host) {
				return true
			}
			continue
		}

		// 通配符匹配 (如 "192.168.*", "*.corp.internal", "10.*")
		if strings.Contains(entry, "*") {
			if matched, _ := filepath.Match(strings.ToLower(entry), strings.ToLower(host)); matched {
				return true
			}
			// 适配前缀/后缀通配，如 192.168.* 匹配 192.168.1.102
			patternPrefix := strings.TrimSuffix(strings.ToLower(entry), "*")
			if strings.HasSuffix(entry, "*") && strings.HasPrefix(strings.ToLower(host), patternPrefix) {
				return true
			}
			continue
		}

		// 域名后缀匹配 (如 ".local", "example.com")
		if strings.HasPrefix(entry, ".") && strings.HasSuffix(strings.ToLower(host), strings.ToLower(entry)) {
			return true
		}

		// 精确 IP 或主机名匹配
		if strings.EqualFold(host, entry) {
			return true
		}
	}

	return false
}

// isLoopback 判断是否为本地环回主机
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
