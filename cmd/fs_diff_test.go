package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
)

func TestCmd_Diff_SingleFile_Identical(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "file1.txt")
	f2 := filepath.Join(tempDir, "file2.txt")
	content := []byte("identical content 12345")
	_ = os.WriteFile(f1, content, 0644)
	_ = os.WriteFile(f2, content, 0644)

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{f1, f2})
	if err != nil {
		t.Fatalf("expected nil error for identical single files, got: %v", err)
	}
}

func TestCmd_Diff_SingleFile_Mismatch(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "file1.txt")
	f2 := filepath.Join(tempDir, "file2.txt")
	_ = os.WriteFile(f1, []byte("content A"), 0644)
	_ = os.WriteFile(f2, []byte("content B differs"), 0644)

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{f1, f2})
	if err == nil {
		t.Fatal("expected error for mismatched single files")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError with code 1, got: %v", err)
	}
}

func TestCmd_Diff_SingleFile_Missing(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "file1.txt")
	f2 := filepath.Join(tempDir, "missing.txt")
	_ = os.WriteFile(f1, []byte("content A"), 0644)

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{f1, f2})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("expected ExitError with code 2 for missing file, got: %v", err)
	}
}

func TestCmd_Diff_Directory_WithoutRecursiveFlag(t *testing.T) {
	tempDir := t.TempDir()
	dir1 := filepath.Join(tempDir, "dir1")
	dir2 := filepath.Join(tempDir, "dir2")
	_ = os.MkdirAll(dir1, 0755)
	_ = os.MkdirAll(dir2, 0755)

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{dir1, dir2})
	if err == nil {
		t.Fatal("expected omitting directory error when -r is not passed")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 2 || !strings.Contains(exitErr.Msg, "omitting directory") {
		t.Fatalf("expected exit code 2 with omitting directory message, got: %v", err)
	}
}

func TestCmd_Diff_Directory_Identical(t *testing.T) {
	tempDir := t.TempDir()
	dir1 := filepath.Join(tempDir, "dir1")
	dir2 := filepath.Join(tempDir, "dir2")
	_ = os.MkdirAll(filepath.Join(dir1, "sub"), 0755)
	_ = os.MkdirAll(filepath.Join(dir2, "sub"), 0755)

	_ = os.WriteFile(filepath.Join(dir1, "a.txt"), []byte("data a"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "a.txt"), []byte("data a"), 0644)
	_ = os.WriteFile(filepath.Join(dir1, "sub", "b.txt"), []byte("data b in sub"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "sub", "b.txt"), []byte("data b in sub"), 0644)

	diffCmd.Flags().Set("recursive", "true")
	defer diffCmd.Flags().Set("recursive", "false")

	err := diffCmd.RunE(diffCmd, []string{dir1, dir2})
	if err != nil {
		t.Fatalf("expected identical directories to return nil, got: %v", err)
	}
}

func TestCmd_Diff_Directory_WithDifferences_OmitMatch(t *testing.T) {
	tempDir := t.TempDir()
	dir1 := filepath.Join(tempDir, "dir1")
	dir2 := filepath.Join(tempDir, "dir2")
	_ = os.MkdirAll(dir1, 0755)
	_ = os.MkdirAll(dir2, 0755)

	// 1. 同名且相同内容 (MATCH 项，应当被默认过滤掉)
	_ = os.WriteFile(filepath.Join(dir1, "matched.txt"), []byte("matched content"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "matched.txt"), []byte("matched content"), 0644)

	// 2. 同名但内容不同 (MODIFIED 项)
	_ = os.WriteFile(filepath.Join(dir1, "modified.txt"), []byte("original"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "modified.txt"), []byte("changed content"), 0644)

	// 3. 仅源端存在 (ADDED 项)
	_ = os.WriteFile(filepath.Join(dir1, "only_in_src.txt"), []byte("new file"), 0644)

	// 4. 仅目标端存在 (DELETED 项)
	_ = os.WriteFile(filepath.Join(dir2, "only_in_dst.txt"), []byte("deleted file"), 0644)

	diffCmd.Flags().Set("recursive", "true")
	defer diffCmd.Flags().Set("recursive", "false")

	// 捕获 stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := diffCmd.RunE(diffCmd, []string{dir1, dir2})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err == nil {
		t.Fatal("expected error with exit code 1 when differences exist")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError with code 1, got: %v", err)
	}

	// 验证关键断言：
	// 1. [MATCH] 必须被默认排除！
	if strings.Contains(output, "[MATCH]") {
		t.Fatalf("output must NOT contain [MATCH] entries by default, got output:\n%s", output)
	}

	// 2. 差异项必须完整呈现
	if !strings.Contains(output, "[MODIFIED]") || !strings.Contains(output, "modified.txt") {
		t.Fatalf("missing [MODIFIED] entry in output:\n%s", output)
	}
	if !strings.Contains(output, "[ADDED]") || !strings.Contains(output, "only_in_src.txt") {
		t.Fatalf("missing [ADDED] entry in output:\n%s", output)
	}
	if !strings.Contains(output, "[DELETED]") || !strings.Contains(output, "only_in_dst.txt") {
		t.Fatalf("missing [DELETED] entry in output:\n%s", output)
	}

	// 3. 统计行必须准确记录匹配数与变动数
	expectedSummary := "Summary: 1 matched, 1 modified, 1 added, 1 deleted."
	if !strings.Contains(output, expectedSummary) {
		t.Fatalf("expected summary %q, got output:\n%s", expectedSummary, output)
	}
}

