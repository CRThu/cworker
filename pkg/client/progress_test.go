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
	var buf bytes.Buffer
	tracker := NewProgressTracker(5, 5000)
	tracker.SetTTY(false)
	tracker.SetOutput(&buf)

	tracker.AddBytes(1000)
	tracker.AddFile()
	tracker.maybeRender(false)
	tracker.maybeRender(true) // 默认 5s 周期内保持静默，不刷屏

	if buf.Len() > 0 {
		t.Fatalf("expected silent output before heartbeat interval, got: %q", buf.String())
	}

	tracker.Finish() // 仅在 finish 时输出单行摘要
	if !strings.Contains(buf.String(), "[cworker] Transferred 1/5 files") {
		t.Fatalf("expected finish summary in output, got: %q", buf.String())
	}
}

func TestProgressTracker_NonTTY_Heartbeat(t *testing.T) {
	var buf bytes.Buffer
	tracker := NewProgressTracker(10, 10000)
	tracker.SetTTY(false)
	tracker.SetOutput(&buf)
	tracker.SetHeartbeatInterval(20 * time.Millisecond) // 缩短心跳周期以便单测毫秒级验证
	tracker.SetLabel("Hashed")

	// 1. 初次传输数据（耗时 < 20ms），应严格静默
	tracker.AddBytes(1000)
	if buf.Len() > 0 {
		t.Fatalf("expected no heartbeat within interval, got: %q", buf.String())
	}

	// 2. 等待超过心跳周期，产生新数据时应触发单行心跳汇报
	time.Sleep(25 * time.Millisecond)
	tracker.AddBytes(2000) // 累计 3000/10000 字节 = 30.0%

	output := buf.String()
	if !strings.Contains(output, "[cworker] Hashed:") || !strings.Contains(output, "30.0%") {
		t.Fatalf("expected heartbeat progress line with 30.0%%, got: %q", output)
	}
	if !strings.HasSuffix(output, "\n") {
		t.Fatalf("heartbeat line must end with newline, got: %q", output)
	}

	// 3. 再次等待超过心跳周期，累加文件并验证第二条心跳
	buf.Reset()
	time.Sleep(25 * time.Millisecond)
	tracker.AddFile() // 1/10 files

	output2 := buf.String()
	if !strings.Contains(output2, "[cworker] Hashed:") || !strings.Contains(output2, "1/10 files") {
		t.Fatalf("expected second heartbeat with file count, got: %q", output2)
	}

	// 4. 调用 Finish，断言输出最终完成摘要
	buf.Reset()
	tracker.Finish()
	finishOut := buf.String()
	if !strings.Contains(finishOut, "[cworker] Hashed 1/10 files") {
		t.Fatalf("expected final finish line, got: %q", finishOut)
	}
}

