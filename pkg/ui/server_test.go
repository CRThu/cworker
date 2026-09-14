package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
	"cworker/pkg/worker"
	"nhooyr.io/websocket"
)

// setupTestEnv 建立隔离的测试数据目录与客户端
func setupTestEnv(t *testing.T) (*Server, string, func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "cworker-ui-test-*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}

	t.Setenv("USERPROFILE", tempDir)
	t.Setenv("HOME", tempDir)

	cli := client.NewClient()
	srv := NewServer(Config{
		BindAddr: "127.0.0.1",
		Port:     0,
		Client:   cli,
	})

	cleanup := func() {
		_ = srv.Close()
		_ = os.RemoveAll(tempDir)
	}

	return srv, tempDir, cleanup
}

func TestServer_StaticAndSPAFallback(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()

	handler := srv.Handler()

	tests := []struct {
		name       string
		path       string
		method     string
		wantStatus int
		contains   string
	}{
		{"root index.html", "/", http.MethodGet, http.StatusOK, "cworker"},
		{"static favicon.svg", "/favicon.svg", http.MethodGet, http.StatusOK, "<svg"},
		{"spa fallback route", "/jobs/running", http.MethodGet, http.StatusOK, "cworker"},
		{"api 404 check", "/api/not-exist", http.MethodGet, http.StatusNotFound, "404 page not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, w.Code)
			}
			if !strings.Contains(w.Body.String(), tt.contains) {
				t.Fatalf("response does not contain expected substring %q", tt.contains)
			}
		})
	}
}

func TestServer_ServerLifecycleAndURL(t *testing.T) {
	srv := NewServer(Config{})
	if srv.cfg.Port != 19001 || srv.cfg.BindAddr != "127.0.0.1" {
		t.Fatalf("unexpected defaults: %+v", srv.cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	// 使用端口 -1 显式触发系统随机分配空闲端口 (ephemeral port)
	testSrv := NewServer(Config{Port: -1, BindAddr: "127.0.0.1"})
	go func() {
		errCh <- testSrv.Start(ctx)
	}()

	// 轮询等待监听就绪
	var addr string
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		addr = testSrv.Addr()
		if addr != "" {
			break
		}
	}
	if addr == "" {
		t.Fatal("server did not bind address in time")
	}

	if !strings.HasPrefix(testSrv.URL(), "http://127.0.0.1:") {
		t.Fatalf("unexpected server url: %s", testSrv.URL())
	}

	cancel()
	err := <-errCh
	if err != nil && err != context.Canceled {
		t.Fatalf("server start returned unexpected error: %v", err)
	}
}

func TestServer_HandleOverview(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. Method Not Allowed
	reqPost := httptest.NewRequest(http.MethodPost, "/api/ui/overview", nil)
	wPost := httptest.NewRecorder()
	handler.ServeHTTP(wPost, reqPost)
	if wPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wPost.Code)
	}

	// 2. GET Overview
	reqGet := httptest.NewRequest(http.MethodGet, "/api/ui/overview", nil)
	wGet := httptest.NewRecorder()
	handler.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wGet.Code, wGet.Body.String())
	}

	var data OverviewData
	if err := json.Unmarshal(wGet.Body.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal overview failed: %v", err)
	}
	if data.LocalCard.IP != "127.0.0.1" || data.LocalCard.Port != protocol.DefaultPort {
		t.Fatalf("unexpected local card: %+v", data.LocalCard)
	}
}

func TestServer_HandleNodes(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. GET (初始状态)
	reqGet := httptest.NewRequest(http.MethodGet, "/api/ui/nodes", nil)
	wGet := httptest.NewRecorder()
	handler.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wGet.Code)
	}

	// 2. POST 校验失败
	reqBadPost := httptest.NewRequest(http.MethodPost, "/api/ui/nodes", strings.NewReader(`{"name":""}`))
	wBadPost := httptest.NewRecorder()
	handler.ServeHTTP(wBadPost, reqBadPost)
	if wBadPost.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name, got %d", wBadPost.Code)
	}

	// 3. POST 成功添加
	validNodeJSON := `{"name":"test-worker-1","target":"192.168.1.50","token":"abc123token"}`
	reqValidPost := httptest.NewRequest(http.MethodPost, "/api/ui/nodes", strings.NewReader(validNodeJSON))
	wValidPost := httptest.NewRecorder()
	handler.ServeHTTP(wValidPost, reqValidPost)
	if wValidPost.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wValidPost.Code, wValidPost.Body.String())
	}

	// 4. 再次 GET 验证已添加
	wGet2 := httptest.NewRecorder()
	handler.ServeHTTP(wGet2, reqGet)
	var respData map[string]interface{}
	_ = json.Unmarshal(wGet2.Body.Bytes(), &respData)
	knownList := respData["known"].([]interface{})
	if len(knownList) != 1 {
		t.Fatalf("expected 1 known node, got %d", len(knownList))
	}

	// 5. DELETE 缺少参数
	reqBadDel := httptest.NewRequest(http.MethodDelete, "/api/ui/nodes", nil)
	wBadDel := httptest.NewRecorder()
	handler.ServeHTTP(wBadDel, reqBadDel)
	if wBadDel.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d", wBadDel.Code)
	}

	// 6. DELETE 正常移除
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/ui/nodes?name=test-worker-1", nil)
	wDel := httptest.NewRecorder()
	handler.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wDel.Code)
	}

	// 7. Method Not Allowed
	reqPut := httptest.NewRequest(http.MethodPut, "/api/ui/nodes", nil)
	wPut := httptest.NewRecorder()
	handler.ServeHTTP(wPut, reqPut)
	if wPut.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wPut.Code)
	}
}

func TestServer_HandleJobs(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. Method Not Allowed
	reqPost := httptest.NewRequest(http.MethodPost, "/api/ui/jobs", nil)
	wPost := httptest.NewRecorder()
	handler.ServeHTTP(wPost, reqPost)
	if wPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wPost.Code)
	}

	// 2. GET (无运行任务时返回空列表)
	reqGet := httptest.NewRequest(http.MethodGet, "/api/ui/jobs?status=RUNNING&search=notfound", nil)
	wGet := httptest.NewRecorder()
	handler.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wGet.Code)
	}
	if !strings.Contains(wGet.Body.String(), "[]") {
		t.Fatalf("expected empty array, got %s", wGet.Body.String())
	}
}

