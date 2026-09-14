package logstream

import (
	"unicode/utf8"
)

// EnsureUTF8 确保字节流为严格合法的 UTF-8 编码。
// 在 Windows 物理宿主下，cmd.exe/ping 等系统原生工具默认输出 OEM 代码页 (如 CP936/GBK)。
// 本方法在探测到非 UTF-8 字节时进行本地代码页解码，确保推送到 WebSocket 客户端时符合 RFC 6455 规范。
func EnsureUTF8(b []byte) []byte {
	if len(b) == 0 || utf8.Valid(b) {
		return b
	}
	return decodeOEMBytes(b)
}
