package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"cworker/pkg/protocol"
)

func TestCmd_Cp_SafeBaseName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"D:/folder/file.txt", "file.txt"},
		{"D:\\folder\\file.txt", "file.txt"},
		{"D:/folder/sub/", "sub"},
		{"D:\\folder\\sub\\", "sub"},
		{"file.txt", "file.txt"},
		{"D:", ""},
		{"D:/", ""},
		{".", ""},
		{"..", ""},
		{"/.", ""},
		{"/..", ""},
		{"D:/..", ""},
		{"D:/.", ""},
		{"", ""},
		{"/d/project/", "project"},
		{"/d/project", "project"},
		{"/d/", ""},
		{"/d", ""},
		{"/var/log/app.log", "app.log"},
		{"app.log", "app.log"},
	}

	for _, tc := range cases {
		if got := pathutil.SafeBaseName(tc.input); got != tc.expected {
			t.Errorf("safeBaseName(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestCmd_Cp_JoinRemote(t *testing.T) {
	if res := pathutil.JoinRemotePath("D:/base", "sub/file.txt"); res != "D:/base/sub/file.txt" {
		t.Fatalf("expected D:/base/sub/file.txt, got %s", res)
	}
	if res := pathutil.JoinRemotePath("D:\\base\\", "/sub/file.txt"); res != "D:/base/sub/file.txt" {
		t.Fatalf("expected D:/base/sub/file.txt, got %s", res)
	}
	if res := pathutil.JoinRemotePath("", "file.txt"); res != "file.txt" {
		t.Fatalf("expected file.txt, got %s", res)
	}
	if res := pathutil.JoinRemotePath("D:/base", ""); res != "D:/base" {
		t.Fatalf("expected D:/base, got %s", res)
	}
	if res := pathutil.JoinRemotePath("/", "file.txt"); res != "/file.txt" {
		t.Fatalf("expected /file.txt, got %s", res)
	}
}

func TestCmd_Cp_BothLocal(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "src.txt")
	dstFile := filepath.Join(tempDir, "dst.txt")
	content := []byte("hello local copy")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	// 1. 单文件本地拷贝
	if err := cpCmd.RunE(cpCmd, []string{srcFile, dstFile}); err != nil {
		t.Fatalf("local file copy failed: %v", err)
	}
	dstContent, err := os.ReadFile(dstFile)
	if err != nil || string(dstContent) != string(content) {
		t.Fatalf("local file content mismatch: %v, got %s", err, string(dstContent))
	}

	// 2. 目录递归本地拷贝 (-r)
	srcDir := filepath.Join(tempDir, "src_dir")
	dstDir := filepath.Join(tempDir, "dst_dir")
	_ = os.MkdirAll(filepath.Join(srcDir, "nested"), 0755)
	_ = os.WriteFile(filepath.Join(srcDir, "nested", "data.txt"), []byte("nested content"), 0644)

	cpCmd.Flags().Set("recursive", "true")
	defer cpCmd.Flags().Set("recursive", "false")

	if err := cpCmd.RunE(cpCmd, []string{srcDir, dstDir}); err != nil {
		t.Fatalf("local dir copy failed: %v", err)
	}
	nestedDst := filepath.Join(dstDir, "nested", "data.txt")
	nestedContent, err := os.ReadFile(nestedDst)
	if err != nil || string(nestedContent) != "nested content" {
		t.Fatalf("local dir nested content mismatch: %v, got %s", err, string(nestedContent))
	}

	// 3. 本地目录未传 -r 时应被拦截
	cpCmd.Flags().Set("recursive", "false")
	err = cpCmd.RunE(cpCmd, []string{srcDir, filepath.Join(tempDir, "dst2")})
	if err == nil || !strings.Contains(err.Error(), "omitting directory") {
		t.Fatalf("expected omitting directory error for local dir without -r, got: %v", err)
	}
}

func TestCmd_Cp_DirectoryWithoutRecursiveFlag(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "local_src")
	_ = os.MkdirAll(srcDir, 0755)

	cpCmd.Flags().Set("recursive", "false")

	err := cpCmd.RunE(cpCmd, []string{srcDir, "mock-node:D:/remote_target"})
	if err == nil {
		t.Fatal("expected error when copying directory without -r")
	}
	if !strings.Contains(err.Error(), "omitting directory") || !strings.Contains(err.Error(), "use -r") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func setupMockWorkerServer(t *testing.T) (*httptest.Server, *sync.Map, *sync.Map) {
	uploadedFiles := &sync.Map{}
	createdDirs := &sync.Map{}

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		pathParam := r.URL.Query().Get("path")
		switch r.URL.Path {
		case "/api/v1/fs/ls":
			if strings.Contains(pathParam, "remote_dir") || strings.Contains(pathParam, "dst_dir") {
				list := []protocol.FileInfo{
					{Name: "file1.txt", Path: "file1.txt", IsDir: false, Size: 11},
					{Name: "empty_sub", Path: "empty_sub", IsDir: true},
				}
				_ = json.NewEncoder(rw).Encode(list)
				return
			}
			// 模拟单文件
			http.Error(rw, "path is a file, not a directory", http.StatusBadRequest)
		case "/api/v1/fs/upload":
			h := sha256.New()
			body, _ := io.ReadAll(io.TeeReader(r.Body, h))
			hashHex := hex.EncodeToString(h.Sum(nil))
			uploadedFiles.Store(pathParam, string(body))
			rw.Header().Set("X-File-SHA256", hashHex)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("OK"))
		case "/api/v1/fs/download":
			content := "hello-from-remote"
			h := sha256.Sum256([]byte(content))
			hashHex := hex.EncodeToString(h[:])
			rw.Header().Set("X-File-Size", fmt.Sprintf("%d", len(content)))
			rw.Header().Set("Trailer", "X-File-SHA256")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(content))
			rw.Header().Set("X-File-SHA256", hashHex)
		case "/api/v1/fs/md":
			createdDirs.Store(pathParam, true)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("CREATED"))
		default:
			http.NotFound(rw, r)
		}
	}))

	return server, uploadedFiles, createdDirs
}

