package fsengine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cworker/pkg/protocol"
)

func TestParseLineRange(t *testing.T) {
	tests := []struct {
		input     string
		wantStart int
		wantEnd   int
		wantErr   bool
	}{
		{"", 0, 0, false},
		{"100:200", 100, 200, false},
		{"50:", 50, 0, false},
		{":30", 1, 30, false},
		{"42", 42, 42, false},
		{"0:10", 0, 0, true},
		{"10:5", 0, 0, true},
		{"abc:123", 0, 0, true},
		{"1:2:3", 0, 0, true},
	}

	for _, tt := range tests {
		s, e, err := ParseLineRange(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseLineRange(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && (s != tt.wantStart || e != tt.wantEnd) {
			t.Errorf("ParseLineRange(%q) = (%d, %d), want (%d, %d)", tt.input, s, e, tt.wantStart, tt.wantEnd)
		}
	}
}

func TestSliceFile_SmallFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "small.txt")

	var sb strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("failed to open file: %v", err)
	}
	defer f.Close()

	// 1. Head 5
	var outHead bytes.Buffer
	trunc, err := SliceFile(f, protocol.TextSliceOptions{Head: 5}, &outHead)
	if err != nil || trunc {
		t.Fatalf("Head failed: err=%v, trunc=%v", err, trunc)
	}
	headLines := strings.Split(strings.TrimSpace(outHead.String()), "\n")
	if len(headLines) != 5 || headLines[0] != "line 1" || headLines[4] != "line 5" {
		t.Fatalf("unexpected head output: %v", headLines)
	}

	// 2. Tail 5
	var outTail bytes.Buffer
	trunc, err = SliceFile(f, protocol.TextSliceOptions{Tail: 5}, &outTail)
	if err != nil || trunc {
		t.Fatalf("Tail failed: err=%v, trunc=%v", err, trunc)
	}
	tailLines := strings.Split(strings.TrimSpace(outTail.String()), "\n")
	if len(tailLines) != 5 || tailLines[0] != "line 46" || tailLines[4] != "line 50" {
		t.Fatalf("unexpected tail output: %v", tailLines)
	}

	// 3. LineRange 10:15
	var outRange bytes.Buffer
	trunc, err = SliceFile(f, protocol.TextSliceOptions{LineRange: "10:15"}, &outRange)
	if err != nil || trunc {
		t.Fatalf("Range failed: err=%v, trunc=%v", err, trunc)
	}
	rangeLines := strings.Split(strings.TrimSpace(outRange.String()), "\n")
	if len(rangeLines) != 6 || rangeLines[0] != "line 10" || rangeLines[5] != "line 15" {
		t.Fatalf("unexpected range output: %v", rangeLines)
	}

	// 4. Default small file: full content, truncated = false
	var outDefault bytes.Buffer
	trunc, err = SliceFile(f, protocol.TextSliceOptions{}, &outDefault)
	if err != nil || trunc {
		t.Fatalf("Default small file failed: err=%v, trunc=%v", err, trunc)
	}
	defLines := strings.Split(strings.TrimSpace(outDefault.String()), "\n")
	if len(defLines) != 50 {
		t.Fatalf("expected 50 lines for small file, got %d", len(defLines))
	}
}

func TestSliceFile_LargeFileOver2MB(t *testing.T) {
	// 创建一个超过 2.5MB 的大文件 (比如 30000 行，每行约 100 字节)
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "large.txt")

	fWrite, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create large file: %v", err)
	}

	totalLines := 30000
	padding := strings.Repeat("A", 80)
	for i := 1; i <= totalLines; i++ {
		fmt.Fprintf(fWrite, "line %06d %s\n", i, padding)
	}
	fWrite.Close()

	fi, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if fi.Size() < 2*1024*1024 {
		t.Fatalf("expected file to be > 2MB, got %d bytes", fi.Size())
	}

	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("failed to open file: %v", err)
	}
	defer f.Close()

	// 1. 测试未传参数触发 1MB 自动截断保护 (应截断为末尾 100 行，且 truncated == true)
	var outDefault bytes.Buffer
	trunc, err := SliceFile(f, protocol.TextSliceOptions{}, &outDefault)
	if err != nil {
		t.Fatalf("Default large file slice failed: %v", err)
	}
	if !trunc {
		t.Fatalf("expected truncated to be true for large file > 1MB")
	}
	lines := strings.Split(strings.TrimSpace(outDefault.String()), "\n")
	if len(lines) != 100 {
		t.Fatalf("expected exactly 100 lines for default truncation, got %d", len(lines))
	}
	expectedFirst := fmt.Sprintf("line %06d %s", totalLines-99, padding)
	if lines[0] != expectedFirst {
		t.Fatalf("expected line 0 to be %q, got %q", expectedFirst, lines[0])
	}

	// 2. 测试超越 2MB 边界的大量 Tail (例如 tail 25000 行，跨越整个大文件，验证绝不丢行)
	var outBigTail bytes.Buffer
	trunc, err = SliceFile(f, protocol.TextSliceOptions{Tail: 25000}, &outBigTail)
	if err != nil || trunc {
		t.Fatalf("Big Tail failed: err=%v, trunc=%v", err, trunc)
	}
	bigTailLines := strings.Split(strings.TrimSpace(outBigTail.String()), "\n")
	if len(bigTailLines) != 25000 {
		t.Fatalf("expected exactly 25000 lines from tail without 2MB truncation, got %d", len(bigTailLines))
	}
	expectedTailFirst := fmt.Sprintf("line %06d %s", totalLines-25000+1, padding)
	if bigTailLines[0] != expectedTailFirst {
		t.Fatalf("expected first line of big tail %q, got %q", expectedTailFirst, bigTailLines[0])
	}

	// 3. 测试显式 --all 全量流式输出
	var outAll bytes.Buffer
	trunc, err = SliceFile(f, protocol.TextSliceOptions{All: true}, &outAll)
	if err != nil || trunc {
		t.Fatalf("All failed: err=%v, trunc=%v", err, trunc)
	}
	if int64(outAll.Len()) != fi.Size() {
		t.Fatalf("expected all output bytes %d, got %d", fi.Size(), outAll.Len())
	}
}