func TestServer_HandleRunJob_Validation(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. 405 Method Not Allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/api/ui/jobs/run", nil)
	wGet := httptest.NewRecorder()
	handler.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wGet.Code)
	}

	// 2. 400 Bad JSON
	reqBadJSON := httptest.NewRequest(http.MethodPost, "/api/ui/jobs/run", strings.NewReader("{invalid"))
	wBadJSON := httptest.NewRecorder()
	handler.ServeHTTP(wBadJSON, reqBadJSON)
	if wBadJSON.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", wBadJSON.Code)
	}

	// 3. 400 Empty Command
	reqEmptyCmd := httptest.NewRequest(http.MethodPost, "/api/ui/jobs/run", strings.NewReader(`{"command":""}`))
	wEmptyCmd := httptest.NewRecorder()
	handler.ServeHTTP(wEmptyCmd, reqEmptyCmd)
	if wEmptyCmd.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty command, got %d", wEmptyCmd.Code)
	}
}

func TestServer_HandleKillJob_Validation(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. 405
	reqGet := httptest.NewRequest(http.MethodGet, "/api/ui/jobs/kill", nil)
	wGet := httptest.NewRecorder()
	handler.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wGet.Code)
	}

	// 2. 400 Empty Job ID
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/ui/jobs/kill", strings.NewReader(`{"job_id":""}`))
	wEmpty := httptest.NewRecorder()
	handler.ServeHTTP(wEmpty, reqEmpty)
	if wEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", wEmpty.Code)
	}
}

func TestServer_HandleCleanJobs_Validation(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. 405
	reqGet := httptest.NewRequest(http.MethodGet, "/api/ui/jobs/clean", nil)
	wGet := httptest.NewRecorder()
	handler.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wGet.Code)
	}

	// 2. 400
	reqBad := httptest.NewRequest(http.MethodPost, "/api/ui/jobs/clean", strings.NewReader("{invalid"))
	wBad := httptest.NewRecorder()
	handler.ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", wBad.Code)
	}
}

func TestServer_HandleStreamLogs_Validation(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/ui/jobs/stream", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing job_id, got %d", w.Code)
	}
}

func TestServer_HandleGetJobLogs_Validation(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. Method Not Allowed
	reqPost := httptest.NewRequest(http.MethodPost, "/api/ui/jobs/logs", nil)
	wPost := httptest.NewRecorder()
	handler.ServeHTTP(wPost, reqPost)
	if wPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wPost.Code)
	}

	// 2. Missing job_id
	reqNoID := httptest.NewRequest(http.MethodGet, "/api/ui/jobs/logs", nil)
	wNoID := httptest.NewRecorder()
	handler.ServeHTTP(wNoID, reqNoID)
	if wNoID.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing job_id, got %d", wNoID.Code)
	}

	// 3. Job not found
	reqNotFound := httptest.NewRequest(http.MethodGet, "/api/ui/jobs/logs?job_id=not-exist-job", nil)
	wNotFound := httptest.NewRecorder()
	handler.ServeHTTP(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for non-existent job, got %d", wNotFound.Code)
	}
}

func TestServer_HandleFsList_Local(t *testing.T) {
	srv, tempDir, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. 405 Method Not Allowed
	reqPost := httptest.NewRequest(http.MethodPost, "/api/ui/fs/ls", nil)
	wPost := httptest.NewRecorder()
	handler.ServeHTTP(wPost, reqPost)
	if wPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wPost.Code)
	}

	// 2. 创建临时测试文件
	testFile := filepath.Join(tempDir, "sample.txt")
	_ = os.WriteFile(testFile, []byte("hello"), 0644)

	// 3. GET 列出目录
	req := httptest.NewRequest(http.MethodGet, "/api/ui/fs/ls?path="+tempDir, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "sample.txt") {
		t.Fatalf("expected to find sample.txt in %s", w.Body.String())
	}
}

func TestServer_HandleFsUploadAndDownload_Local(t *testing.T) {
	srv, tempDir, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	uploadDst := filepath.Join(tempDir, "upload_test.txt")

	// 1. 上传校验：缺少 path 参数
	reqNoPath := httptest.NewRequest(http.MethodPost, "/api/ui/fs/upload", strings.NewReader("content"))
	wNoPath := httptest.NewRecorder()
	handler.ServeHTTP(wNoPath, reqNoPath)
	if wNoPath.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing path, got %d", wNoPath.Code)
	}

	// 2. 正常上传
	reqUpload := httptest.NewRequest(http.MethodPost, "/api/ui/fs/upload?path="+uploadDst, strings.NewReader("uploaded content 12345"))
	wUpload := httptest.NewRecorder()
	handler.ServeHTTP(wUpload, reqUpload)
	if wUpload.Code != http.StatusOK {
		t.Fatalf("expected 200 for upload, got %d: %s", wUpload.Code, wUpload.Body.String())
	}

	// 验证落盘
	savedBytes, err := os.ReadFile(uploadDst)
	if err != nil || string(savedBytes) != "uploaded content 12345" {
		t.Fatalf("saved content mismatch: %s, err: %v", string(savedBytes), err)
	}

	// 3. 下载校验：缺少 path 参数
	reqNoDl := httptest.NewRequest(http.MethodGet, "/api/ui/fs/download", nil)
	wNoDl := httptest.NewRecorder()
	handler.ServeHTTP(wNoDl, reqNoDl)
	if wNoDl.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing download path, got %d", wNoDl.Code)
	}

	// 4. 正常下载
	reqDl := httptest.NewRequest(http.MethodGet, "/api/ui/fs/download?path="+uploadDst, nil)
	wDl := httptest.NewRecorder()
	handler.ServeHTTP(wDl, reqDl)
	if wDl.Code != http.StatusOK {
		t.Fatalf("expected 200 for download, got %d", wDl.Code)
	}
	if wDl.Body.String() != "uploaded content 12345" {
		t.Fatalf("downloaded content mismatch: %s", wDl.Body.String())
	}
}