func TestCmd_Cp_ExecutionFlows(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	server, uploadedFiles, createdDirs := setupMockWorkerServer(t)
	defer server.Close()

	u, _ := url.Parse(server.URL)

	// 配置 mock-node 和 mock-dst 节点
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-node",
		Target: u.Host,
	})
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-dst",
		Target: u.Host,
	})

	// 1. 本地到远端单文件上传
	localSrcFile := filepath.Join(tempProfile, "single_upload.txt")
	_ = os.WriteFile(localSrcFile, []byte("single-file-upload-data"), 0644)

	cpCmd.Flags().Set("recursive", "false")
	cpCmd.Flags().Set("concurrency", "4")
	err := cpCmd.RunE(cpCmd, []string{localSrcFile, "mock-node:D:/remote_file.txt"})
	if err != nil {
		t.Fatalf("local to remote single file failed: %v", err)
	}
	if val, ok := uploadedFiles.Load("D:/remote_file.txt"); !ok || val != "single-file-upload-data" {
		t.Fatalf("uploaded file mismatch: %v", val)
	}

	// 2. 本地到远端文件夹上传 (-r)
	localDir := filepath.Join(tempProfile, "upload_dir")
	_ = os.MkdirAll(filepath.Join(localDir, "sub_empty"), 0755)
	_ = os.WriteFile(filepath.Join(localDir, "fileA.txt"), []byte("dataA"), 0644)

	cpCmd.Flags().Set("recursive", "true")
	cpCmd.Flags().Set("concurrency", "2")
	err = cpCmd.RunE(cpCmd, []string{localDir, "mock-node:D:/uploaded_target"})
	if err != nil {
		t.Fatalf("local to remote directory upload failed: %v", err)
	}
	if _, ok := createdDirs.Load("D:/uploaded_target/sub_empty"); !ok {
		t.Fatal("expected empty subdirectory created on remote")
	}
	if val, ok := uploadedFiles.Load("D:/uploaded_target/fileA.txt"); !ok || val != "dataA" {
		t.Fatalf("uploaded dir file mismatch: %v", val)
	}

	// 3. 远端到本地单文件下载
	localDstFile := filepath.Join(tempProfile, "downloaded.txt")
	cpCmd.Flags().Set("recursive", "false")
	err = cpCmd.RunE(cpCmd, []string{"mock-node:D:/remote_file.txt", localDstFile})
	if err != nil {
		t.Fatalf("remote to local single file download failed: %v", err)
	}
	downloadedData, err := os.ReadFile(localDstFile)
	if err != nil || string(downloadedData) != "hello-from-remote" {
		t.Fatalf("downloaded data mismatch: %s, err: %v", string(downloadedData), err)
	}

	// 4. 远端到本地文件夹下载 (-r)
	localDstDir := filepath.Join(tempProfile, "download_target")
	cpCmd.Flags().Set("recursive", "true")
	err = cpCmd.RunE(cpCmd, []string{"mock-node:D:/remote_dir", localDstDir})
	if err != nil {
		t.Fatalf("remote to local directory download failed: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(localDstDir, "empty_sub")); err != nil || !fi.IsDir() {
		t.Fatalf("expected empty_sub created locally: %v", err)
	}
	dlFileContent, err := os.ReadFile(filepath.Join(localDstDir, "file1.txt"))
	if err != nil || string(dlFileContent) != "hello-from-remote" {
		t.Fatalf("downloaded dir file content mismatch: %s, err: %v", string(dlFileContent), err)
	}

	// 5. 远端目录下载缺少 -r 标志应拦截
	cpCmd.Flags().Set("recursive", "false")
	err = cpCmd.RunE(cpCmd, []string{"mock-node:D:/remote_dir", localDstDir})
	if err == nil {
		t.Fatal("expected omitting directory error for remote dir without -r")
	}
	if !strings.Contains(err.Error(), "omitting directory") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 6. 远端到远端单文件拷贝
	err = cpCmd.RunE(cpCmd, []string{"mock-node:D:/remote_file.txt", "mock-dst:D:/relay_dest.txt"})
	if err != nil {
		t.Fatalf("remote to remote single file relay failed: %v", err)
	}
	if val, ok := uploadedFiles.Load("D:/relay_dest.txt"); !ok || val != "hello-from-remote" {
		t.Fatalf("relay dest file mismatch: %v", val)
	}

	// 7. 远端到远端文件夹中继 (-r)
	cpCmd.Flags().Set("recursive", "true")
	cpCmd.Flags().Set("concurrency", "4")
	err = cpCmd.RunE(cpCmd, []string{"mock-node:D:/remote_dir", "mock-dst:D:/relay_target_dir"})
	if err != nil {
		t.Fatalf("remote to remote directory relay failed: %v", err)
	}
	if _, ok := createdDirs.Load("D:/relay_target_dir/empty_sub"); !ok {
		t.Fatal("expected empty_sub created on relay destination")
	}
	if val, ok := uploadedFiles.Load("D:/relay_target_dir/file1.txt"); !ok || val != "hello-from-remote" {
		t.Fatalf("relay target dir file mismatch: %v", val)
	}
}

