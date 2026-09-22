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
	"testing"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
)

func TestCmd_Local_CatAndMd(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 测试 md 本地递归创建目录 (带多层子目录)
	targetDir := filepath.Join(tempDir, "a", "b", "c")
	if err := mdCmd.RunE(mdCmd, []string{targetDir}); err != nil {
		t.Fatalf("local md failed: %v", err)
	}
	if fi, err := os.Stat(targetDir); err != nil || !fi.IsDir() {
		t.Fatalf("expected directory %s to be created", targetDir)
	}

	// 2. 测试 cat 本地输出文件内容
	testFile := filepath.Join(targetDir, "test.txt")
	testContent := []byte("antigravity cworker local cat test")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := catCmd.RunE(catCmd, []string{testFile})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if err != nil {
		t.Fatalf("local cat failed: %v", err)
	}
	if buf.String() != string(testContent) {
		t.Fatalf("expected cat output %q, got %q", string(testContent), buf.String())
	}
}

func TestCmd_Local_Cp_EmptyDirAndFiles(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	dstDir := filepath.Join(tempDir, "dst")

	// 创建空文件与空子目录
	emptySubDir := filepath.Join(srcDir, "empty_sub")
	_ = os.MkdirAll(emptySubDir, 0755)
	emptyFile := filepath.Join(srcDir, "empty.txt")
	_ = os.WriteFile(emptyFile, []byte{}, 0644)

	cpCmd.Flags().Set("recursive", "true")
	defer cpCmd.Flags().Set("recursive", "false")

	if err := cpCmd.RunE(cpCmd, []string{srcDir, dstDir}); err != nil {
		t.Fatalf("local copy with empty dirs and files failed: %v", err)
	}

	// 验证空子目录守恒
	dstEmptySub := filepath.Join(dstDir, "empty_sub")
	if fi, err := os.Stat(dstEmptySub); err != nil || !fi.IsDir() {
		t.Fatalf("empty directory was not copied: %v", err)
	}

	// 验证空文件
	dstEmptyFile := filepath.Join(dstDir, "empty.txt")
	if fi, err := os.Stat(dstEmptyFile); err != nil || fi.Size() != 0 {
		t.Fatalf("empty file was not preserved: %v", err)
	}
}

func TestCmd_Local_LsAndRm(t *testing.T) {
	tempDir := t.TempDir()
	file1 := filepath.Join(tempDir, "f1.txt")
	subDir := filepath.Join(tempDir, "sub")
	_ = os.WriteFile(file1, []byte("test"), 0644)
	_ = os.MkdirAll(subDir, 0755)

	// 1. 本地 ls 测试
	if err := lsCmd.RunE(lsCmd, []string{tempDir}); err != nil {
		t.Fatalf("local ls failed: %v", err)
	}

	// 2. 本地 rm 单文件测试 (-y 自动确认)
	rmCmd.Flags().Set("yes", "true")
	rmCmd.Flags().Set("recursive", "false")
	if err := rmCmd.RunE(rmCmd, []string{file1}); err != nil {
		t.Fatalf("local rm single file failed: %v", err)
	}
	if _, err := os.Stat(file1); !os.IsNotExist(err) {
		t.Fatalf("file %s should have been deleted", file1)
	}

	// 3. 本地 rm 非空目录未传 -r 被拦截
	subFile := filepath.Join(subDir, "subfile.txt")
	_ = os.WriteFile(subFile, []byte("sub"), 0644)
	err := rmCmd.RunE(rmCmd, []string{subDir})
	if err == nil {
		t.Fatal("expected error when deleting non-empty directory without -r")
	}

	// 4. 本地 rm 递归删除 (-r -y)
	rmCmd.Flags().Set("recursive", "true")
	defer func() {
		rmCmd.Flags().Set("recursive", "false")
		rmCmd.Flags().Set("yes", "false")
	}()
	if err := rmCmd.RunE(rmCmd, []string{subDir}); err != nil {
		t.Fatalf("local rm recursive failed: %v", err)
	}
	if _, err := os.Stat(subDir); !os.IsNotExist(err) {
		t.Fatalf("directory %s should have been deleted", subDir)
	}
}