func TestCmd_Diff_CrossNode(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	localFile := filepath.Join(tempProfile, "local.txt")
	content := []byte("cross node content")
	_ = os.WriteFile(localFile, content, 0644)

	// Mock Worker Server
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/fs/hash" {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		path := r.URL.Query().Get("path")
		if path == "D:/same.txt" {
			list := []protocol.FileInfo{
				{Name: "same.txt", Path: "same.txt", Size: int64(len(content)), SHA256: "c18e1ef67c3df95a5639b56f8f48039d91a92e1281cb9fc61a941f534444585e"},
			}
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(list)
			return
		}
		if path == "D:/diff.txt" {
			list := []protocol.FileInfo{
				{Name: "diff.txt", Path: "diff.txt", Size: 999, SHA256: "different_hash_value"},
			}
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(list)
			return
		}
		if path == "D:/remote_dir" {
			list := []protocol.FileInfo{
				{Name: "f1.txt", Path: "f1.txt", Size: 10, SHA256: "hash_f1"},
			}
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(list)
			return
		}
		rw.WriteHeader(http.StatusNotFound)
		_, _ = rw.Write([]byte("path not found"))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)

	// 配置 mock-node 和 mock-node2
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-node",
		Target: u.Host,
	})
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-node2",
		Target: u.Host,
	})

	diffCmd.Flags().Set("recursive", "false")

	// 1. 本地 vs 远端 (内容不同)
	diffTarget := "mock-node:D:/diff.txt"
	err := diffCmd.RunE(diffCmd, []string{localFile, diffTarget})
	if err == nil {
		t.Fatal("expected difference for local vs remote")
	}
	if exitErr, ok := err.(*ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError 1, got: %v", err)
	}

	// 2. 远端 vs 本地 (反向拓扑)
	err = diffCmd.RunE(diffCmd, []string{diffTarget, localFile})
	if err == nil {
		t.Fatal("expected difference for remote vs local")
	}
	if exitErr, ok := err.(*ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError 1, got: %v", err)
	}

	// 3. 远端 vs 远端 (两远端节点单文件不同)
	sameTarget := "mock-node2:D:/same.txt"
	err = diffCmd.RunE(diffCmd, []string{sameTarget, diffTarget})
	if err == nil {
		t.Fatal("expected difference for remote vs remote")
	}
	if exitErr, ok := err.(*ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError 1, got: %v", err)
	}

	// 4. 目录跨节点比对全拓扑 (-r)
	localDir := filepath.Join(tempProfile, "local_dir")
	_ = os.MkdirAll(localDir, 0755)
	_ = os.WriteFile(filepath.Join(localDir, "f1.txt"), []byte("different-local-data"), 0644)

	diffCmd.Flags().Set("recursive", "true")

	// 4.1 Local vs Remote (-r)
	err = diffCmd.RunE(diffCmd, []string{localDir, "mock-node:D:/remote_dir"})
	if err == nil {
		t.Fatal("expected difference for local vs remote dir")
	}
	if exitErr, ok := err.(*ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError 1 for local vs remote dir, got: %v", err)
	}

	// 4.2 Remote vs Local (-r)
	err = diffCmd.RunE(diffCmd, []string{"mock-node:D:/remote_dir", localDir})
	if err == nil {
		t.Fatal("expected difference for remote vs local dir")
	}
	if exitErr, ok := err.(*ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError 1 for remote vs local dir, got: %v", err)
	}

	// 4.3 Remote vs Remote (-r) (内容一致返回 0)
	err = diffCmd.RunE(diffCmd, []string{"mock-node:D:/remote_dir", "mock-node2:D:/remote_dir"})
	if err != nil {
		t.Fatalf("expected 0 exit code for identical remote dirs, got: %v", err)
	}

	// 5. 远端不存在路径报错 (ExitCode 2)
	diffCmd.Flags().Set("recursive", "false")
	missingTarget := "mock-node:D:/missing.txt"
	err = diffCmd.RunE(diffCmd, []string{localFile, missingTarget})
	if err == nil {
		t.Fatal("expected error for remote missing path")
	}
	if exitErr, ok := err.(*ExitError); !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("expected ExitError 2, got: %v", err)
	}
}