func TestServer_HandleFsTransfer_LocalToLocal(t *testing.T) {
	srv, tempDir, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	srcFile := filepath.Join(tempDir, "transfer_src.txt")
	dstFile := filepath.Join(tempDir, "transfer_dst.txt")
	_ = os.WriteFile(srcFile, []byte("transfer data direct"), 0644)

	// 1. 405 Method Not Allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/api/ui/fs/transfer", nil)
	wGet := httptest.NewRecorder()
	handler.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wGet.Code)
	}

	// 2. 本地单文件传输
	payload := fmt.Sprintf(`{"src_path":%q,"dst_path":%q}`, srcFile, dstFile)
	reqTransfer := httptest.NewRequest(http.MethodPost, "/api/ui/fs/transfer", strings.NewReader(payload))
	wTransfer := httptest.NewRecorder()
	handler.ServeHTTP(wTransfer, reqTransfer)
	if wTransfer.Code != http.StatusOK {
		t.Fatalf("expected 200 for transfer, got %d: %s", wTransfer.Code, wTransfer.Body.String())
	}

	copiedBytes, err := os.ReadFile(dstFile)
	if err != nil || string(copiedBytes) != "transfer data direct" {
		t.Fatalf("transfer file content mismatch: %s, err: %v", string(copiedBytes), err)
	}

	// 3. 本地目录传输 (带 recursive)
	srcDir := filepath.Join(tempDir, "test_dir_src")
	dstDir := filepath.Join(tempDir, "test_dir_dst")
	_ = os.MkdirAll(srcDir, 0755)
	_ = os.WriteFile(filepath.Join(srcDir, "sub.txt"), []byte("sub-file"), 0644)

	// 未带 recursive: 应当返回 400
	badDirPayload := fmt.Sprintf(`{"src_path":%q,"dst_path":%q,"recursive":false}`, srcDir, dstDir)
	reqBadDir := httptest.NewRequest(http.MethodPost, "/api/ui/fs/transfer", strings.NewReader(badDirPayload))
	wBadDir := httptest.NewRecorder()
	handler.ServeHTTP(wBadDir, reqBadDir)
	if wBadDir.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for dir transfer without -r, got %d", wBadDir.Code)
	}

	// 带 recursive: 应当成功拷贝
	goodDirPayload := fmt.Sprintf(`{"src_path":%q,"dst_path":%q,"recursive":true}`, srcDir, dstDir)
	reqGoodDir := httptest.NewRequest(http.MethodPost, "/api/ui/fs/transfer", strings.NewReader(goodDirPayload))
	wGoodDir := httptest.NewRecorder()
	handler.ServeHTTP(wGoodDir, reqGoodDir)
	if wGoodDir.Code != http.StatusOK {
		t.Fatalf("expected 200 for recursive dir transfer, got %d: %s", wGoodDir.Code, wGoodDir.Body.String())
	}
}

// TestServer_HandleFsTransfer_StreamingNDJSON 测试 application/x-ndjson 流式进度反馈与多文件状态
func TestServer_HandleFsTransfer_StreamingNDJSON(t *testing.T) {
	srv, tempDir, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	srcDir := filepath.Join(tempDir, "stream_src")
	dstDir := filepath.Join(tempDir, "stream_dst")
	_ = os.MkdirAll(srcDir, 0755)
	_ = os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("file-1-data-stream"), 0644)
	_ = os.WriteFile(filepath.Join(srcDir, "file2.txt"), []byte("file-2-data-stream"), 0644)

	// 1. 测试目录传输流式反馈 (?stream=true)
	payload := fmt.Sprintf(`{"src_path":%q,"dst_path":%q,"recursive":true}`, srcDir, dstDir)
	req := httptest.NewRequest(http.MethodPost, "/api/ui/fs/transfer?stream=true", strings.NewReader(payload))
	req.Header.Set("Accept", "application/x-ndjson")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for streaming transfer, got %d: %s", w.Code, w.Body.String())
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/x-ndjson") {
		t.Fatalf("expected Content-Type application/x-ndjson, got %q", contentType)
	}

	// 验证按行包含 NDJSON 帧
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) == 0 {
		t.Fatal("expected at least one NDJSON frame")
	}

	var hasDone bool
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("invalid json line %q: %v", l, err)
		}
		if m["type"] == "done" {
			hasDone = true
		}
	}
	if !hasDone {
		t.Fatalf("expected done frame in stream output, got: %s", w.Body.String())
	}

	// 2. 测试流式模式下错误处理帧
	badPayload := fmt.Sprintf(`{"src_path":%q,"dst_path":%q,"recursive":false}`, srcDir, dstDir)
	reqErr := httptest.NewRequest(http.MethodPost, "/api/ui/fs/transfer?stream=true", strings.NewReader(badPayload))
	wErr := httptest.NewRecorder()
	handler.ServeHTTP(wErr, reqErr)

	if wErr.Code != http.StatusOK {
		t.Fatalf("expected 200 stream container for stream error, got %d", wErr.Code)
	}
	errLines := strings.Split(strings.TrimSpace(wErr.Body.String()), "\n")
	var hasErrorFrame bool
	for _, l := range errLines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err == nil {
			if m["type"] == "error" && m["error"] != "" {
				hasErrorFrame = true
			}
		}
	}
	if !hasErrorFrame {
		t.Fatalf("expected error frame in stream output, got: %s", wErr.Body.String())
	}
}