func TestSliceFile_EdgeCases(t *testing.T) {
	tempDir := t.TempDir()

	// 1. nil 文件句柄
	_, err := SliceFile(nil, protocol.TextSliceOptions{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "file handle cannot be nil") {
		t.Fatalf("expected error for nil file handle, got %v", err)
	}

	// 2. 空文件 (0 字节)
	emptyPath := filepath.Join(tempDir, "empty.txt")
	emptyFile, err := os.Create(emptyPath)
	if err != nil {
		t.Fatalf("create empty file failed: %v", err)
	}
	defer emptyFile.Close()

	var emptyBuf bytes.Buffer
	trunc, err := SliceFile(emptyFile, protocol.TextSliceOptions{Tail: 10}, &emptyBuf)
	if err != nil || trunc || emptyBuf.Len() != 0 {
		t.Fatalf("expected empty file to return (false, nil), got trunc=%v, err=%v", trunc, err)
	}

	// 3. 已关闭文件 (Stat 错误)
	closedFile, err := os.Open(emptyPath)
	if err != nil {
		t.Fatalf("open empty file failed: %v", err)
	}
	_ = closedFile.Close()
	_, err = SliceFile(closedFile, protocol.TextSliceOptions{}, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("expected error on closed file, got nil")
	}

	// 4. 非法行号区间
	nonEmptyPath := filepath.Join(tempDir, "sample.txt")
	_ = os.WriteFile(nonEmptyPath, []byte("line 1\nline 2\nline 3\n"), 0644)
	neFile, _ := os.Open(nonEmptyPath)
	defer neFile.Close()

	_, err = SliceFile(neFile, protocol.TextSliceOptions{LineRange: "invalid:range:here"}, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("expected error on invalid range, got nil")
	}

	// 5. streamHead 早停 / Head 超过总行数 / Head <= 0
	var outHeadZero bytes.Buffer
	if err := streamHead(neFile, 0, &outHeadZero); err != nil || outHeadZero.Len() != 0 {
		t.Fatalf("expected Head 0 to return nil and empty, got %v", err)
	}

	var outHeadOver bytes.Buffer
	if err := streamHead(neFile, 100, &outHeadOver); err != nil {
		t.Fatalf("expected Head 100 on 3-line file to succeed, got %v", err)
	}
	if strings.Count(outHeadOver.String(), "\n") != 3 {
		t.Fatalf("expected 3 lines, got %d", strings.Count(outHeadOver.String(), "\n"))
	}

	// 6. streamRange 超出 EOF / 开区间
	var outRangeEOF bytes.Buffer
	if err := streamRange(neFile, 10, 20, &outRangeEOF); err != nil || outRangeEOF.Len() != 0 {
		t.Fatalf("expected streamRange beyond EOF to output nothing, got %v", err)
	}

	var outRangeOpen bytes.Buffer
	if err := streamRange(neFile, 2, 0, &outRangeOpen); err != nil {
		t.Fatalf("expected streamRange open-ended to succeed, got %v", err)
	}
	if !strings.Contains(outRangeOpen.String(), "line 2\nline 3\n") {
		t.Fatalf("unexpected open range output: %q", outRangeOpen.String())
	}

	// 7. streamTail n <= 0
	if err := streamTail(neFile, 0, 100, &bytes.Buffer{}); err != nil {
		t.Fatalf("expected streamTail n<=0 to succeed, got %v", err)
	}

	// 8. 无末尾换行符的文件
	noNewlinePath := filepath.Join(tempDir, "no_newline.txt")
	_ = os.WriteFile(noNewlinePath, []byte("line A\nline B"), 0644)
	nnFile, _ := os.Open(noNewlinePath)
	defer nnFile.Close()

	var outNN bytes.Buffer
	trunc, err = SliceFile(nnFile, protocol.TextSliceOptions{Tail: 1}, &outNN)
	if err != nil || trunc {
		t.Fatalf("expected tail 1 on no-newline file to succeed: %v", err)
	}
	if !strings.Contains(outNN.String(), "line B") {
		t.Fatalf("expected line B, got %q", outNN.String())
	}

	// 9. 大文件 (>32KB) 倒序扫描请求超过文件总行数 (测试回溯到 offset 0 边界)
	mediumPath := filepath.Join(tempDir, "medium.txt")
	mf, _ := os.Create(mediumPath)
	for i := 0; i < 600; i++ {
		_, _ = mf.WriteString(strings.Repeat("M", 80) + "\n")
	}
	_ = mf.Close()

	mOpen, _ := os.Open(mediumPath)
	defer mOpen.Close()
	mStat, _ := mOpen.Stat()

	var outMediumTail bytes.Buffer
	if err := streamTail(mOpen, 2000, mStat.Size(), &outMediumTail); err != nil {
		t.Fatalf("expected streamTail on large file with n > total to succeed, got %v", err)
	}
	if strings.Count(outMediumTail.String(), "\n") != 600 {
		t.Fatalf("expected 600 lines from tail over total, got %d", strings.Count(outMediumTail.String(), "\n"))
	}
}