func TestCmd_Cp_DownloadFile_FailureDoesNotDestroyExistingFile(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	// 创建一个失败的远端下载服务器（500 错误）
	failServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		http.Error(rw, "internal worker error", http.StatusInternalServerError)
	}))
	defer failServer.Close()

	u, _ := url.Parse(failServer.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "fail-node",
		Target: u.Host,
	})

	localTarget := filepath.Join(tempProfile, "important_local_file.txt")
	originalContent := "original-local-valuable-content"
	if err := os.WriteFile(localTarget, []byte(originalContent), 0644); err != nil {
		t.Fatalf("write local target failed: %v", err)
	}

	// 触发单文件下载，由于远端报错，下载失败
	cpCmd.Flags().Set("recursive", "false")
	err := cpCmd.RunE(cpCmd, []string{"fail-node:D:/remote_broken.txt", localTarget})
	if err == nil {
		t.Fatal("expected download error from fail-node, got nil")
	}

	// 校验本地已有目标文件未被截断或删除，依然完好无损
	currentBytes, err := os.ReadFile(localTarget)
	if err != nil {
		t.Fatalf("local target file was deleted: %v", err)
	}
	if string(currentBytes) != originalContent {
		t.Fatalf("local file was modified or corrupted! expected %q, got %q", originalContent, string(currentBytes))
	}

	// 校验临时文件已被清理
	entries, err := os.ReadDir(tempProfile)
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".cwtemp-") || strings.Contains(e.Name(), ".cwsave-") {
			t.Fatalf("temporary download file was leaked: %s", e.Name())
		}
	}
}

