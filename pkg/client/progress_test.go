package client

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProgressTracker_TTY_Rendering(t *testing.T) {
	tracker := NewProgressTracker(10, 1024*1024*10) // 10MB, 10 files
	tracker.SetTTY(true)

	if tracker.TotalFiles() != 10 {
		t.Fatalf("expected 10 total files, got %d", tracker.TotalFiles())
	}
	if tracker.TotalBytes() != 1024*1024*10 {
		t.Fatalf("expected 10MB total bytes, got %d", tracker.TotalBytes())
	}

	// 模拟传输 5MB 和 5 个文件
	tracker.AddBytes(1024 * 1024 * 5)
	tracker.AddFile()
	tracker.AddFile()
	tracker.AddFile()
	tracker.AddFile()
	tracker.AddFile()

	if tracker.TransferredBytes() != 1024*1024*5 {
		t.Fatalf("expected 5MB transferred, got %d", tracker.TransferredBytes())
	}
	if tracker.CompletedFiles() != 5 {
		t.Fatalf("expected 5 completed files, got %d", tracker.CompletedFiles())
	}

	// 强制渲染一次
	tracker.maybeRender(true)

	// 调整预期大小并完成
	tracker.SetTotalBytes(1024 * 1024 * 5)
	tracker.Finish()
}

func TestProgressTracker_NonTTY_Silence(t *testing.T) {
	tracker := NewProgressTracker(5, 5000)
	tracker.SetTTY(false)

	tracker.AddBytes(1000)
	tracker.AddFile()
	tracker.maybeRender(false)
	tracker.maybeRender(true) // non-TTY 下强制渲染也保持静默

	tracker.Finish() // 仅在 finish 时输出单行摘要
}

func TestProgressTracker_ZeroValues(t *testing.T) {
	var nilTracker *ProgressTracker
	nilTracker.AddBytes(100)
	nilTracker.AddFile()
	nilTracker.Finish()
	nilTracker.SetTTY(true)
	nilTracker.SetTotalBytes(100)
	if nilTracker.TotalBytes() != 0 || nilTracker.TransferredBytes() != 0 || nilTracker.CompletedFiles() != 0 || nilTracker.TotalFiles() != 0 {
		t.Fatal("nil tracker should return 0 for all getters")
	}

	// 空文件与空字节
	zeroTracker := NewProgressTracker(0, 0)
	zeroTracker.SetTTY(true)
	zeroTracker.maybeRender(true)
	zeroTracker.Finish()
}

func TestProgressTracker_Concurrency_Race(t *testing.T) {
	tracker := NewProgressTracker(100, 1000000)
	tracker.SetTTY(false)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				tracker.AddBytes(100)
				tracker.AddFile()
				time.Sleep(100 * time.Microsecond)
			}
		}()
	}
	wg.Wait()

	if tracker.CompletedFiles() != 1000 {
		t.Fatalf("expected 1000 completed files, got %d", tracker.CompletedFiles())
	}
	if tracker.TransferredBytes() != 100000 {
		t.Fatalf("expected 100000 transferred bytes, got %d", tracker.TransferredBytes())
	}
}

func TestFormatSizeAndSpeed(t *testing.T) {
	if FormatSize(500) != "500 B" {
		t.Fatalf("expected '500 B', got '%s'", FormatSize(500))
	}
	if FormatSize(1500) != "1.5 KB" {
		t.Fatalf("expected '1.5 KB', got '%s'", FormatSize(1500))
	}
	if FormatSize(1500*1024) != "1.5 MB" {
		t.Fatalf("expected '1.5 MB', got '%s'", FormatSize(1500*1024))
	}
	if FormatSize(1500*1024*1024) != "1.5 GB" {
		t.Fatalf("expected '1.5 GB', got '%s'", FormatSize(1500*1024*1024))
	}

	if FormatSpeed(500) != "500 B/s" {
		t.Fatalf("expected '500 B/s', got '%s'", FormatSpeed(500))
	}
	if FormatSpeed(1500) != "1.5 KB/s" {
		t.Fatalf("expected '1.5 KB/s', got '%s'", FormatSpeed(1500))
	}
	if FormatSpeed(1500*1024) != "1.5 MB/s" {
		t.Fatalf("expected '1.5 MB/s', got '%s'", FormatSpeed(1500*1024))
	}
	if FormatSpeed(1500*1024*1024) != "1.5 GB/s" {
		t.Fatalf("expected '1.5 GB/s', got '%s'", FormatSpeed(1500*1024*1024))
	}
}