// TestServer_HandleFsTransfer_StreamingNDJSON_BulkFiles 验证超过 8 个并发槽位（24 个文件）时 UI 流式推送的准确性与全部落盘
func TestServer_HandleFsTransfer_StreamingNDJSON_BulkFiles(t *testing.T) {
	srv, tempDir, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	srcDir := filepath.Join(tempDir, "bulk_stream_src")
	dstDir := filepath.Join(tempDir, "bulk_stream_dst")
	const totalFiles = 24

	expectedContents := make(map[string]string)
	for i := 1; i <= totalFiles; i++ {
		var relPath string
		var content string
		if i <= 8 {
			relPath = fmt.Sprintf("root_file_%02d.txt", i)
			content = fmt.Sprintf("root file content %d", i)
		} else if i <= 16 {
			relPath = fmt.Sprintf("sub/nested/file_%02d.bin", i)
			content = strings.Repeat(fmt.Sprintf("DATA-%02d-", i), 200)
		} else {
			relPath = fmt.Sprintf("deep/level2/level3/chunk_%02d.txt", i)
			content = fmt.Sprintf("deep content %d", i)
		}
		fullSrc := filepath.Join(srcDir, filepath.FromSlash(relPath))
		_ = os.MkdirAll(filepath.Dir(fullSrc), 0755)
		if err := os.WriteFile(fullSrc, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		expectedContents[filepath.ToSlash(relPath)] = content
	}

	payload := fmt.Sprintf(`{"src_path":%q,"dst_path":%q,"recursive":true}`, srcDir, dstDir)
	req := httptest.NewRequest(http.MethodPost, "/api/ui/fs/transfer?stream=true", strings.NewReader(payload))
	req.Header.Set("Accept", "application/x-ndjson")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for bulk streaming transfer, got %d: %s", w.Code, w.Body.String())
	}

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) == 0 {
		t.Fatal("expected NDJSON output")
	}

	var hasDone bool
	var maxCompleted int
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("invalid json line %s: %v", l, err)
		}
		if m["type"] == "progress" {
			if tf, ok := m["total_files"].(float64); ok && int(tf) != totalFiles {
				t.Fatalf("expected total_files %d, got %v", totalFiles, tf)
			}
			if cf, ok := m["completed_files"].(float64); ok {
				if int(cf) > maxCompleted {
					maxCompleted = int(cf)
				}
			}
		} else if m["type"] == "done" {
			hasDone = true
			if pct, ok := m["percent"].(float64); !ok || int(pct) != 100 {
				t.Fatalf("expected done frame percent 100, got %v", m["percent"])
			}
		}
	}

	if !hasDone {
		t.Fatalf("expected done frame, output: %s", w.Body.String())
	}
	if maxCompleted != totalFiles {
		t.Fatalf("expected progress completed_files to reach %d, reached %d", totalFiles, maxCompleted)
	}

	// 验证 24 个文件全部物理落盘且内容完全一致
	for relPath, expectedContent := range expectedContents {
		fullDst := filepath.Join(dstDir, filepath.FromSlash(relPath))
		data, err := os.ReadFile(fullDst)
		if err != nil {
			t.Fatalf("file '%s' not copied to dst: %v", relPath, err)
		}
		if string(data) != expectedContent {
			t.Fatalf("content mismatch for '%s'", relPath)
		}
	}
}