// TestCmd_Cp_BulkDirectory_MoreThan8Files 验证通过 CLI cp -r 传输多于 8 个文件的大批量目录复制完整性
func TestCmd_Cp_BulkDirectory_MoreThan8Files(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	server, uploadedFiles, createdDirs := setupMockWorkerServer(t)
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-node",
		Target: u.Host,
	})

	srcDir := filepath.Join(tempProfile, "cli_bulk_src")
	dstDir := filepath.Join(tempProfile, "cli_bulk_dst")

	const fileCount = 20
	expectedContents := make(map[string]string)

	// 创建多层级子目录与 20 个文件
	_ = os.MkdirAll(filepath.Join(srcDir, "sub1", "nested"), 0755)
	_ = os.MkdirAll(filepath.Join(srcDir, "empty_dir"), 0755)

	for i := 1; i <= fileCount; i++ {
		var relPath string
		var content string
		if i <= 10 {
			relPath = fmt.Sprintf("sub1/nested/file_%02d.txt", i)
			content = fmt.Sprintf("content of file %d - %s", i, strings.Repeat("X", i*50))
		} else {
			relPath = fmt.Sprintf("file_%02d.txt", i)
			content = fmt.Sprintf("root content of file %d", i)
		}
		expectedContents[filepath.ToSlash(relPath)] = content
		fullPath := filepath.Join(srcDir, filepath.FromSlash(relPath))
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. 测试本地到本地大批量并发目录复制
	cpCmd.Flags().Set("recursive", "true")
	cpCmd.Flags().Set("concurrency", "4") // 限制并发为 4，强迫 20 个文件多轮复用池
	defer func() {
		cpCmd.Flags().Set("recursive", "false")
		cpCmd.Flags().Set("concurrency", "8")
	}()

	if err := cpCmd.RunE(cpCmd, []string{srcDir, dstDir}); err != nil {
		t.Fatalf("local bulk copy failed: %v", err)
	}

	// 验证本地 20 个文件全部存在且内容一致
	for relPath, expectedContent := range expectedContents {
		dstPath := filepath.Join(dstDir, filepath.FromSlash(relPath))
		data, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("destination file '%s' missing: %v", relPath, err)
		}
		if string(data) != expectedContent {
			t.Fatalf("content mismatch for '%s'", relPath)
		}
	}

	// 2. 测试本地上传到远端 mock worker (20 个文件)
	if err := cpCmd.RunE(cpCmd, []string{srcDir, "mock-node:D:/remote_bulk_upload"}); err != nil {
		t.Fatalf("remote bulk upload failed: %v", err)
	}

	// 验证远端收到的所有 20 个文件
	var remoteUploadedCount int
	for relPath, expectedContent := range expectedContents {
		remoteRel := pathutil.JoinRemotePath("D:/remote_bulk_upload", relPath)
		val, ok := uploadedFiles.Load(remoteRel)
		if !ok {
			t.Fatalf("file '%s' was not uploaded to remote worker", remoteRel)
		}
		if val.(string) != expectedContent {
			t.Fatalf("uploaded file '%s' content mismatch", remoteRel)
		}
		remoteUploadedCount++
	}
	if remoteUploadedCount != fileCount {
		t.Fatalf("expected %d uploaded files, got %d", fileCount, remoteUploadedCount)
	}

	// 验证空目录在远端也被创建
	remoteEmpty := pathutil.JoinRemotePath("D:/remote_bulk_upload", "empty_dir")
	if _, ok := createdDirs.Load(remoteEmpty); !ok {
		t.Fatalf("empty directory '%s' was not created on remote worker", remoteEmpty)
	}
}