func TestCmd_Diff_Directory_OmitTruncation(t *testing.T) {
	tempDir := t.TempDir()
	dir1 := filepath.Join(tempDir, "d1")
	dir2 := filepath.Join(tempDir, "d2")
	_ = os.MkdirAll(dir1, 0755)
	_ = os.MkdirAll(dir2, 0755)

	// 创建 60 个内容不同的文件
	for i := 1; i <= 60; i++ {
		name := fmt.Sprintf("file_%03d.txt", i)
		_ = os.WriteFile(filepath.Join(dir1, name), []byte(fmt.Sprintf("content-a-%d", i)), 0644)
		_ = os.WriteFile(filepath.Join(dir2, name), []byte(fmt.Sprintf("content-b-%d", i)), 0644)
	}

	diffCmd.Flags().Set("recursive", "true")
	defer diffCmd.Flags().Set("recursive", "false")

	// 1. 默认 limit 50 截断测试
	diffCmd.Flags().Set("all", "false")
	diffCmd.Flags().Set("limit", "50")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := diffCmd.RunE(diffCmd, []string{dir1, dir2})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	if err == nil {
		t.Fatal("expected error with exit code 1")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitCode 1, got %v", err)
	}

	expectedOmit := "... and 10 more differing entries omitted (use --all to show all)"
	if !strings.Contains(out, expectedOmit) {
		t.Fatalf("expected omitted message %q, got output:\n%s", expectedOmit, out)
	}
	expectedSummary := "Summary: 0 matched, 60 modified, 0 added, 0 deleted."
	if !strings.Contains(out, expectedSummary) {
		t.Fatalf("expected summary %q, got output:\n%s", expectedSummary, out)
	}

	// 2. 传 --all 显示全部测试
	diffCmd.Flags().Set("all", "true")
	defer diffCmd.Flags().Set("all", "false")

	rAll, wAll, _ := os.Pipe()
	os.Stdout = wAll

	_ = diffCmd.RunE(diffCmd, []string{dir1, dir2})

	_ = wAll.Close()
	os.Stdout = oldStdout
	var bufAll bytes.Buffer
	_, _ = io.Copy(&bufAll, rAll)
	outAll := bufAll.String()

	if strings.Contains(outAll, "omitted") {
		t.Fatalf("output with --all must not contain omitted message, got:\n%s", outAll)
	}
	if !strings.Contains(outAll, "file_060.txt") {
		t.Fatalf("file_060.txt should be in output when --all is set")
	}

	// 3. 自定义 --limit 5 测试
	diffCmd.Flags().Set("all", "false")
	diffCmd.Flags().Set("limit", "5")
	defer diffCmd.Flags().Set("limit", "50")

	rLim, wLim, _ := os.Pipe()
	os.Stdout = wLim

	_ = diffCmd.RunE(diffCmd, []string{dir1, dir2})

	_ = wLim.Close()
	os.Stdout = oldStdout
	var bufLim bytes.Buffer
	_, _ = io.Copy(&bufLim, rLim)
	outLim := bufLim.String()

	expectedOmit5 := "... and 55 more differing entries omitted (use --all to show all)"
	if !strings.Contains(outLim, expectedOmit5) {
		t.Fatalf("expected omitted message %q, got:\n%s", expectedOmit5, outLim)
	}
}