// 验证在非 TTY (Agent 管道) 环境下，长时间目录递归删除会触发单行心跳保活日志，防止 Agent 超时
func TestCmd_Rm_NonTTY_Heartbeat(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	// 缩短心跳周期至 25ms 以便毫秒级测试
	client.SetDefaultHeartbeatInterval(25 * time.Millisecond)
	defer client.SetDefaultHeartbeatInterval(5 * time.Second)

	f := false
	client.SetTerminalOverride(&f)
	defer client.SetTerminalOverride(nil)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// 模拟耗时 35ms 递归删除
		time.Sleep(35 * time.Millisecond)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("DELETED"))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "rm-hb-node", Target: u.Host})

	rmCmd.Flags().Set("recursive", "true")
	rmCmd.Flags().Set("yes", "true")
	defer func() {
		rmCmd.Flags().Set("recursive", "false")
		rmCmd.Flags().Set("yes", "false")
	}()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := rmCmd.RunE(rmCmd, []string{"rm-hb-node:D:/huge_dir"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// 核心断言 1: 在非 TTY 管道中必须捕获到保活心跳日志
	if !strings.Contains(output, "[cworker] Deleting 'rm-hb-node:D:/huge_dir'") {
		t.Fatalf("expected non-TTY heartbeat in rm output, got:\n%s", output)
	}

	// 核心断言 2: 最终必须输出成功完成标识
	if !strings.Contains(output, "[OK] Deleted 'rm-hb-node:D:/huge_dir'") {
		t.Fatalf("expected deleted success output, got:\n%s", output)
	}
}

// 验证在 TTY 模式下遇到长耗时删除时，终端在同一行原地刷新动态提示
func TestCmd_Rm_TTY_Heartbeat(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	client.SetDefaultHeartbeatInterval(25 * time.Millisecond)
	defer client.SetDefaultHeartbeatInterval(5 * time.Second)

	trueVal := true
	client.SetTerminalOverride(&trueVal)
	defer client.SetTerminalOverride(nil)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		time.Sleep(35 * time.Millisecond)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("DELETED"))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "rm-tty-node", Target: u.Host})

	rmCmd.Flags().Set("recursive", "true")
	rmCmd.Flags().Set("yes", "true")
	defer func() {
		rmCmd.Flags().Set("recursive", "false")
		rmCmd.Flags().Set("yes", "false")
	}()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := rmCmd.RunE(rmCmd, []string{"rm-tty-node:D:/tty_dir"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// 核心断言 1: 在 TTY 模式下必须包含 \r 原地覆写提示
	if !strings.Contains(output, "\rDeleting 'rm-tty-node:D:/tty_dir'") {
		t.Fatalf("expected TTY carriage return prompt, got:\n%s", output)
	}

	// 核心断言 2: 最终包含成功输出
	if !strings.Contains(output, "[OK] Deleted 'rm-tty-node:D:/tty_dir'") {
		t.Fatalf("expected success message, got:\n%s", output)
	}
}

// 验证本地递归删除深层且包含大量子目录和文件的层级时，能够完全物理清理且正确报告
func TestCmd_Rm_Local_LargeHierarchy_WithFiles(t *testing.T) {
	tempDir := t.TempDir()
	rootDir := filepath.Join(tempDir, "tree_root")

	// 创建多层嵌套目录与几十个文件
	for i := 1; i <= 5; i++ {
		sub := filepath.Join(rootDir, fmt.Sprintf("sub_%d", i), "nested")
		_ = os.MkdirAll(sub, 0755)
		for j := 1; j <= 5; j++ {
			_ = os.WriteFile(filepath.Join(sub, fmt.Sprintf("file_%d.dat", j)), []byte("payload"), 0644)
		}
	}

	rmCmd.Flags().Set("recursive", "true")
	rmCmd.Flags().Set("yes", "true")
	defer func() {
		rmCmd.Flags().Set("recursive", "false")
		rmCmd.Flags().Set("yes", "false")
	}()

	err := rmCmd.RunE(rmCmd, []string{rootDir})
	if err != nil {
		t.Fatalf("local rm recursive on hierarchy failed: %v", err)
	}

	if _, err := os.Stat(rootDir); !os.IsNotExist(err) {
		t.Fatalf("rootDir should have been completely removed, got err: %v", err)
	}
}

// 验证在 TTY 模式下接收到 NDJSON 流式事件时，原地刷新已删除项数与速率并给出最终完成统计
func TestCmd_Rm_Streaming_TTY(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	trueVal := true
	client.SetTerminalOverride(&trueVal)
	defer client.SetTerminalOverride(nil)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/rm") {
			rw.Header().Set("Content-Type", "application/x-ndjson")
			rw.WriteHeader(http.StatusOK)
			flusher, _ := rw.(http.Flusher)
			enc := json.NewEncoder(rw)

			_ = enc.Encode(protocol.FsRmEvent{Event: protocol.FsRmEventProgress, RemovedCount: 250})
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)

			_ = enc.Encode(protocol.FsRmEvent{Event: protocol.FsRmEventDone, RemovedCount: 1520})
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "rm-stream-tty", Target: u.Host})

	rmCmd.Flags().Set("recursive", "true")
	rmCmd.Flags().Set("yes", "true")
	defer func() {
		rmCmd.Flags().Set("recursive", "false")
		rmCmd.Flags().Set("yes", "false")
	}()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := rmCmd.RunE(rmCmd, []string{"rm-stream-tty:D:/huge_backup"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// 核心断言 1: 必须包含 TTY 原位刷新的已删项数提示
	if !strings.Contains(output, "items removed") {
		t.Fatalf("expected 'items removed' in TTY output, got:\n%s", output)
	}
	if !strings.Contains(output, "\rDeleting 'rm-stream-tty:D:/huge_backup'") {
		t.Fatalf("expected TTY carriage return prompt, got:\n%s", output)
	}

	// 核心断言 2: 必须包含最终完成信息
	if !strings.Contains(output, "[OK] Deleted 'rm-stream-tty:D:/huge_backup'") {
		t.Fatalf("expected success message, got:\n%s", output)
	}
}

// 验证在非 TTY 管道模式下，长耗时低频心跳能够携带已删项数汇报
func TestCmd_Rm_Streaming_NonTTY(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	client.SetDefaultHeartbeatInterval(25 * time.Millisecond)
	defer client.SetDefaultHeartbeatInterval(5 * time.Second)

	falseVal := false
	client.SetTerminalOverride(&falseVal)
	defer client.SetTerminalOverride(nil)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/rm") {
			rw.Header().Set("Content-Type", "application/x-ndjson")
			rw.WriteHeader(http.StatusOK)
			flusher, _ := rw.(http.Flusher)
			enc := json.NewEncoder(rw)

			_ = enc.Encode(protocol.FsRmEvent{Event: protocol.FsRmEventProgress, RemovedCount: 880})
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(35 * time.Millisecond)

			_ = enc.Encode(protocol.FsRmEvent{Event: protocol.FsRmEventDone, RemovedCount: 1200})
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "rm-stream-nontty", Target: u.Host})

	rmCmd.Flags().Set("recursive", "true")
	rmCmd.Flags().Set("yes", "true")
	defer func() {
		rmCmd.Flags().Set("recursive", "false")
		rmCmd.Flags().Set("yes", "false")
	}()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := rmCmd.RunE(rmCmd, []string{"rm-stream-nontty:D:/agent_backup"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// 核心断言 1: 非 TTY 管道模式必须包含心跳
	if !strings.Contains(output, "[cworker] Deleting 'rm-stream-nontty:D:/agent_backup'") {
		t.Fatalf("expected non-TTY heartbeat in output, got:\n%s", output)
	}

	// 核心断言 2: 必须输出成功完成标识
	if !strings.Contains(output, "[OK] Deleted 'rm-stream-nontty:D:/agent_backup'") {
		t.Fatalf("expected deleted success output, got:\n%s", output)
	}
}