// 验证全为 0 字节小文件批量传输/哈希时，即使 totalBytes 为 0，百分比能平滑降级为按已完成文件数计算
func TestProgressTracker_ZeroBytes_NonTTY_Heartbeat(t *testing.T) {
	var buf bytes.Buffer
	tracker := NewProgressTracker(5, 0) // 5 个文件，总字节为 0
	tracker.SetTTY(false)
	tracker.SetOutput(&buf)
	tracker.SetHeartbeatInterval(20 * time.Millisecond)
	tracker.SetLabel("Transferred")

	// 1. 等待心跳周期到达，完成第 1 个文件 (1/5 files = 20%)
	time.Sleep(25 * time.Millisecond)
	tracker.AddFile()

	output := buf.String()
	if !strings.Contains(output, "[cworker] Transferred:") || !strings.Contains(output, "20.0%") || !strings.Contains(output, "1/5 files") {
		t.Fatalf("expected 20.0%% progress based on file count when bytes are zero, got: %q", output)
	}

	// 2. 再次跨越心跳周期，完成第 2 个文件 (2/5 files = 40%)
	buf.Reset()
	time.Sleep(25 * time.Millisecond)
	tracker.AddFile()

	output2 := buf.String()
	if !strings.Contains(output2, "[cworker] Transferred:") || !strings.Contains(output2, "40.0%") || !strings.Contains(output2, "2/5 files") {
		t.Fatalf("expected 40.0%% progress for 2/5 files, got: %q", output2)
	}

	buf.Reset()
	tracker.AddFile()
	tracker.AddFile()
	tracker.AddFile() // 5/5
	tracker.Finish()

	finishOut := buf.String()
	if !strings.Contains(finishOut, "[cworker] Transferred 5/5 files") {
		t.Fatalf("expected finish summary for 5/5 zero-byte files, got: %q", finishOut)
	}
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

func TestProgressTracker_SnapshotAndCallbacks(t *testing.T) {
	tracker := NewProgressTracker(2, 200)
	tracker.SetTTY(false)

	var snapshots []ProgressSnapshot
	var mu sync.Mutex
	tracker.SetUpdateCallback(func(snap ProgressSnapshot) {
		mu.Lock()
		defer mu.Unlock()
		snapshots = append(snapshots, snap)
	})

	tracker.StartFile("alpha.txt")
	snap1 := tracker.Snapshot()
	if len(snap1.ActiveFiles) != 1 || snap1.ActiveFiles[0] != "alpha.txt" {
		t.Fatalf("expected active file alpha.txt, got %v", snap1.ActiveFiles)
	}

	tracker.AddBytes(100)
	tracker.AddFile()
	tracker.EndFile("alpha.txt")

	tracker.StartFile("beta.txt")
	snap2 := tracker.Snapshot()
	if len(snap2.ActiveFiles) != 1 || snap2.ActiveFiles[0] != "beta.txt" {
		t.Fatalf("expected active file beta.txt, got %v", snap2.ActiveFiles)
	}

	tracker.AddBytes(100)
	tracker.AddFile()
	tracker.EndFile("beta.txt")

	tracker.Finish()

	mu.Lock()
	count := len(snapshots)
	mu.Unlock()

	if count == 0 {
		t.Fatal("expected update callbacks to be triggered, got 0")
	}

	finalSnap := tracker.Snapshot()
	if finalSnap.CompletedFiles != 2 || finalSnap.TransferredBytes != 200 || finalSnap.Percent != 100 {
		t.Fatalf("unexpected final snapshot: %+v", finalSnap)
	}
}

// TestProgressTracker_SingleLargeFile_Progress 测试单大文件传输的生命周期：
// 从未知大小 (totalBytes=0) 到 HTTP 头回填大小，再到按真实字节平滑推进百分比，以及防止重复覆盖。
func TestProgressTracker_SingleLargeFile_Progress(t *testing.T) {
	// 初始状态：1 个文件，大小未知 (例如远端下载或中继首包未达)
	tracker := NewProgressTracker(1, 0)
	tracker.SetTTY(false)

	tracker.AddBytes(1024 * 1024) // 已传 1MB
	snap0 := tracker.Snapshot()
	if snap0.TotalBytes != 0 || snap0.Percent != 0 {
		t.Fatalf("expected 0 total bytes and 0%%, got total=%d percent=%d", snap0.TotalBytes, snap0.Percent)
	}

	// 模拟首包响应到达，回填 10GB 真实文件大小
	const tenGB = int64(10 * 1024 * 1024 * 1024)
	tracker.SetTotalBytes(tenGB)

	if tracker.TotalBytes() != tenGB {
		t.Fatalf("expected total bytes %d, got %d", tenGB, tracker.TotalBytes())
	}

	// 传输到 2.5GB (25%)
	const twoPointFiveGB = int64(2560 * 1024 * 1024)
	tracker.AddBytes(twoPointFiveGB - 1024*1024)
	snap1 := tracker.Snapshot()
	if snap1.Percent != 25 {
		t.Fatalf("expected 25%% for 2.5GB/10GB, got %d%%", snap1.Percent)
	}
	if snap1.TransferredBytes != twoPointFiveGB {
		t.Fatalf("expected transferred bytes %d, got %d", twoPointFiveGB, snap1.TransferredBytes)
	}

	// 验证防覆盖：已有有效大小时，再次调用 SetTotalBytes (例如中途某未知小包) 应被忽略
	tracker.SetTotalBytes(2048)
	if tracker.TotalBytes() != tenGB {
		t.Fatalf("expected total bytes protected at %d, but was overwritten to %d", tenGB, tracker.TotalBytes())
	}

	// 传满剩余 7.5GB 并完成文件
	tracker.AddBytes(tenGB - twoPointFiveGB)
	tracker.AddFile()
	tracker.Finish()
	snapEnd := tracker.Snapshot()
	if snapEnd.CompletedFiles != 1 || snapEnd.Percent != 100 || snapEnd.TransferredBytes != tenGB {
		t.Fatalf("expected finished single file with 100%%, got %+v", snapEnd)
	}
}

// TestProgressTracker_MultiFile_TotalBytesProtection 测试多文件批量传输时，
// 全局总大小 (totalBytes) 绝对禁止被并发子文件的 SetTotalBytes 覆盖篡改。
func TestProgressTracker_MultiFile_TotalBytesProtection(t *testing.T) {
	const totalDirBytes = int64(50 * 1024 * 1024) // 50MB 目录总大小
	const totalFiles = int64(20)                  // 20 个文件

	tracker := NewProgressTracker(totalFiles, totalDirBytes)
	tracker.SetTTY(false)

	// 模拟并发多协程传输各个子文件，各个子协程收到子文件响应头并尝试调用 SetTotalBytes
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(fileIdx int) {
			defer wg.Done()
			// 尝试用单个小文件大小 (例如 1KB) 破坏全局分母
			tracker.SetTotalBytes(1024)
			// 传输该文件数据
			tracker.AddBytes(1024 * 1024) // 1MB
			tracker.AddFile()
		}(i)
	}
	wg.Wait()

	// 核心断言：全局总大小必须依然是 50MB，绝不能被任何子文件的 1024 篡改！
	if tracker.TotalBytes() != totalDirBytes {
		t.Fatalf("CRITICAL BUG: directory totalBytes corrupted! expected %d, got %d", totalDirBytes, tracker.TotalBytes())
	}

	snap := tracker.Snapshot()
	if snap.TotalFiles != totalFiles {
		t.Fatalf("expected total files %d, got %d", totalFiles, snap.TotalFiles)
	}
	if snap.CompletedFiles != 10 {
		t.Fatalf("expected 10 completed files, got %d", snap.CompletedFiles)
	}
	// 10MB / 50MB = 20%
	if snap.Percent != 20 {
		t.Fatalf("expected 20%% based on real byte size (10MB/50MB), got %d%%", snap.Percent)
	}
}