func TestCmd_Diff_ConcurrentExecution_Speedup(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	var (
		activeCount    atomic.Int32
		maxActiveCount atomic.Int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/fs/hash" {
			rw.WriteHeader(http.StatusNotFound)
			return
		}

		current := activeCount.Add(1)
		for {
			max := maxActiveCount.Load()
			if current <= max || maxActiveCount.CompareAndSwap(max, current) {
				break
			}
		}
		defer activeCount.Add(-1)

		// 模拟耗时哈希计算 (两端各需 80ms)
		time.Sleep(80 * time.Millisecond)

		flusher, _ := rw.(http.Flusher)
		rw.Header().Set("Content-Type", "application/x-ndjson")
		rw.WriteHeader(http.StatusOK)
		if flusher != nil {
			flusher.Flush()
		}

		path := r.URL.Query().Get("path")
		fileName := filepath.Base(path)

		event := protocol.FsHashEvent{
			Event: protocol.FsHashEventEntry,
			Entry: &protocol.FileInfo{
				Name:   fileName,
				Path:   fileName,
				Size:   1024,
				SHA256: "fake-hash-12345",
			},
		}
		data, _ := json.Marshal(event)
		_, _ = rw.Write(append(data, '\n'))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-src",
		Target: u.Host,
	})
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-dst",
		Target: u.Host,
	})

	diffCmd.Flags().Set("recursive", "false")
	diffCmd.Flags().Set("all", "false")
	diffCmd.Flags().Set("limit", "50")

	startTime := time.Now()
	err := diffCmd.RunE(diffCmd, []string{"node-src:D:/file.txt", "node-dst:D:/file.txt"})
	elapsed := time.Since(startTime)

	if err != nil {
		t.Fatalf("expected identical files to return nil, got: %v", err)
	}

	// 核心断言 1: 最大并发请求数必须达到 2 (证明两端是并发异步执行的)
	if maxActiveCount.Load() < 2 {
		t.Fatalf("expected concurrent execution with maxActiveCount >= 2, got: %d", maxActiveCount.Load())
	}

	// 核心断言 2: 总执行耗时接近 max(T1, T2) (~80ms)，严格小于串行 T1+T2 (>=160ms)
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("expected concurrent elapsed time < 150ms, took: %v", elapsed)
	}
}

func TestCmd_Diff_ConcurrentFailFast(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	dstCanceledChan := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if strings.Contains(path, "fail-immediate") {
			// 源端立即失败 (404 Not Found)
			rw.WriteHeader(http.StatusNotFound)
			_, _ = rw.Write([]byte("path not found on src"))
			return
		}

		// 目标端模拟长耗时哈希流，挂起等待或监听 Context 取消
		flusher, _ := rw.(http.Flusher)
		rw.Header().Set("Content-Type", "application/x-ndjson")
		rw.WriteHeader(http.StatusOK)
		if flusher != nil {
			flusher.Flush()
		}

		// 监听请求上下文是否被客户端因源端失败而触发快速熔断 (Cancel)
		select {
		case <-r.Context().Done():
			select {
			case dstCanceledChan <- struct{}{}:
			default:
			}
			return
		case <-time.After(3 * time.Second):
			return
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "fast-fail-node",
		Target: u.Host,
	})

	diffCmd.Flags().Set("recursive", "false")

	startTime := time.Now()
	err := diffCmd.RunE(diffCmd, []string{"fast-fail-node:D:/fail-immediate.txt", "fast-fail-node:D:/long-wait.txt"})
	elapsed := time.Since(startTime)

	if err == nil {
		t.Fatal("expected error due to src failure")
	}

	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("expected ExitError with code 2, got: %v", err)
	}

	// 验证目标端是否在短时间内被极速熔断
	select {
	case <-dstCanceledChan:
		// 目标端成功收到客户端 context cancel 信号
	case <-time.After(1 * time.Second):
		t.Fatal("expected dst request to be promptly canceled via fail-fast context, but timed out")
	}

	// 耗时应当远小于目标端的 3 秒等待
	if elapsed >= 1*time.Second {
		t.Fatalf("fail-fast took too long: %v (expected < 1s)", elapsed)
	}
}

