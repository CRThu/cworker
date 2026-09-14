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
	cpGBK   uint32 = 936
)

const mbErrInvalidChars uint32 = 0x00000008

func tryDecodeCP(cp uint32, dwFlags uint32, b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	n, err := windows.MultiByteToWideChar(cp, dwFlags, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return nil
	}
	wchars := make([]uint16, n)
	_, err = windows.MultiByteToWideChar(cp, dwFlags, &b[0], int32(len(b)), &wchars[0], n)
	if err != nil {
		return nil
	}
	return []byte(string(utf16.Decode(wchars)))
}

func decodeOEMBytes(b []byte) []byte {
	if len(b) == 0 {
		return b
	}

	// 1. 优先尝试 CP936 (GBK/GB18030)，使用 MB_ERR_INVALID_CHARS 严格校验
	// Windows 物理机上 cmd/ping 等中文输出绝大多数均为 CP936。
	// 无论宿主机系统区域是中文还是英文 (如 CI 英文环境)，均能准确解码中文而不退化为乱码。
	if res := tryDecodeCP(cpGBK, mbErrInvalidChars, b); res != nil {
		return res
	}

	// 2. 尝试系统当前 OEM 代码页 (针对特定区域语言如 CP866/CP932/CP850 等)
	if res := tryDecodeCP(cpOEMCP, 0, b); res != nil {
		return res
	}

	// 3. 尝试 ANSI 代码页 (CP_ACP)
	if res := tryDecodeCP(cpACP, 0, b); res != nil {
		return res
	}

	// 4. 兜底安全替换非法字符
	return []byte(strings.ToValidUTF8(string(b), "\uFFFD"))
}