// 验证通过 CLI cp -r 传输长任务目录时，在非 TTY 环境下定时输出单行进度心跳并在结束时输出单行摘要
func TestCmd_Cp_NonTTY_Heartbeat(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	client.SetDefaultHeartbeatInterval(25 * time.Millisecond)
	defer client.SetDefaultHeartbeatInterval(5 * time.Second)

	f := false
	client.SetTerminalOverride(&f)
	defer client.SetTerminalOverride(nil)

	srcDir := filepath.Join(tempProfile, "src_heartbeat")
	_ = os.MkdirAll(srcDir, 0755)

	// 创建几个小文件
	for i := 1; i <= 3; i++ {
		_ = os.WriteFile(filepath.Join(srcDir, fmt.Sprintf("file_%d.txt", i)), []byte(strings.Repeat("D", 500)), 0644)
	}

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// 模拟每处理一个文件消耗一定时间，确保跨越 25ms 心跳周期
		time.Sleep(30 * time.Millisecond)
		switch r.URL.Path {
		case "/api/v1/fs/upload":
			h := sha256.New()
			_, _ = io.Copy(h, r.Body)
			hashHex := hex.EncodeToString(h.Sum(nil))
			rw.Header().Set("X-File-SHA256", hashHex)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("OK"))
		case "/api/v1/fs/md":
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("OK"))
		default:
			http.NotFound(rw, r)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "hb-cp-node", Target: u.Host})

	cpCmd.Flags().Set("recursive", "true")
	cpCmd.Flags().Set("concurrency", "1") // 串行上传强迫时间跨越
	defer func() {
		cpCmd.Flags().Set("recursive", "false")
		cpCmd.Flags().Set("concurrency", "8")
	}()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cpCmd.RunE(cpCmd, []string{srcDir, "hb-cp-node:D:/uploaded_hb"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// 核心断言 1: 在非 TTY 管道中必须捕获到阶段性心跳 "[cworker] Transferred: "
	if !strings.Contains(output, "[cworker] Transferred:") {
		t.Fatalf("expected non-TTY heartbeat in cp output, got:\n%s", output)
	}

	// 核心断言 2: 必须捕获到最终完成摘要 "[cworker] Transferred 3/3 files"
	if !strings.Contains(output, "[cworker] Transferred 3/3 files") {
		t.Fatalf("expected final finish summary in cp output, got:\n%s", output)
	}

	// 核心断言 3: 必须包含最终成功标识
	if !strings.Contains(output, "[OK] Transfer completed successfully.") {
		t.Fatalf("expected success message in cp output, got:\n%s", output)
	}
}

// 验证当并发数参数传入 <= 0 (如 -j 0 或 -j -1) 时，自动回退至默认并发数 8 并正常传输
func TestCmd_Cp_DefaultConcurrencyFallback(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "f1.txt")
	f2 := filepath.Join(tempDir, "f2.txt")
	_ = os.WriteFile(f1, []byte("concurrency test"), 0644)

	cpCmd.Flags().Set("concurrency", "0")
	defer cpCmd.Flags().Set("concurrency", "8")

	err := cpCmd.RunE(cpCmd, []string{f1, f2})
	if err != nil {
		t.Fatalf("expected nil error when concurrency is 0, got: %v", err)
	}
	content, err := os.ReadFile(f2)
	if err != nil || string(content) != "concurrency test" {
		t.Fatalf("unexpected content or error: %v, got %s", err, string(content))
	}
}

// 验证通过 cw cp 传输大文件 (>10MB) 时，流式管道、CountingReader/CountingWriter 与哈希核验全链路无损
func TestCmd_Cp_LargeFile_Streaming(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "large_src.dat")
	dstFile := filepath.Join(tempDir, "large_dst.dat")

	// 构造 12MB 的大文件
	targetSize := 12 * 1024 * 1024
	chunk := bytes.Repeat([]byte("M"), 1024*1024)
	f, err := os.Create(srcFile)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		_, _ = f.Write(chunk)
	}
	f.Close()

	cpCmd.Flags().Set("recursive", "false")
	cpCmd.Flags().Set("concurrency", "4")
	defer cpCmd.Flags().Set("concurrency", "8")

	err = cpCmd.RunE(cpCmd, []string{srcFile, dstFile})
	if err != nil {
		t.Fatalf("cp command on large file failed: %v", err)
	}

	fi, err := os.Stat(dstFile)
	if err != nil {
		t.Fatalf("stat dstFile failed: %v", err)
	}
	if fi.Size() != int64(targetSize) {
		t.Fatalf("expected dstFile size %d, got %d", targetSize, fi.Size())
	}
}