func TestCmd_Diff_CrossNode_NDJSON_Stream(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/fs/hash" {
			rw.WriteHeader(http.StatusNotFound)
			return
		}

		rw.Header().Set("Content-Type", "application/x-ndjson")
		rw.WriteHeader(http.StatusOK)
		flusher, _ := rw.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}

		path := r.URL.Query().Get("path")
		enc := json.NewEncoder(rw)

		// 模拟发送 init 事件
		_ = enc.Encode(protocol.FsHashEvent{
			Event:      protocol.FsHashEventInit,
			TotalFiles: 2,
			TotalBytes: 2048,
		})
		if flusher != nil {
			flusher.Flush()
		}

		// 发送 entry 事件
		f1Hash := "hash-common"
		f2Hash := "hash-src-unique"
		if strings.Contains(path, "dst_dir") {
			f2Hash = "hash-dst-unique"
		}

		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventEntry,
			Entry: &protocol.FileInfo{
				Name:   "f1.txt",
				Path:   "f1.txt",
				Size:   1024,
				SHA256: f1Hash,
			},
		})
		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventEntry,
			Entry: &protocol.FileInfo{
				Name:   "f2.txt",
				Path:   "f2.txt",
				Size:   1024,
				SHA256: f2Hash,
			},
		})
		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventDone,
		})
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "stream-node",
		Target: u.Host,
	})

	diffCmd.Flags().Set("recursive", "true")
	defer diffCmd.Flags().Set("recursive", "false")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := diffCmd.RunE(diffCmd, []string{"stream-node:D:/src_dir", "stream-node:D:/dst_dir"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err == nil {
		t.Fatal("expected exit error 1 for modified file")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError with code 1, got: %v", err)
	}

	if !strings.Contains(output, "[MODIFIED]") || !strings.Contains(output, "f2.txt") {
		t.Fatalf("expected output to contain modified f2.txt, got:\n%s", output)
	}
	if strings.Contains(output, "[MATCH]") {
		t.Fatalf("output should not contain [MATCH] entries, got:\n%s", output)
	}
	if !strings.Contains(output, "Summary: 1 matched, 1 modified, 0 added, 0 deleted.") {
		t.Fatalf("unexpected summary line, got:\n%s", output)
	}
}

// 验证在两端并发比对中，若某端遭遇网络中途掐断/连接重置，CLI 能极速熔断对端并以 ExitCode 2 安全退出
func TestCmd_Diff_ConcurrentNetworkDrop(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	waitCanceledChan := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if strings.Contains(path, "drop-node") {
			rw.Header().Set("Content-Type", "application/x-ndjson")
			rw.WriteHeader(http.StatusOK)
			flusher, _ := rw.(http.Flusher)
			if flusher != nil {
				flusher.Flush()
			}
			// 模拟网络中途突然断开 (Hijack 后暴力关闭底层 Socket)
			hj, ok := rw.(http.Hijacker)
			if !ok {
				t.Fatal("server does not support hijacking")
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}

		// 另一端模拟长任务挂起等待
		flusher, _ := rw.(http.Flusher)
		rw.Header().Set("Content-Type", "application/x-ndjson")
		rw.WriteHeader(http.StatusOK)
		if flusher != nil {
			flusher.Flush()
		}

		select {
		case <-r.Context().Done():
			select {
			case waitCanceledChan <- struct{}{}:
			default:
			}
			return
		case <-time.After(3 * time.Second):
			return
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-drop",
		Target: u.Host,
	})
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-wait",
		Target: u.Host,
	})

	diffCmd.Flags().Set("recursive", "true")
	defer diffCmd.Flags().Set("recursive", "false")

	start := time.Now()
	err := diffCmd.RunE(diffCmd, []string{"node-drop:D:/drop-node/data", "node-wait:D:/wait-node/data"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error due to network drop")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("expected ExitCode 2 on network drop, got: %v", err)
	}

	select {
	case <-waitCanceledChan:
		// 目标端被对端的网络中断成功熔断
	case <-time.After(1 * time.Second):
		t.Fatal("expected wait-node to be canceled promptly upon drop-node network break")
	}

	if elapsed > 1*time.Second {
		t.Errorf("expected fast failure exit < 1s, took %v", elapsed)
	}
}

