package pathutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	// ErrEmptyPath 表示路径为空或全为空格
	ErrEmptyPath = errors.New("path cannot be empty")
)

// gitBashRegex 匹配形如 /c/... 或 /d/... 的 Git Bash / MSYS2 风格路径
var gitBashRegex = regexp.MustCompile(`^/([a-zA-Z])(/.*)?$`)

// ParseNodePath 解析命令中的目标路径，分离节点名与远端路径。
// 规则：
// 1. 若首个冒号位于索引 1 且前缀为单个英文字母（如 "C:/foo"），则判定为本地 Windows 盘符路径，节点名为空。
// 2. 否则首个冒号左侧为节点名，右侧为节点内部物理路径。
// 3. 若无冒号，则整串为本地路径，节点名为空。
func ParseNodePath(target string) (node string, path string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ""
	}

	colonIdx := strings.Index(target, ":")
	if colonIdx == -1 {
		// 无冒号，纯本地路径
		return "", target
	}

	// 保护 Windows 本地盘符：如 "D:/test" 或 "c:\test"
	if colonIdx == 1 && unicode.IsLetter(rune(target[0])) {
		return "", target
	}

	node = strings.TrimSpace(target[:colonIdx])
	path = strings.TrimSpace(target[colonIdx+1:])
	return node, path
}

// NormalizeLocalPath 将输入的路径规范化为标准 Windows 本地物理路径。
// 特性：
// 1. 自动转换 Git Bash / POSIX 风格："/d/projects" -> "D:\projects"
// 2. 统一正反斜杠为系统路径分隔符（Windows 下为反斜杠）
// 3. 校验空路径并剔除潜在注入风险
func NormalizeLocalPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrEmptyPath
	}

	// 转换正斜杠为一致的前缀探测
	slashPath := strings.ReplaceAll(trimmed, "\\", "/")

	// 匹配并转换 Git Bash 风格路径：/d/workspace -> D:/workspace
	if matches := gitBashRegex.FindStringSubmatch(slashPath); len(matches) > 1 {
		driveLetter := strings.ToUpper(matches[1])
		subPath := ""
		if len(matches) > 2 {
			subPath = matches[2]
		}
		trimmed = fmt.Sprintf("%s:%s", driveLetter, subPath)
	}

	// 使用系统标准库清洗路径（自动转换为 Windows 反斜杠）
	cleanPath := filepath.Clean(trimmed)

	// 针对 Windows 盘符根目录特殊处理：
	// Go 的 filepath.Clean("D:") 在 Windows 下会返回 "D:."（表示 D 盘当前工作目录）
	// 我们统一定义为绝对盘符根目录 "D:\"
	if len(cleanPath) == 3 && cleanPath[1] == ':' && cleanPath[2] == '.' && unicode.IsLetter(rune(cleanPath[0])) {
		cleanPath = string(unicode.ToUpper(rune(cleanPath[0]))) + `:\`
	} else if len(cleanPath) == 2 && cleanPath[1] == ':' && unicode.IsLetter(rune(cleanPath[0])) {
		cleanPath = string(unicode.ToUpper(rune(cleanPath[0]))) + `:\`
	}

	return cleanPath, nil
}

// JoinRemotePath 规范化拼接远端正斜杠路径 (对齐跨平台 POSIX / Windows 远端表示)
func JoinRemotePath(base, rel string) string {
	baseSlash := strings.TrimRight(filepath.ToSlash(base), "/")
	relSlash := strings.TrimLeft(filepath.ToSlash(rel), "/")
	if baseSlash == "" {
		if strings.HasPrefix(filepath.ToSlash(base), "/") {
			return "/" + relSlash
		}
		return relSlash
	}
	if relSlash == "" {
		return baseSlash
	}
	return baseSlash + "/" + relSlash
}

// SafeBaseName 安全提取路径的基础文件名，兼容 Windows 盘符与 Git Bash 根盘符
func SafeBaseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimRight(p, "/")
	// 识别 Git Bash 根盘符：形如 "/d" 或 "/c" 为盘符根目录，无子文件名
	if len(p) == 2 && p[0] == '/' && unicode.IsLetter(rune(p[1])) {
		return ""
	}
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		p = p[idx+1:]
	}
	if len(p) >= 2 && p[1] == ':' {
		p = p[2:]
	}
	base := strings.TrimLeft(p, "/")
	if base == "." || base == ".." || strings.TrimRight(base, ":") == "" {
		return ""
	}
	return base
}

// GetAvailableDrives 探测本地 Windows 可用盘符，返回如 ["C:/", "D:/"]
func GetAvailableDrives() []string {
	var roots []string
	for _, letter := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		path := string(letter) + ":\\"
		if _, err := os.Stat(path); err == nil {
			roots = append(roots, string(letter)+":/")
		}
	}
	if len(roots) == 0 {
		roots = []string{"C:/"}
	}
	return roots
}
