//go:build windows

package logstream

import (
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const (
	cpACP   uint32 = 0
	cpOEMCP uint32 = 1
)

func decodeOEMBytes(b []byte) []byte {
	if len(b) == 0 {
		return b
	}

	// 优先尝试控制台 OEM 代码页 (中文 Windows 下为 CP936/GBK)
	n, err := windows.MultiByteToWideChar(cpOEMCP, 0, &b[0], int32(len(b)), nil, 0)
	if err == nil && n > 0 {
		wchars := make([]uint16, n)
		_, err = windows.MultiByteToWideChar(cpOEMCP, 0, &b[0], int32(len(b)), &wchars[0], n)
		if err == nil {
			return []byte(string(utf16.Decode(wchars)))
		}
	}

	// 尝试 ANSI 代码页 (CP_ACP)
	n, err = windows.MultiByteToWideChar(cpACP, 0, &b[0], int32(len(b)), nil, 0)
	if err == nil && n > 0 {
		wchars := make([]uint16, n)
		_, err = windows.MultiByteToWideChar(cpACP, 0, &b[0], int32(len(b)), &wchars[0], n)
		if err == nil {
			return []byte(string(utf16.Decode(wchars)))
		}
	}

	// 兜底安全替换非法字符
	return []byte(strings.ToValidUTF8(string(b), "\uFFFD"))
}