// 验证 1000 级海量批量文件跨机并发 NDJSON 比对与输出截断
func TestCmd_Diff_LargeBatch_1000Files(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/x-ndjson")
		rw.WriteHeader(http.StatusOK)
		flusher, _ := rw.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}

		path := r.URL.Query().Get("path")
		isSrc := strings.Contains(path, "src")
		enc := json.NewEncoder(rw)

		_ = enc.Encode(protocol.FsHashEvent{
			Event:      protocol.FsHashEventInit,
			TotalFiles: 999,
			TotalBytes: 99900,
		})
		if flusher != nil {
			flusher.Flush()
		}

		// 1..997 项两端完全一致 (MATCH)
		for i := 1; i <= 997; i++ {
			p := fmt.Sprintf("sub/file_%04d.txt", i)
			_ = enc.Encode(protocol.FsHashEvent{
				Event: protocol.FsHashEventEntry,
				Entry: &protocol.FileInfo{
					Name:   filepath.Base(p),
					Path:   p,
					Size:   100,
					SHA256: fmt.Sprintf("hash_match_%04d", i),
				},
			})
		}

		// 第 998 项：内容不同 (MODIFIED)
		p998 := "sub/file_0998.txt"
		hash998 := "hash_998_src"
		if !isSrc {
			hash998 = "hash_998_dst"
		}
		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventEntry,
			Entry: &protocol.FileInfo{
				Name:   filepath.Base(p998),
				Path:   p998,
				Size:   100,
				SHA256: hash998,
			},
		})

		// 第 999 项：源端独有 (ADDED)
		if isSrc {
			p999 := "sub/file_0999_src_only.txt"
			_ = enc.Encode(protocol.FsHashEvent{
				Event: protocol.FsHashEventEntry,
				Entry: &protocol.FileInfo{
					Name:   filepath.Base(p999),
					Path:   p999,
					Size:   100,
					SHA256: "hash_999",
				},
			})
		}

		// 第 1000 项：目标端独有 (DELETED)
		if !isSrc {
			p1000 := "sub/file_1000_dst_only.txt"
			_ = enc.Encode(protocol.FsHashEvent{
				Event: protocol.FsHashEventEntry,
				Entry: &protocol.FileInfo{
					Name:   filepath.Base(p1000),
					Path:   p1000,
					Size:   100,
					SHA256: "hash_1000",
				},
			})
		}

		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventDone,
		})
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-batch-src",
		Target: u.Host,
	})
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-batch-dst",
		Target: u.Host,
	})

	diffCmd.Flags().Set("recursive", "true")
	diffCmd.Flags().Set("all", "false")
	diffCmd.Flags().Set("limit", "50")
	defer diffCmd.Flags().Set("recursive", "false")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	start := time.Now()
	err := diffCmd.RunE(diffCmd, []string{"node-batch-src:D:/src", "node-batch-dst:D:/dst"})
	elapsed := time.Since(start)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err == nil {
		t.Fatal("expected exit error 1 for differences")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitCode 1, got: %v", err)
	}

	// 验证关键断言:
	// 1. 997 个 MATCH 项默认被全部过滤
	if strings.Contains(output, "[MATCH]") {
		t.Fatalf("output must not contain [MATCH] lines, got:\n%s", output)
	}
	// 2. 差异项完整呈现
	if !strings.Contains(output, "[MODIFIED]") || !strings.Contains(output, "file_0998.txt") {
		t.Fatalf("expected modified file in output, got:\n%s", output)
	}
	if !strings.Contains(output, "[ADDED]") || !strings.Contains(output, "file_0999_src_only.txt") {
		t.Fatalf("expected added file in output, got:\n%s", output)
	}
	if !strings.Contains(output, "[DELETED]") || !strings.Contains(output, "file_1000_dst_only.txt") {
		t.Fatalf("expected deleted file in output, got:\n%s", output)
	}
	// 3. 统计信息精确无误
	expectedSummary := "Summary: 997 matched, 1 modified, 1 added, 1 deleted."
	if !strings.Contains(output, expectedSummary) {
		t.Fatalf("expected summary %q, got:\n%s", expectedSummary, output)
	}
	t.Logf("Concurrent diff on 1000 items took %v", elapsed)
}