// TestServer_WorkerIntegration 真实拉起 Worker 节点进行全链路集成测试 (Run -> PS -> Stream -> Kill -> Clean)
func TestServer_WorkerIntegration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cworker-integ-*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("USERPROFILE", tempDir)
	t.Setenv("HOME", tempDir)

	// 1. 启动测试 Worker (监听本地 127.0.0.1 动态可用端口，严防冲突)
	wCtx, wCancel := context.WithCancel(context.Background())
	defer wCancel()

	freeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free port failed: %v", err)
	}
	freePort := freeListener.Addr().(*net.TCPAddr).Port
	_ = freeListener.Close()

	workerNode, err := worker.NewWorker(worker.Config{
		Name:     "mock-worker",
		BindAddr: "127.0.0.1",
		Port:     freePort,
		DataDir:  tempDir,
		Token:    "test-integration-token",
	})
	if err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	go func() {
		_ = workerNode.Start(wCtx)
	}()

	// 等待 Worker 端口就绪
	var workerPort int
	for i := 0; i < 30; i++ {
		time.Sleep(50 * time.Millisecond)
		if workerNode.Port() != 0 {
			workerPort = workerNode.Port()
			break
		}
	}
	if workerPort == 0 {
		t.Fatal("worker did not bind port in time")
	}

	// 2. 启动控制台 Server (基于真实的 Client 绑定账本)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-worker",
		Target: fmt.Sprintf("127.0.0.1:%d", workerPort),
		Token:  "test-integration-token",
	})

	uiServer := httptest.NewServer(NewServer(Config{
		Client: cli,
	}).Handler())
	defer uiServer.Close()

	clientHTTP := &http.Client{Timeout: 10 * time.Second}

	// 3. 验证 Overview 识别到该 Worker
	respOverview, err := clientHTTP.Get(uiServer.URL + "/api/ui/overview")
	if err != nil || respOverview.StatusCode != http.StatusOK {
		t.Fatalf("get overview failed: %v", err)
	}
	var ovData OverviewData
	_ = json.NewDecoder(respOverview.Body).Decode(&ovData)
	respOverview.Body.Close()
	if ovData.OnlineCount != 1 {
		t.Fatalf("expected 1 online worker in overview, got %d", ovData.OnlineCount)
	}

	// 4. 派发两个任务 (一个快速完成，一个运行中)
	runPayload1, _ := json.Marshal(map[string]string{
		"node":    "mock-worker",
		"command": "cmd /c echo stream test output line",
		"name":    "quick-echo-job",
	})
	respRun1, err := clientHTTP.Post(uiServer.URL+"/api/ui/jobs/run", "application/json", bytes.NewReader(runPayload1))
	if err != nil || respRun1.StatusCode != http.StatusOK {
		t.Fatalf("run job 1 failed: %v", err)
	}
	var jobInfo1 protocol.JobInfo
	_ = json.NewDecoder(respRun1.Body).Decode(&jobInfo1)
	respRun1.Body.Close()

	runPayload2, _ := json.Marshal(map[string]string{
		"node":    "mock-worker",
		"command": "cmd /c ping 127.0.0.1 -n 6",
		"name":    "long-running-job",
	})
	respRun2, err := clientHTTP.Post(uiServer.URL+"/api/ui/jobs/run", "application/json", bytes.NewReader(runPayload2))
	if err != nil || respRun2.StatusCode != http.StatusOK {
		t.Fatalf("run job 2 failed: %v", err)
	}
	var jobInfo2 protocol.JobInfo
	_ = json.NewDecoder(respRun2.Body).Decode(&jobInfo2)
	respRun2.Body.Close()

	// 等待 200ms 让 job 1 执行完毕，job 2 仍在运行
	time.Sleep(200 * time.Millisecond)

	// 5. 查询任务列表与全方位过滤 (GET /api/ui/jobs)
	// 5.1 全量查询 (验证排序：RUNNING 置顶)
	respJobs, err := clientHTTP.Get(uiServer.URL + "/api/ui/jobs")
	if err != nil || respJobs.StatusCode != http.StatusOK {
		t.Fatalf("list jobs failed: %v", err)
	}
	var jobsList []protocol.JobInfo
	_ = json.NewDecoder(respJobs.Body).Decode(&jobsList)
	respJobs.Body.Close()
	if len(jobsList) < 2 {
		t.Fatalf("expected at least 2 jobs, got %d", len(jobsList))
	}

	// 5.2 按状态过滤
	respRunning, _ := clientHTTP.Get(uiServer.URL + "/api/ui/jobs?status=RUNNING")
	var runningList []protocol.JobInfo
	_ = json.NewDecoder(respRunning.Body).Decode(&runningList)
	respRunning.Body.Close()

	// 5.3 按关键词搜索 (ID / 名称 / 命令)
	respSearchName, _ := clientHTTP.Get(uiServer.URL + "/api/ui/jobs?search=long-running")
	var searchNameList []protocol.JobInfo
	_ = json.NewDecoder(respSearchName.Body).Decode(&searchNameList)
	respSearchName.Body.Close()
	if len(searchNameList) == 0 {
		t.Fatal("expected search by name to match job 2")
	}

	respSearchCmd, _ := clientHTTP.Get(uiServer.URL + "/api/ui/jobs?search=ping")
	var searchCmdList []protocol.JobInfo
	_ = json.NewDecoder(respSearchCmd.Body).Decode(&searchCmdList)
	respSearchCmd.Body.Close()
	if len(searchCmdList) == 0 {
		t.Fatal("expected search by command to match job 2")
	}

	respSearchID, _ := clientHTTP.Get(uiServer.URL + "/api/ui/jobs?search=" + jobInfo1.ID)
	var searchIDList []protocol.JobInfo
	_ = json.NewDecoder(respSearchID.Body).Decode(&searchIDList)
	respSearchID.Body.Close()
	if len(searchIDList) == 0 {
		t.Fatal("expected search by ID to match job 1")
	}

	// 6. WebSocket 实时日志推流桥接测试 (GET /api/ui/jobs/stream)
	wsURL := strings.Replace(uiServer.URL, "http://", "ws://", 1) + "/api/ui/jobs/stream?job_id=" + jobInfo1.ID
	wsCtx, wsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer wsCancel()

	conn, _, err := websocket.Dial(wsCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial ui stream failed: %v", err)
	}

	var logAccumulator strings.Builder
	for {
		_, msg, err := conn.Read(wsCtx)
		if err != nil {
			break
		}
		logAccumulator.Write(msg)
		if strings.Contains(logAccumulator.String(), "stream test output line") {
			break
		}
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")

	// 6.2 REST 历史日志拉取测试 (GET /api/ui/jobs/logs)
	respLogs, err := clientHTTP.Get(uiServer.URL + "/api/ui/jobs/logs?job_id=" + jobInfo1.ID + "&lines=100")
	if err != nil {
		t.Fatalf("get logs failed: %v", err)
	}
	logsBytes, _ := io.ReadAll(respLogs.Body)
	respLogs.Body.Close()
	if respLogs.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for logs, got %d: %s", respLogs.StatusCode, string(logsBytes))
	}
	if !strings.Contains(string(logsBytes), "stream test output line") {
		t.Fatalf("expected logs to contain output, got: %s", string(logsBytes))
	}

	// 7. 终止运行中任务测试 (POST /api/ui/jobs/kill)
	killPayload, _ := json.Marshal(map[string]string{
		"job_id": jobInfo2.ID,
	})
	respKill, err := clientHTTP.Post(uiServer.URL+"/api/ui/jobs/kill", "application/json", bytes.NewReader(killPayload))
	if err != nil || respKill.StatusCode != http.StatusOK {
		t.Fatalf("kill job failed: %v", err)
	}
	respKill.Body.Close()

	// 8. 远端文件系统操作测试 (Upload -> List -> Download)
	remoteFileDst := filepath.Join(tempDir, "remote_test_file.txt")
	uploadResp, err := clientHTTP.Post(fmt.Sprintf("%s/api/ui/fs/upload?node=mock-worker&path=%s", uiServer.URL, remoteFileDst), "text/plain", strings.NewReader("remote file content 999"))
	if err != nil || uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("remote upload failed: %v", err)
	}
	uploadResp.Body.Close()

	// 远端文件列表
	lsResp, err := clientHTTP.Get(fmt.Sprintf("%s/api/ui/fs/ls?node=mock-worker&path=%s", uiServer.URL, tempDir))
	if err != nil || lsResp.StatusCode != http.StatusOK {
		t.Fatalf("remote ls failed: %v", err)
	}
	lsBytes, _ := io.ReadAll(lsResp.Body)
	lsResp.Body.Close()
	if !strings.Contains(string(lsBytes), "remote_test_file.txt") {
		t.Fatalf("expected remote file in ls: %s", string(lsBytes))
	}

	// 远端下载
	dlResp, err := clientHTTP.Get(fmt.Sprintf("%s/api/ui/fs/download?node=mock-worker&path=%s", uiServer.URL, remoteFileDst))
	if err != nil || dlResp.StatusCode != http.StatusOK {
		t.Fatalf("remote download failed: %v", err)
	}
	dlContent, _ := io.ReadAll(dlResp.Body)
	dlResp.Body.Close()
	if string(dlContent) != "remote file content 999" {
		t.Fatalf("downloaded content mismatch: %s", string(dlContent))
	}

	// 9. 跨机互传测试：单文件与目录互传 (Local <-> Remote, Remote <-> Remote)
	localFile := filepath.Join(tempDir, "local_to_upload.txt")
	_ = os.WriteFile(localFile, []byte("local to remote data"), 0644)
	remoteUploadDst := filepath.Join(tempDir, "transferred_remote.txt")

	// 9.1 单文件 Local -> Remote
	transferUpPayload, _ := json.Marshal(map[string]interface{}{
		"src_node": "",
		"src_path": localFile,
		"dst_node": "mock-worker",
		"dst_path": remoteUploadDst,
	})
	respTransUp, err := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(transferUpPayload))
	if err != nil || respTransUp.StatusCode != http.StatusOK {
		t.Fatalf("local to remote transfer failed: %v", err)
	}
	respTransUp.Body.Close()

	// 9.2 目录 Local -> Remote (测试 recursive: false 触发 400，以及 recursive: true 成功)
	localDirSrc := filepath.Join(tempDir, "local_dir_upload")
	_ = os.MkdirAll(localDirSrc, 0755)
	_ = os.WriteFile(filepath.Join(localDirSrc, "f1.txt"), []byte("dir-file-1"), 0644)
	remoteDirDst := filepath.Join(tempDir, "remote_dir_recv")

	badDirUp, _ := json.Marshal(map[string]interface{}{
		"src_node":  "",
		"src_path":  localDirSrc,
		"dst_node":  "mock-worker",
		"dst_path":  remoteDirDst,
		"recursive": false,
	})
	respBadDirUp, _ := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(badDirUp))
	if respBadDirUp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for dir upload without recursive, got %d", respBadDirUp.StatusCode)
	}

	goodDirUp, _ := json.Marshal(map[string]interface{}{
		"src_node":  "",
		"src_path":  localDirSrc,
		"dst_node":  "mock-worker",
		"dst_path":  remoteDirDst,
		"recursive": true,
	})
	respGoodDirUp, err := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(goodDirUp))
	if err != nil || respGoodDirUp.StatusCode != http.StatusOK {
		t.Fatalf("recursive dir upload failed: %v", err)
	}
	respGoodDirUp.Body.Close()

	// 9.3 目录 Remote -> Local (测试 recursive: false 触发 400，以及 recursive: true 成功)
	localDirRecv := filepath.Join(tempDir, "local_dir_from_remote")
	badDirDown, _ := json.Marshal(map[string]interface{}{
		"src_node":  "mock-worker",
		"src_path":  remoteDirDst,
		"dst_node":  "",
		"dst_path":  localDirRecv,
		"recursive": false,
	})
	respBadDirDown, _ := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(badDirDown))
	if respBadDirDown.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for dir download without recursive, got %d", respBadDirDown.StatusCode)
	}

	goodDirDown, _ := json.Marshal(map[string]interface{}{
		"src_node":  "mock-worker",
		"src_path":  remoteDirDst,
		"dst_node":  "",
		"dst_path":  localDirRecv,
		"recursive": true,
	})
	respGoodDirDown, err := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(goodDirDown))
	if err != nil || respGoodDirDown.StatusCode != http.StatusOK {
		t.Fatalf("recursive dir download failed: %v", err)
	}
	respGoodDirDown.Body.Close()

	// 9.4 单文件 Remote -> Local
	localDownloadedDst := filepath.Join(tempDir, "transferred_back_local.txt")
	transferDownPayload, _ := json.Marshal(map[string]interface{}{
		"src_node": "mock-worker",
		"src_path": remoteUploadDst,
		"dst_node": "",
		"dst_path": localDownloadedDst,
	})
	respTransDown, err := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(transferDownPayload))
	if err != nil || respTransDown.StatusCode != http.StatusOK {
		t.Fatalf("remote to local transfer failed: %v", err)
	}
	respTransDown.Body.Close()

	// 9.5 Remote -> Remote 单文件与目录互传
	remoteRelayFileDst := filepath.Join(tempDir, "remote_relay_file.txt")
	relayFilePayload, _ := json.Marshal(map[string]interface{}{
		"src_node": "mock-worker",
		"src_path": remoteUploadDst,
		"dst_node": "mock-worker",
		"dst_path": remoteRelayFileDst,
	})
	respRelayFile, err := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(relayFilePayload))
	if err != nil || respRelayFile.StatusCode != http.StatusOK {
		t.Fatalf("relay file transfer failed: %v", err)
	}
	respRelayFile.Body.Close()

	// Remote -> Remote 目录传输 (缺少 recursive -> 400)
	remoteRelayDirDst := filepath.Join(tempDir, "remote_relay_dir")
	badRelayDir, _ := json.Marshal(map[string]interface{}{
		"src_node":  "mock-worker",
		"src_path":  remoteDirDst,
		"dst_node":  "mock-worker",
		"dst_path":  remoteRelayDirDst,
		"recursive": false,
	})
	respBadRelayDir, _ := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(badRelayDir))
	if respBadRelayDir.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for relay dir without recursive, got %d", respBadRelayDir.StatusCode)
	}

	goodRelayDir, _ := json.Marshal(map[string]interface{}{
		"src_node":  "mock-worker",
		"src_path":  remoteDirDst,
		"dst_node":  "mock-worker",
		"dst_path":  remoteRelayDirDst,
		"recursive": true,
	})
	respGoodRelayDir, err := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(goodRelayDir))
	if err != nil || respGoodRelayDir.StatusCode != http.StatusOK {
		t.Fatalf("relay dir transfer failed: %v", err)
	}
	respGoodRelayDir.Body.Close()

	// 10. 清理已完成任务测试 (POST /api/ui/jobs/clean)
	cleanPayload, _ := json.Marshal(map[string]interface{}{
		"node": "mock-worker",
		"all":  true,
	})
	respClean, err := clientHTTP.Post(uiServer.URL+"/api/ui/jobs/clean", "application/json", bytes.NewReader(cleanPayload))
	if err != nil || respClean.StatusCode != http.StatusOK {
		t.Fatalf("clean jobs failed: %v", err)
	}
	respClean.Body.Close()

	// 11. 异常分支补充测试
	// 400 Bad JSON for transfer
	respBadTrans, _ := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", strings.NewReader("{bad"))
	if respBadTrans.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad transfer json, got %d", respBadTrans.StatusCode)
	}

	// 400 transfer non-existent local source
	badSrcPayload, _ := json.Marshal(map[string]interface{}{
		"src_node": "",
		"src_path": filepath.Join(tempDir, "non_existent_file.xyz"),
		"dst_node": "mock-worker",
		"dst_path": filepath.Join(tempDir, "out.xyz"),
	})
	respBadSrc, _ := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(badSrcPayload))
	if respBadSrc.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-existent local file, got %d", respBadSrc.StatusCode)
	}

	// 400 local-to-local non-existent source
	badL2LPayload, _ := json.Marshal(map[string]interface{}{
		"src_node": "",
		"src_path": filepath.Join(tempDir, "non_existent_file.xyz"),
		"dst_node": "",
		"dst_path": filepath.Join(tempDir, "out.xyz"),
	})
	respBadL2L, _ := clientHTTP.Post(uiServer.URL+"/api/ui/fs/transfer", "application/json", bytes.NewReader(badL2LPayload))
	if respBadL2L.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-existent l2l source, got %d", respBadL2L.StatusCode)
	}

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "offline-mock-worker",
		Target: "127.0.0.1:59999",
	})

	// 500 Run job on unreachable node
	badRunPayload, _ := json.Marshal(map[string]string{
		"node":    "offline-mock-worker",
		"command": "cmd /c dir",
	})
	respBadRun, _ := clientHTTP.Post(uiServer.URL+"/api/ui/jobs/run", "application/json", bytes.NewReader(badRunPayload))
	if respBadRun.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for unreachable node run, got %d", respBadRun.StatusCode)
	}

	// 500 Kill non-existent job
	badKillPayload, _ := json.Marshal(map[string]string{
		"job_id": "job-non-existent-99999",
	})
	respBadKill, _ := clientHTTP.Post(uiServer.URL+"/api/ui/jobs/kill", "application/json", bytes.NewReader(badKillPayload))
	if respBadKill.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for non-existent job kill, got %d", respBadKill.StatusCode)
	}

	// 500 Clean unreachable node
	badCleanPayload, _ := json.Marshal(map[string]interface{}{
		"node": "offline-mock-worker",
		"all":  true,
	})
	respBadClean, _ := clientHTTP.Post(uiServer.URL+"/api/ui/jobs/clean", "application/json", bytes.NewReader(badCleanPayload))
	if respBadClean.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for unreachable clean, got %d", respBadClean.StatusCode)
	}
}

