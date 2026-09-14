package logstream

import (
	"testing"
	"unicode/utf8"
)

func TestEnsureUTF8(t *testing.T) {
	// 1. 纯 ASCII
	ascii := []byte("hello world 123")
	out := EnsureUTF8(ascii)
	if string(out) != "hello world 123" {
		t.Fatalf("unexpected output: %s", string(out))
	}
	if !utf8.Valid(out) {
		t.Fatalf("output must be valid utf-8")
	}

	// 2. 原生 UTF-8
	utf8Str := []byte("你好世界，任务执行完毕")
	out = EnsureUTF8(utf8Str)
	if string(out) != "你好世界，任务执行完毕" {
		t.Fatalf("unexpected output: %s", string(out))
	}
	if !utf8.Valid(out) {
		t.Fatalf("output must be valid utf-8")
	}

	// 3. 非法/非 UTF-8 序列 (如 GBK: 0xC4 0xE3 0xBA 0xC3 代表 "你好")
	gbk := []byte{0xC4, 0xE3, 0xBA, 0xC3}
	out = EnsureUTF8(gbk)
	if !utf8.Valid(out) {
		t.Fatalf("EnsureUTF8 output must strictly be valid utf8, got invalid bytes")
	}
	if string(out) != "你好" {
		t.Fatalf("expected Chinese '你好', got: %s", string(out))
	}

	// 4. 空切片
	if len(EnsureUTF8(nil)) != 0 {
		t.Fatalf("expected empty slice for nil")
	}
}