// 验证在 TTY 终端模式下，两端并发哈希时 ProgressTracker 进度条正确聚合两端总数，绝无分子大于分母的倒挂
func TestCmd_Diff_TTY_SharedProgressTracker(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/x-ndjson")
		rw.WriteHeader(http.StatusOK)
		flusher := rw.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}

		path := r.URL.Query().Get("path")
		isSrc := strings.Contains(path, "src")
		enc := json.NewEncoder(rw)

		// 模拟每端各有 20 个文件，各 2000 字节
		_ = enc.Encode(protocol.FsHashEvent{
			Event:      protocol.FsHashEventInit,
			TotalFiles: 20,
			TotalBytes: 2000,
		})
		if flusher != nil {
			flusher.Flush()
		}

		prefix := "dst"
		if isSrc {
			prefix = "src"
		}
		for i := 1; i <= 20; i++ {
			_ = enc.Encode(protocol.FsHashEvent{
				Event: protocol.FsHashEventEntry,
				Entry: &protocol.FileInfo{
					Name:   fmt.Sprintf("%s_%d.txt", prefix, i),
					Path:   fmt.Sprintf("file_%d.txt", i),
					Size:   100,
					SHA256: fmt.Sprintf("hash_%d", i),
				},
			})
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(1 * time.Millisecond)
		}

		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventDone,
		})
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "tty-node-src",
		Target: u.Host,
	})
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "tty-node-dst",
		Target: u.Host,
	})

	diffCmd.Flags().Set("recursive", "true")
	defer diffCmd.Flags().Set("recursive", "false")

	// 开启模拟 TTY 终端
	trueVal := true
	client.SetTerminalOverride(&trueVal)
	defer client.SetTerminalOverride(nil)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := diffCmd.RunE(diffCmd, []string{"tty-node-src:D:/src", "tty-node-dst:D:/dst"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Fatalf("expected diff on identical trees to succeed, got: %v", err)
	}

	// 核心断言 1: 最终 Finish 必须体现两端总和 (20 + 20 = 40 files, 2000 + 2000 = 4000 B)
	if !strings.Contains(output, "40/40 files") {
		t.Fatalf("expected progress bar to show combined 40/40 files, got output:\n%s", output)
	}
	if !strings.Contains(output, "3.9 KB") && !strings.Contains(output, "4000 B") && !strings.Contains(output, "4.0 KB") {
		t.Fatalf("expected progress bar to show combined total bytes, got output:\n%s", output)
	}

	// 核心断言 2: 绝不能出现单端未累加导致的分母为 20 (例如 21/20 或 40/20 files)
	if strings.Contains(output, "/20 files") {
		t.Fatalf("output contained stale single-ended denominator /20 files:\n%s", output)
	}
}

