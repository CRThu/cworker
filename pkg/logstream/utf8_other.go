//go:build !windows

package logstream

import "strings"

func decodeOEMBytes(b []byte) []byte {
	return []byte(strings.ToValidUTF8(string(b), "\uFFFD"))
}