func TestServer_ExtraEdgeCases(t *testing.T) {
	srv, _, cleanup := setupTestEnv(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. handleFsUpload 405 Method Not Allowed
	reqGetUp := httptest.NewRequest(http.MethodGet, "/api/ui/fs/upload", nil)
	wGetUp := httptest.NewRecorder()
	handler.ServeHTTP(wGetUp, reqGetUp)
	if wGetUp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET upload, got %d", wGetUp.Code)
	}

	// 2. handleFsDownload 405 Method Not Allowed
	reqPostDl := httptest.NewRequest(http.MethodPost, "/api/ui/fs/download", nil)
	wPostDl := httptest.NewRecorder()
	handler.ServeHTTP(wPostDl, reqPostDl)
	if wPostDl.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST download, got %d", wPostDl.Code)
	}

	// 3. handleNodes POST with target without port (tests auto append default port)
	nodeNoPort := `{"name":"target-no-port","target":"10.0.0.9"}`
	reqNoPort := httptest.NewRequest(http.MethodPost, "/api/ui/nodes", strings.NewReader(nodeNoPort))
	wNoPort := httptest.NewRecorder()
	handler.ServeHTTP(wNoPort, reqNoPort)
	if wNoPort.Code != http.StatusOK {
		t.Fatalf("expected 200 for node target without port, got %d", wNoPort.Code)
	}

	_ = srv.cli.SaveKnownNode(protocol.KnownNode{
		Name:   "offline-node",
		Target: "127.0.0.1:59999",
	})

	// 4. handleFsList with remote node that fails resolution
	reqBadNodeLs := httptest.NewRequest(http.MethodGet, "/api/ui/fs/ls?node=offline-node&path=.", nil)
	wBadNodeLs := httptest.NewRecorder()
	handler.ServeHTTP(wBadNodeLs, reqBadNodeLs)
	if wBadNodeLs.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for bad node ls, got %d", wBadNodeLs.Code)
	}

	// 5. handleFsUpload with remote node that fails resolution
	reqBadNodeUp := httptest.NewRequest(http.MethodPost, "/api/ui/fs/upload?node=offline-node&path=test.txt", strings.NewReader("data"))
	wBadNodeUp := httptest.NewRecorder()
	handler.ServeHTTP(wBadNodeUp, reqBadNodeUp)
	if wBadNodeUp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for bad node upload, got %d", wBadNodeUp.Code)
	}

	// 6. handleFsDownload with remote node that fails resolution
	reqBadNodeDl := httptest.NewRequest(http.MethodGet, "/api/ui/fs/download?node=offline-node&path=test.txt", nil)
	wBadNodeDl := httptest.NewRecorder()
	handler.ServeHTTP(wBadNodeDl, reqBadNodeDl)
	if wBadNodeDl.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for bad node download, got %d", wBadNodeDl.Code)
	}

	// 7. handleFsTransfer remote to remote failures (e.g. unreachable nodes)
	badRelayJSON := `{"src_node":"offline-node","src_path":"a","dst_node":"offline-node","dst_path":"b"}`
	reqBadRelay := httptest.NewRequest(http.MethodPost, "/api/ui/fs/transfer", strings.NewReader(badRelayJSON))
	wBadRelay := httptest.NewRecorder()
	handler.ServeHTTP(wBadRelay, reqBadRelay)
	if wBadRelay.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for bad relay transfer, got %d", wBadRelay.Code)
	}

	// 8. handleFsTransfer local to remote with unreachable dst
	badUpJSON := `{"src_node":"","src_path":".","dst_node":"offline-node","dst_path":"b","recursive":true}`
	reqBadUp := httptest.NewRequest(http.MethodPost, "/api/ui/fs/transfer", strings.NewReader(badUpJSON))
	wBadUp := httptest.NewRecorder()
	handler.ServeHTTP(wBadUp, reqBadUp)
	if wBadUp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for bad upload transfer, got %d", wBadUp.Code)
	}

	// 9. handleFsTransfer remote to local with unreachable src
	badDownJSON := `{"src_node":"offline-node","src_path":"a","dst_node":"","dst_path":".","recursive":true}`
	reqBadDown := httptest.NewRequest(http.MethodPost, "/api/ui/fs/transfer", strings.NewReader(badDownJSON))
	wBadDown := httptest.NewRecorder()
	handler.ServeHTTP(wBadDown, reqBadDown)
	if wBadDown.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for bad download transfer, got %d", wBadDown.Code)
	}
}