// 验证在非 TTY (Agent 管道) 环境下，长任务哈希过程中会定时按设定周期输出单行心跳日志，防止 Agent 挂起超时
func TestCmd_Diff_NonTTY_Heartbeat(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	// 缩短默认心跳周期至 30ms，以便单测毫秒级完成并稳定捕获
	client.SetDefaultHeartbeatInterval(30 * time.Millisecond)
	defer client.SetDefaultHeartbeatInterval(5 * time.Second)

	// 强制非 TTY 模式
	f := false
	client.SetTerminalOverride(&f)
	defer client.SetTerminalOverride(nil)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		flusher, _ := rw.(http.Flusher)
		rw.Header().Set("Content-Type", "application/x-ndjson")
		rw.WriteHeader(http.StatusOK)
		if flusher != nil {
			flusher.Flush()
		}

		enc := json.NewEncoder(rw)
		_ = enc.Encode(protocol.FsHashEvent{
			Event:      protocol.FsHashEventInit,
			TotalFiles: 3,
			TotalBytes: 3000,
		})
		if flusher != nil {
			flusher.Flush()
		}

		// 模拟延迟产生心跳
		time.Sleep(40 * time.Millisecond)
		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventEntry,
			Entry: &protocol.FileInfo{Path: "f1.txt", Size: 1000, SHA256: "hash1"},
		})
		if flusher != nil {
			flusher.Flush()
		}

		time.Sleep(40 * time.Millisecond)
		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventEntry,
			Entry: &protocol.FileInfo{Path: "f2.txt", Size: 1000, SHA256: "hash2"},
		})
		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventEntry,
			Entry: &protocol.FileInfo{Path: "f3.txt", Size: 1000, SHA256: "hash3"},
		})
		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventDone,
		})
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "hb-src", Target: u.Host})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "hb-dst", Target: u.Host})

	diffCmd.Flags().Set("recursive", "true")
	defer diffCmd.Flags().Set("recursive", "false")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := diffCmd.RunE(diffCmd, []string{"hb-src:D:/data", "hb-dst:D:/data"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// 核心断言 1: 在非 TTY 管道中，必须捕获到阶段性的单行心跳日志 "[cworker] Hashed: xx.x%"
	if !strings.Contains(output, "[cworker] Hashed:") {
		t.Fatalf("expected non-TTY heartbeat lines in output, got:\n%s", output)
	}

	// 核心断言 2: 最终 Finish 摘要必须完整呈现 (3 + 3 = 6 files)
	if !strings.Contains(output, "[cworker] Hashed 6/6 files") {
		t.Fatalf("expected final finish summary, got:\n%s", output)
	}

	// 核心断言 3: 最终差异结果正确输出
	if !strings.Contains(output, "All 3 files match") {
		t.Fatalf("expected match summary, got:\n%s", output)
	}
}

// 验证目标端为目录但源端为单文件且未带 -r 时，返回正确的 ExitError(Code: 2) 并提示 omitting directory
func TestCmd_Diff_DestinationDirectory_WithoutRecursiveFlag(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "file1.txt")
	_ = os.WriteFile(f1, []byte("data"), 0644)
	dir2 := filepath.Join(tempDir, "dir2")
	_ = os.MkdirAll(dir2, 0755)

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{f1, dir2})
	if err == nil {
		t.Fatal("expected error when destination is a directory without -r")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("expected ExitError with code 2, got: %v", err)
	}
	if !strings.Contains(exitErr.Error(), "omitting directory") || !strings.Contains(exitErr.Error(), "use -r") {
		t.Fatalf("unexpected error message: %v", exitErr)
	}
}

// 验证在 TTY 模式下遇到错误时，终端能够安全换行退出，避免错误信息与 \r 混排
func TestCmd_Diff_TTY_Error_Println(t *testing.T) {
	trueVal := true
	client.SetTerminalOverride(&trueVal)
	defer client.SetTerminalOverride(nil)

	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "exist.txt")
	_ = os.WriteFile(f1, []byte("data"), 0644)
	fMissing := filepath.Join(tempDir, "missing.txt")

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{f1, fMissing})
	if err == nil {
		t.Fatal("expected error for missing file in TTY")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("expected ExitCode 2, got %v", err)
	}
}

// 验证目录递归对比包含大文件 (>10MB) 时，两端哈希流式推进，
// 且 ProgressTracker 能够正常承载大文件增量字节与收尾汇总
func TestCmd_Diff_LargeFile_IncrementalStreaming(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	dirA := t.TempDir()
	dirB := t.TempDir()

	// 创建一个 12MB 的大文件 (超过 10MB 分块心跳阈值)
	chunk := bytes.Repeat([]byte("K"), 1024*1024)

	fA := filepath.Join(dirA, "large.dat")
	fB := filepath.Join(dirB, "large.dat")

	f1, err := os.Create(fA)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := os.Create(fB)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		_, _ = f1.Write(chunk)
		_, _ = f2.Write(chunk)
	}
	f1.Close()
	f2.Close()

	diffCmd.Flags().Set("recursive", "true")
	diffCmd.Flags().Set("all", "false")
	defer diffCmd.Flags().Set("recursive", "false")

	err = diffCmd.RunE(diffCmd, []string{dirA, dirB})
	if err != nil {
		t.Fatalf("diff command on large file failed: %v", err)
	}
}