func TestCountingReaderAndWriter(t *testing.T) {
	tracker := NewProgressTracker(1, 100)
	tracker.SetTTY(false)

	raw := []byte("hello progress tracking world")
	cr := NewCountingReader(bytes.NewReader(raw), tracker)

	buf := make([]byte, 10)
	n, err := cr.Read(buf)
	if err != nil || n != 10 {
		t.Fatalf("read failed: n=%d, err=%v", n, err)
	}
	if tracker.TransferredBytes() != 10 {
		t.Fatalf("expected 10 bytes tracked, got %d", tracker.TransferredBytes())
	}

	rest, err := io.ReadAll(cr)
	if err != nil {
		t.Fatalf("read rest failed: %v", err)
	}
	if len(rest)+10 != len(raw) {
		t.Fatalf("content length mismatch: %d != %d", len(rest)+10, len(raw))
	}
	if tracker.TransferredBytes() != int64(len(raw)) {
		t.Fatalf("expected %d bytes tracked, got %d", len(raw), tracker.TransferredBytes())
	}

	// CountingWriter 测试
	var outBuf bytes.Buffer
	trackerW := NewProgressTracker(1, 50)
	trackerW.SetTTY(false)
	cw := NewCountingWriter(&outBuf, trackerW)

	nw, err := cw.Write([]byte("written-data"))
	if err != nil || nw != len("written-data") {
		t.Fatalf("write failed: nw=%d, err=%v", nw, err)
	}
	if trackerW.TransferredBytes() != int64(len("written-data")) {
		t.Fatalf("expected %d bytes written, got %d", len("written-data"), trackerW.TransferredBytes())
	}
}

func TestProgressTracker_SetOutput_And_FinishOnce(t *testing.T) {
	var buf bytes.Buffer
	tracker := NewProgressTracker(2, 2048)
	tracker.SetOutput(&buf)
	tracker.SetTTY(false)

	tracker.AddBytes(1024)
	tracker.AddFile()
	tracker.Finish()
	tracker.Finish() // 重复调用 Finish，断言幂等

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 line from Finish, got %d: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "Transferred 1/2 files") {
		t.Fatalf("unexpected summary line: %s", lines[0])
	}
}

func TestFormatSizeAndSpeed_NegativeValues(t *testing.T) {
	if s := FormatSize(-100); s != "0 B" {
		t.Fatalf("expected '0 B' for negative size, got '%s'", s)
	}
	if s := FormatSpeed(-50.0); s != "0 B/s" {
		t.Fatalf("expected '0 B/s' for negative speed, got '%s'", s)
	}
}

func TestIsSafeRelativePath(t *testing.T) {
	cases := []struct {
		path string
		safe bool
	}{
		{"file.txt", true},
		{"sub/file.txt", true},
		{"sub/nested/file.txt", true},
		{"", false},
		{"/abs/path", false},
		{"\\abs\\path", false},
		{"../escaped.txt", false},
		{"sub/../../escaped.txt", false},
		{"C:/Windows/calc.exe", false},
		{"C:\\Windows\\calc.exe", false},
		{"D:", false},
		{"file.txt:evil", false},
		{"sub/file.txt:stream:$DATA", false},
		{"foo:bar", false},
		{".", false},
		{"..", false},
		{"sub/.", true},
	}

	for _, tc := range cases {
		got := isSafeRelativePath(tc.path)
		if got != tc.safe {
			t.Errorf("isSafeRelativePath(%q) = %v; want %v", tc.path, got, tc.safe)
		}
	}
}