// TestProgressTracker_FallbackToFileCountWhenZeroBytes 测试全空文件 (totalBytes == 0) 时，
// 平滑回退为按已完成文件数比例计算进度。
func TestProgressTracker_FallbackToFileCountWhenZeroBytes(t *testing.T) {
	tracker := NewProgressTracker(5, 0)
	tracker.SetTTY(false)

	snap0 := tracker.Snapshot()
	if snap0.Percent != 0 {
		t.Fatalf("expected 0%% initially, got %d%%", snap0.Percent)
	}

	tracker.AddFile()
	tracker.AddFile()
	snap1 := tracker.Snapshot()
	// 2/5 = 40%
	if snap1.Percent != 40 {
		t.Fatalf("expected 40%% for 2/5 files when totalBytes==0, got %d%%", snap1.Percent)
	}

	tracker.AddFile()
	tracker.AddFile()
	tracker.AddFile()
	snap2 := tracker.Snapshot()
	if snap2.Percent != 100 {
		t.Fatalf("expected 100%% for 5/5 files, got %d%%", snap2.Percent)
	}
}

// 验证 ProgressTracker 在各种空指针、非法负数参数及防御性降级分支下的绝对安全
func TestProgressTracker_NilAndDefensiveBranches(t *testing.T) {
	// 1. 全局配置接口测试
	SetDefaultHeartbeatInterval(10 * time.Millisecond)
	SetDefaultHeartbeatInterval(0) // 校验 <=0 时回退为 5s
	if defaultHeartbeatInterval != 5*time.Second {
		t.Fatalf("expected fallback to 5s, got %v", defaultHeartbeatInterval)
	}

	trueVal := true
	SetTerminalOverride(&trueVal)
	if !IsTerminal() {
		t.Fatal("expected IsTerminal true with override")
	}
	falseVal := false
	SetTerminalOverride(&falseVal)
	if IsTerminal() {
		t.Fatal("expected IsTerminal false with override")
	}
	SetTerminalOverride(nil)

	// 2. 负数参数初始化
	negTracker := NewProgressTracker(-5, -500)
	if negTracker.TotalFiles() != 0 || negTracker.TotalBytes() != 0 {
		t.Fatalf("expected 0 for negative totals, got files=%d bytes=%d", negTracker.TotalFiles(), negTracker.TotalBytes())
	}

	// 3. nil 指针方法调用（不 panic）
	var nilTr *ProgressTracker
	nilTr.SetLabel("Test")
	nilTr.SetHeartbeatInterval(time.Second)
	nilTr.SetUpdateCallback(nil)
	nilTr.SetTotals(1, 1)
	nilTr.AddTotals(1, 1)
	nilTr.StartFile("a.txt")
	nilTr.EndFile("a.txt")
	nilTr.SetOutput(nil)
	nilTr.maybeRender(true)
	if nilTr.Snapshot().Percent != 0 {
		t.Fatal("expected 0 for nil tracker snapshot")
	}
	if nilTr.getWriter() == nil {
		t.Fatal("nil tracker getWriter should return os.Stdout")
	}

	// 4. 有效实例上的边界测试
	tr := NewProgressTracker(2, 200)
	tr.StartFile("") // 空文件名忽略
	tr.EndFile("")   // 空文件名忽略
	tr.AddBytes(-50) // 负数字节忽略
	tr.SetTotals(-1, -1)
	tr.SetOutput(nil) // 重置为 os.Stdout
	if tr.getWriter() == nil {
		t.Fatal("expected non-nil writer")
	}
	tr.SetHeartbeatInterval(0) // 自动回退为 5s
	tr.maybeRender(true)
	tr.Finish()
}