func TestServer_HandleFsRootsMkdirRm(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("CW_HOME", tempDir)

	srv := NewServer(Config{})
	handler := srv.Handler()

	// 1. handleFsRoots
	reqRoots := httptest.NewRequest(http.MethodGet, "/api/ui/fs/roots", nil)
	wRoots := httptest.NewRecorder()
	handler.ServeHTTP(wRoots, reqRoots)
	if wRoots.Code != http.StatusOK {
		t.Fatalf("expected 200 for roots, got %d", wRoots.Code)
	}
	var roots []string
	if err := json.NewDecoder(wRoots.Body).Decode(&roots); err != nil || len(roots) == 0 {
		t.Fatalf("expected non-empty roots, got %v (err: %v)", roots, err)
	}

	// Bad method for roots
	reqRootsBad := httptest.NewRequest(http.MethodPost, "/api/ui/fs/roots", nil)
	wRootsBad := httptest.NewRecorder()
	handler.ServeHTTP(wRootsBad, reqRootsBad)
	if wRootsBad.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /api/ui/fs/roots, got %d", wRootsBad.Code)
	}

	// 2. handleFsMkdir validation
	reqMkdirBadMethod := httptest.NewRequest(http.MethodGet, "/api/ui/fs/mkdir", nil)
	wMkdirBadMethod := httptest.NewRecorder()
	handler.ServeHTTP(wMkdirBadMethod, reqMkdirBadMethod)
	if wMkdirBadMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /api/ui/fs/mkdir, got %d", wMkdirBadMethod.Code)
	}

	reqMkdirBadJSON := httptest.NewRequest(http.MethodPost, "/api/ui/fs/mkdir", strings.NewReader("bad-json"))
	wMkdirBadJSON := httptest.NewRecorder()
	handler.ServeHTTP(wMkdirBadJSON, reqMkdirBadJSON)
	if wMkdirBadJSON.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad json mkdir, got %d", wMkdirBadJSON.Code)
	}

	reqMkdirEmpty := httptest.NewRequest(http.MethodPost, "/api/ui/fs/mkdir", strings.NewReader(`{"path":""}`))
	wMkdirEmpty := httptest.NewRecorder()
	handler.ServeHTTP(wMkdirEmpty, reqMkdirEmpty)
	if wMkdirEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty path mkdir, got %d", wMkdirEmpty.Code)
	}

	// 3. Local mkdir
	targetSubdir := filepath.Join(tempDir, "created-sub-dir")
	mkdirJSON, _ := json.Marshal(map[string]string{"node": "", "path": targetSubdir})
	reqMkdir := httptest.NewRequest(http.MethodPost, "/api/ui/fs/mkdir", bytes.NewReader(mkdirJSON))
	wMkdir := httptest.NewRecorder()
	handler.ServeHTTP(wMkdir, reqMkdir)
	if wMkdir.Code != http.StatusOK {
		t.Fatalf("expected 200 for local mkdir, got %d", wMkdir.Code)
	}
	fi, err := os.Stat(targetSubdir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected created directory to exist, err: %v", err)
	}

	// 4. Remote mkdir failure with unreachable node
	remoteMkdirJSON, _ := json.Marshal(map[string]string{"node": "offline-node", "path": "test"})
	reqRemoteMkdir := httptest.NewRequest(http.MethodPost, "/api/ui/fs/mkdir", bytes.NewReader(remoteMkdirJSON))
	wRemoteMkdir := httptest.NewRecorder()
	handler.ServeHTTP(wRemoteMkdir, reqRemoteMkdir)
	if wRemoteMkdir.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for remote mkdir to offline node, got %d", wRemoteMkdir.Code)
	}

	// 5. handleFsRemove validation
	reqRmBadMethod := httptest.NewRequest(http.MethodPut, "/api/ui/fs/rm", nil)
	wRmBadMethod := httptest.NewRecorder()
	handler.ServeHTTP(wRmBadMethod, reqRmBadMethod)
	if wRmBadMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for PUT /api/ui/fs/rm, got %d", wRmBadMethod.Code)
	}

	reqRmBadJSON := httptest.NewRequest(http.MethodPost, "/api/ui/fs/rm", strings.NewReader("bad-json"))
	wRmBadJSON := httptest.NewRecorder()
	handler.ServeHTTP(wRmBadJSON, reqRmBadJSON)
	if wRmBadJSON.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad json rm, got %d", wRmBadJSON.Code)
	}

	reqRmEmpty := httptest.NewRequest(http.MethodPost, "/api/ui/fs/rm", strings.NewReader(`{"path":""}`))
	wRmEmpty := httptest.NewRecorder()
	handler.ServeHTTP(wRmEmpty, reqRmEmpty)
	if wRmEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty path rm, got %d", wRmEmpty.Code)
	}

	// 6. Local rm
	rmJSON, _ := json.Marshal(map[string]interface{}{"node": "", "path": targetSubdir, "recursive": true})
	reqRm := httptest.NewRequest(http.MethodPost, "/api/ui/fs/rm", bytes.NewReader(rmJSON))
	wRm := httptest.NewRecorder()
	handler.ServeHTTP(wRm, reqRm)
	if wRm.Code != http.StatusOK {
		t.Fatalf("expected 200 for local rm, got %d", wRm.Code)
	}
	if _, err := os.Stat(targetSubdir); !os.IsNotExist(err) {
		t.Fatalf("expected directory to be deleted")
	}

	// 7. Remote rm failure with unreachable node
	remoteRmJSON, _ := json.Marshal(map[string]interface{}{"node": "offline-node", "path": "test", "recursive": true})
	reqRemoteRm := httptest.NewRequest(http.MethodPost, "/api/ui/fs/rm", bytes.NewReader(remoteRmJSON))
	wRemoteRm := httptest.NewRecorder()
	handler.ServeHTTP(wRemoteRm, reqRemoteRm)
	if wRemoteRm.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for remote rm to offline node, got %d", wRemoteRm.Code)
	}
}

