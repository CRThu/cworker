package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cworker/pkg/process"
	"cworker/pkg/protocol"

	"nhooyr.io/websocket"
)

func TestWorker_InitToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w, err := NewWorker(Config{
		Name:    "test-worker",
		Port:    9991,
		DataDir: tempDir,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	if w.cfg.Token == "" {
		t.Fatal("expected auto-generated token, got empty string")
	}

	// 检查持久化 token 文件
	tokenFile := filepath.Join(tempDir, "token")
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("token file not written: %v", err)
	}
	if string(data) != w.cfg.Token {
		t.Fatalf("token file content mismatch: expected %s, got %s", w.cfg.Token, string(data))
	}
}

func TestWorker_AuthMiddleware(t *testing.T) {
	w := &Worker{
		cfg: Config{
			Token: "valid-secret-token",
		},
	}

	dummyHandler := func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("OK"))
	}
	authed := w.authMiddleware(dummyHandler)

	// 1. 无 Token 请求 -> 401
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec1 := httptest.NewRecorder()
	authed(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec1.Code)
	}

	// 2. 错误 Token 请求 -> 401
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set("Authorization", "Bearer wrong-token")
	rec2 := httptest.NewRecorder()
	authed(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", rec2.Code)
	}

	// 3. 正确 Bearer Token -> 200
	req3 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req3.Header.Set("Authorization", "Bearer valid-secret-token")
	rec3 := httptest.NewRecorder()
	authed(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid bearer token, got %d", rec3.Code)
	}

	// 4. URL query token -> 200
	req4 := httptest.NewRequest(http.MethodGet, "/test?token=valid-secret-token", nil)
	rec4 := httptest.NewRecorder()
	authed(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid query token, got %d", rec4.Code)
	}
}

func TestWorker_FsHandlers(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_fs_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{}
	targetFilePath := filepath.Join(tempDir, "sub", "test.txt")

	// 1. 测试 make dir
	mdReq := httptest.NewRequest(http.MethodPost, "/api/v1/fs/md?path="+filepath.Join(tempDir, "sub"), nil)
	mdRec := httptest.NewRecorder()
	w.handleFsMakeDir(mdRec, mdReq)
	if mdRec.Code != http.StatusOK {
		t.Fatalf("handleFsMakeDir failed: %d", mdRec.Code)
	}

	// 2. 测试 upload
	content := []byte("hello_worker_fs_handlers")
	uploadReq := httptest.NewRequest(http.MethodPut, "/api/v1/fs/upload?path="+targetFilePath, bytes.NewReader(content))
	uploadRec := httptest.NewRecorder()
	w.handleFsUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("handleFsUpload failed: %d, body: %s", uploadRec.Code, uploadRec.Body.String())
	}

	// 2.1 测试带有错误 SHA-256 头的上传被安全拦截并清理脏文件
	badShaPath := filepath.Join(tempDir, "sub", "bad_sha.txt")
	badShaReq := httptest.NewRequest(http.MethodPut, "/api/v1/fs/upload?path="+badShaPath, bytes.NewReader(content))
	badShaReq.Header.Set("X-File-SHA256", "0000000000000000000000000000000000000000000000000000000000000000")
	badShaRec := httptest.NewRecorder()
	w.handleFsUpload(badShaRec, badShaReq)
	if badShaRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for sha256 mismatch, got %d", badShaRec.Code)
	}
	if _, err := os.Stat(badShaPath); !os.IsNotExist(err) {
		t.Fatal("corrupted file with bad sha256 should have been deleted")
	}

	// 3. 测试 download 并断言 X-File-SHA256 响应头
	downloadReq := httptest.NewRequest(http.MethodGet, "/api/v1/fs/download?path="+targetFilePath, nil)
	downloadRec := httptest.NewRecorder()
	w.handleFsDownload(downloadRec, downloadReq)
	if downloadRec.Code != http.StatusOK {
		t.Fatalf("handleFsDownload failed: %d", downloadRec.Code)
	}
	if downloadRec.Header().Get("X-File-SHA256") == "" {
		t.Fatal("expected X-File-SHA256 header in download response")
	}
	if downloadRec.Body.String() != string(content) {
		t.Fatalf("download content mismatch: expected %s, got %s", string(content), downloadRec.Body.String())
	}

	// 4. 测试 ls
	lsReq := httptest.NewRequest(http.MethodGet, "/api/v1/fs/ls?path="+filepath.Join(tempDir, "sub"), nil)
	lsRec := httptest.NewRecorder()
	w.handleFsList(lsRec, lsReq)
	if lsRec.Code != http.StatusOK {
		t.Fatalf("handleFsList failed: %d", lsRec.Code)
	}

	// 5. 测试 remove
	rmReq := httptest.NewRequest(http.MethodPost, "/api/v1/fs/rm?path="+filepath.Join(tempDir, "sub")+"&recursive=true", nil)
	rmRec := httptest.NewRecorder()
	w.handleFsRemove(rmRec, rmReq)
	if rmRec.Code != http.StatusOK {
		t.Fatalf("handleFsRemove failed: %d", rmRec.Code)
	}

	if _, err := os.Stat(targetFilePath); !os.IsNotExist(err) {
		t.Fatalf("file should have been deleted, but still exists")
	}
}

func TestWorker_JobHandlersAndHealth(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_jobs_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{
		cfg: Config{
			Name:    "test-node",
			Port:    19000,
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 1. 测试 handleHealth
	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthRec := httptest.NewRecorder()
	w.handleHealth(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("handleHealth failed: %d", healthRec.Code)
	}

	var nodeInfo protocol.NodeInfo
	if err := json.NewDecoder(healthRec.Body).Decode(&nodeInfo); err != nil {
		t.Fatalf("decode nodeInfo failed: %v", err)
	}
	if nodeInfo.Name != "test-node" {
		t.Fatalf("expected node name 'test-node', got %s", nodeInfo.Name)
	}

	// 2. 测试 handleRunJob
	runReqBody, _ := json.Marshal(protocol.RunJobRequest{
		Node:    "test-node",
		Command: "cmd.exe /c echo worker_job_ok",
	})
	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/run", bytes.NewReader(runReqBody))
	runRec := httptest.NewRecorder()
	w.handleRunJob(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("handleRunJob failed: %d, body: %s", runRec.Code, runRec.Body.String())
	}

	var jobInfo protocol.JobInfo
	if err := json.NewDecoder(runRec.Body).Decode(&jobInfo); err != nil {
		t.Fatalf("decode jobInfo failed: %v", err)
	}
	if jobInfo.ID == "" {
		t.Fatal("expected job ID, got empty")
	}

	// 等待任务完成
	time.Sleep(300 * time.Millisecond)

	// 3. 测试 handleListJobs
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/ps", nil)
	listRec := httptest.NewRecorder()
	w.handleListJobs(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("handleListJobs failed: %d", listRec.Code)
	}
	var jobs []protocol.JobInfo
	_ = json.NewDecoder(listRec.Body).Decode(&jobs)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job in list, got %d", len(jobs))
	}

	// 4. 测试 handleGetLogs
	logsReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/logs?job_id="+jobInfo.ID+"&lines=10", nil)
	logsRec := httptest.NewRecorder()
	w.handleGetLogs(logsRec, logsReq)
	if logsRec.Code != http.StatusOK {
		t.Fatalf("handleGetLogs failed: %d", logsRec.Code)
	}

	// 5. 测试 handleKillJob
	killReqBody, _ := json.Marshal(protocol.KillJobRequest{JobID: jobInfo.ID})
	killReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/kill", bytes.NewReader(killReqBody))
	killRec := httptest.NewRecorder()
	w.handleKillJob(killRec, killReq)
	if killRec.Code != http.StatusOK {
		t.Fatalf("handleKillJob failed: %d", killRec.Code)
	}
}

func TestWorker_StartAndLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_start_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w, err := NewWorker(Config{
		Name:     "start-test-node",
		BindAddr: "127.0.0.1",
		Port:     19098,
		DataDir:  tempDir,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Start(ctx)
	}()

	// 等待服务监听就绪
	time.Sleep(150 * time.Millisecond)

	// 验证健康探测端点
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:19098/api/v1/health?token=%s", w.Token()))
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// 取消 context 优雅退出
	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Fatalf("worker Start returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not shut down in time")
	}
}

func TestWorker_EdgeCases(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_edge_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{
		cfg: Config{
			Name:    "edge-node",
			Port:    19000,
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 1. handleRunJob 错误方法与损坏 JSON
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/run", nil)
	w.handleRunJob(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET on runJob, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/jobs/run", bytes.NewReader([]byte("not-json")))
	w.handleRunJob(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad json on runJob, got %d", rec.Code)
	}

	// 2. handleKillJob 错误方法与不存在的任务
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/jobs/kill", nil)
	w.handleKillJob(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET on killJob, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	body, _ := json.Marshal(protocol.KillJobRequest{JobID: "non-existent"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/jobs/kill", bytes.NewReader(body))
	w.handleKillJob(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for kill non-existent job, got %d", rec.Code)
	}

	// 3. handleGetLogs 缺少参数与不存在日志
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/jobs/logs", nil)
	w.handleGetLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing job_id, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/jobs/logs?job_id=non-existent", nil)
	w.handleGetLogs(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent job log, got %d", rec.Code)
	}

	// 4. FS 异常测试
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/fs/upload", nil)
	w.handleFsUpload(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET on fs/upload, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/fs/download?path="+filepath.Join(tempDir, "missing.txt"), nil)
	w.handleFsDownload(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for download missing file, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/fs/ls?path="+filepath.Join(tempDir, "missing_dir"), nil)
	w.handleFsList(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for ls missing dir, got %d", rec.Code)
	}

	// 5. getLocalIP 测活
	ip := getLocalIP()
	if ip == "" {
		t.Fatal("expected non-empty local IP")
	}
}

func TestWorker_Getters(t *testing.T) {
	w := &Worker{
		cfg: Config{
			Name: "getter-node",
			Port: 19055,
		},
	}
	if w.Name() != "getter-node" {
		t.Fatalf("expected getter-node, got %s", w.Name())
	}
	if w.Port() != 19055 {
		t.Fatalf("expected 19055, got %d", w.Port())
	}
}

func TestWorker_FsMakeDir_And_Remove_EdgeCases(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_fs_edge_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{}

	// 1. handleFsMakeDir 错误方法 (GET) -> 405
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fs/md?path="+filepath.Join(tempDir, "sub"), nil)
	w.handleFsMakeDir(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET on makeDir, got %d", rec.Code)
	}

	// 2. handleFsMakeDir 缺少或非法路径 -> 400
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/fs/md?path=", nil)
	w.handleFsMakeDir(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty path on makeDir, got %d", rec.Code)
	}

	// 3. handleFsRemove 非法路径 -> 400
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/fs/rm?path=", nil)
	w.handleFsRemove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty path on remove, got %d", rec.Code)
	}

	// 4. handleFsRemove 不存在路径 -> 404
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/fs/rm?path="+filepath.Join(tempDir, "not_exist"), nil)
	w.handleFsRemove(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent path on remove, got %d", rec.Code)
	}

	// 5. handleFsRemove 非空目录未加 recursive=true -> 400 拦截
	nonEmptyDir := filepath.Join(tempDir, "non_empty")
	_ = os.MkdirAll(nonEmptyDir, 0755)
	_ = os.WriteFile(filepath.Join(nonEmptyDir, "child.txt"), []byte("data"), 0644)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/fs/rm?path="+nonEmptyDir, nil)
	w.handleFsRemove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-empty dir without recursive, got %d", rec.Code)
	}

	// 6. handleFsRemove 空目录未加 recursive=true -> 200 成功删除
	emptyDir := filepath.Join(tempDir, "empty_dir")
	_ = os.MkdirAll(emptyDir, 0755)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/fs/rm?path="+emptyDir, nil)
	w.handleFsRemove(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty dir without recursive, got %d", rec.Code)
	}
	if _, err := os.Stat(emptyDir); !os.IsNotExist(err) {
		t.Fatal("empty dir should have been removed")
	}

	// 7. handleFsRemove 单个文件删除 -> 200 成功
	singleFile := filepath.Join(tempDir, "single.txt")
	_ = os.WriteFile(singleFile, []byte("file_content"), 0644)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/fs/rm?path="+singleFile, nil)
	w.handleFsRemove(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for single file removal, got %d", rec.Code)
	}
	if _, err := os.Stat(singleFile); !os.IsNotExist(err) {
		t.Fatal("file should have been removed")
	}
}

func TestWorker_StreamLogs_WebSocket(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_ws_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{
		cfg: Config{
			Name:    "ws-test-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 1. 测试 missing job_id
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/stream", nil)
	w.handleStreamLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing job_id, got %d", rec.Code)
	}

	// 2. 测试 non-existent job
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/jobs/stream?job_id=missing-job", nil)
	w.handleStreamLogs(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent job, got %d", rec.Code)
	}

	// 3. 启动真实 httptest.Server 建立 WebSocket 连接流式拉取
	runReq := protocol.RunJobRequest{
		Name:    "stream-job",
		Command: "cmd.exe /c echo chunk1 && ping -n 2 127.0.0.1 >nul && echo chunk2",
	}
	job, err := process.StartJob(runReq, "ws-job-1", "ws-node", tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}
	w.jobs["ws-job-1"] = job

	server := httptest.NewServer(http.HandlerFunc(w.handleStreamLogs))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "?job_id=ws-job-1"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var received bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, msg, err := conn.Read(ctx)
			if err != nil {
				return
			}
			received.Write(msg)
			if strings.Contains(received.String(), "chunk1") {
				return
			}
		}
	}()

	select {
	case <-readDone:
		if !strings.Contains(received.String(), "chunk1") {
			t.Fatalf("expected stream to contain chunk1, got: %s", received.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket chunks")
	}
}

func TestWorker_FsList_RecursiveAndRelativePath(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{}

	// 创建多层结构: root/fileA.txt, root/sub/fileB.txt, root/empty_dir
	_ = os.WriteFile(filepath.Join(tempDir, "fileA.txt"), []byte("contentA"), 0644)
	subDir := filepath.Join(tempDir, "sub")
	_ = os.MkdirAll(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "fileB.txt"), []byte("contentB"), 0644)
	_ = os.MkdirAll(filepath.Join(tempDir, "empty_dir"), 0755)

	// 请求 recursive=true
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fs/ls?path="+tempDir+"&recursive=true", nil)
	w.handleFsList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	var list []protocol.FileInfo
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	foundPaths := make(map[string]protocol.FileInfo)
	for _, fi := range list {
		foundPaths[fi.Path] = fi
	}

	// 断言所有 Path 都是干净的标准相对路径 (零绝对路径前缀)
	if _, ok := foundPaths["fileA.txt"]; !ok {
		t.Fatal("expected fileA.txt in relative paths")
	}
	if _, ok := foundPaths["sub/fileB.txt"]; !ok {
		t.Fatal("expected sub/fileB.txt in relative paths")
	}
	if empty, ok := foundPaths["empty_dir"]; !ok || !empty.IsDir {
		t.Fatal("expected empty_dir preserved as directory")
	}
}

func TestWorker_FsList_FileError(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{}
	filePath := filepath.Join(tempDir, "sample.txt")
	_ = os.WriteFile(filePath, []byte("text"), 0644)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fs/ls?path="+filePath, nil)
	w.handleFsList(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for ls on regular file, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "path is a file, not a directory") {
		t.Fatalf("unexpected error body: %s", rec.Body.String())
	}
}

func TestWorker_FsDownload_DirectoryCheckAndSizeHeader(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{}

	// 1. 尝试对目录调用 download 应返回 400
	recDir := httptest.NewRecorder()
	reqDir := httptest.NewRequest(http.MethodGet, "/api/v1/fs/download?path="+tempDir, nil)
	w.handleFsDownload(recDir, reqDir)
	if recDir.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for download on dir, got %d", recDir.Code)
	}

	// 2. 普通文件应包含 X-File-Size 头部与 Trailer
	filePath := filepath.Join(tempDir, "download_test.txt")
	content := []byte("downloadable content 12345")
	_ = os.WriteFile(filePath, content, 0644)

	recFile := httptest.NewRecorder()
	reqFile := httptest.NewRequest(http.MethodGet, "/api/v1/fs/download?path="+filePath, nil)
	w.handleFsDownload(recFile, reqFile)
	if recFile.Code != http.StatusOK {
		t.Fatalf("expected 200 for download file, got %d", recFile.Code)
	}
	if recFile.Header().Get("X-File-Size") != fmt.Sprintf("%d", len(content)) {
		t.Fatalf("expected X-File-Size=%d, got %s", len(content), recFile.Header().Get("X-File-Size"))
	}
	if recFile.Header().Get("Trailer") != "X-File-SHA256" {
		t.Fatalf("expected Trailer header set, got %s", recFile.Header().Get("Trailer"))
	}
}

func TestWorker_FsUpload_DirectoryCheck(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{}

	// 上传到已有目录路径应返回 400
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/fs/upload?path="+tempDir, strings.NewReader("some data"))
	w.handleFsUpload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for upload to existing directory, got %d", rec.Code)
	}
}

func TestWorker_IsValidJobID(t *testing.T) {
	validIDs := []string{
		"job-1234abcd",
		"job-abcdef0123456789",
		"job_test_123",
		"Job-ABC_123",
	}
	for _, id := range validIDs {
		if !isValidJobID(id) {
			t.Errorf("expected valid job ID for %q, got false", id)
		}
	}

	invalidIDs := []string{
		"",
		"../../etc/passwd",
		"../jobs/output.log",
		"job/123",
		"job\\123",
		"job:123",
		"job 123",
		"job;rm",
		strings.Repeat("a", 129),
	}
	for _, id := range invalidIDs {
		if isValidJobID(id) {
			t.Errorf("expected invalid job ID for %q, got true", id)
		}
	}
}

func TestWorker_TailFile_SmallAndLarge(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 空文件
	emptyPath := filepath.Join(tempDir, "empty.log")
	emptyFile, err := os.Create(emptyPath)
	if err != nil {
		t.Fatalf("create empty file failed: %v", err)
	}
	defer emptyFile.Close()

	res, err := tailFile(emptyFile, 10)
	if err != nil || len(res) != 0 {
		t.Fatalf("expected empty result for empty file, got: %q, err: %v", string(res), err)
	}

	// 2. 小文件
	smallPath := filepath.Join(tempDir, "small.log")
	smallContent := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(smallPath, []byte(smallContent), 0644); err != nil {
		t.Fatalf("write small file failed: %v", err)
	}
	smallFile, _ := os.Open(smallPath)
	defer smallFile.Close()

	res, err = tailFile(smallFile, 2)
	if err != nil {
		t.Fatalf("tail small file failed: %v", err)
	}
	expected := "line4\nline5\n"
	if string(res) != expected {
		t.Fatalf("tail small file mismatch: expected %q, got %q", expected, string(res))
	}

	// 3. 大文件 (>64KB)
	largePath := filepath.Join(tempDir, "large.log")
	largeFile, err := os.OpenFile(largePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("create large file failed: %v", err)
	}
	defer largeFile.Close()

	for i := 1; i <= 2000; i++ {
		_, _ = fmt.Fprintf(largeFile, "log-record-item-index-%05d-data-padding-padding-padding\n", i)
	}

	res, err = tailFile(largeFile, 3)
	if err != nil {
		t.Fatalf("tail large file failed: %v", err)
	}
	resStr := string(res)
	if !strings.Contains(resStr, "log-record-item-index-02000") || !strings.Contains(resStr, "log-record-item-index-01998") {
		t.Fatalf("tail large file missing expected lines: %s", resStr)
	}
	lines := strings.Split(strings.TrimSuffix(resStr, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected exactly 3 lines, got %d: %v", len(lines), lines)
	}
}

func TestWorker_GetLogs_PathTraversal_Rejected(t *testing.T) {
	w := &Worker{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/logs?job_id=../../windows/win.ini", nil)
	w.handleGetLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for path traversal job_id, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid or missing job_id") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestWorker_FsUpload_FailureDoesNotDestroyExistingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_atomic_upload_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{}
	targetFilePath := filepath.Join(tempDir, "critical_data.txt")
	originalContent := "original-worker-important-content"
	if err := os.WriteFile(targetFilePath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("write original file failed: %v", err)
	}

	// 1. 上传提供错误的 SHA-256 头
	corruptedUpload := []byte("new-corrupted-upload-data")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/fs/upload?path="+targetFilePath, bytes.NewReader(corruptedUpload))
	req.Header.Set("X-File-SHA256", "0000000000000000000000000000000000000000000000000000000000000000")
	rec := httptest.NewRecorder()
	w.handleFsUpload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
	}

	// 验证原文件未被截断或删除，内容完好无损
	currentContent, err := os.ReadFile(targetFilePath)
	if err != nil {
		t.Fatalf("target file was deleted or cannot be read: %v", err)
	}
	if string(currentContent) != originalContent {
		t.Fatalf("target file content altered! expected %q, got %q", originalContent, string(currentContent))
	}

	// 验证没有残留的临时 .cwupload- 文件
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".cwupload-") {
			t.Fatalf("leak temp file found: %s", e.Name())
		}
	}
}

func TestWorker_FsList_FaithfullyListsAllFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_list_all_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{}
	// 创建多个文件，包含各种命名的文件
	f1 := filepath.Join(tempDir, "app.log")
	_ = os.WriteFile(f1, []byte("log data"), 0644)
	f2 := filepath.Join(tempDir, "data.bin")
	_ = os.WriteFile(f2, []byte("bin data"), 0644)

	// 测试单层 ReadDir：真实忠实反映磁盘文件全集（SSOT）
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fs/ls?path="+tempDir, nil)
	w.handleFsList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleFsList failed: %d", rec.Code)
	}

	var entries []protocol.FileInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal entries failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected exactly 2 files, got: %+v", entries)
	}
}

// 模拟传输中断时，defer 机制确保临时文件绝不残留
type errReader struct {
	readBytes int
}

func (r *errReader) Read(p []byte) (n int, err error) {
	if r.readBytes > 0 {
		return 0, io.ErrUnexpectedEOF
	}
	r.readBytes = len(p)
	copy(p, "some-partial-data")
	return len("some-partial-data"), nil
}

func TestWorker_FsUpload_InterruptCleansTempFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_interrupt_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{}
	targetFilePath := filepath.Join(tempDir, "interrupted_target.txt")

	// 使用模拟读取发生 UnexpectedEOF 异常的 reader
	req := httptest.NewRequest(http.MethodPut, "/api/v1/fs/upload?path="+targetFilePath, &errReader{})
	rec := httptest.NewRecorder()
	w.handleFsUpload(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on interrupted stream, got %d", rec.Code)
	}

	// 确认目标目录下没有任何残留的 .cwupload- 临时文件
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".cwupload-") {
			t.Fatalf("temporary upload file leaked on interrupt: %s", e.Name())
		}
	}
}

func TestWorker_HttpMethodRestrictions(t *testing.T) {
	w := &Worker{}

	// 1. handleFsRemove 拒绝 GET，只允许 POST 和 DELETE
	rmReqGet := httptest.NewRequest(http.MethodGet, "/api/v1/fs/rm?path=dummy", nil)
	rmRecGet := httptest.NewRecorder()
	w.handleFsRemove(rmRecGet, rmReqGet)
	if rmRecGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("handleFsRemove expected 405 on GET, got %d", rmRecGet.Code)
	}

	// 2. handleFsDownload 拒绝 POST，只允许 GET
	dlReqPost := httptest.NewRequest(http.MethodPost, "/api/v1/fs/download?path=dummy", nil)
	dlRecPost := httptest.NewRecorder()
	w.handleFsDownload(dlRecPost, dlReqPost)
	if dlRecPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("handleFsDownload expected 405 on POST, got %d", dlRecPost.Code)
	}

	// 3. handleFsList 拒绝 POST，只允许 GET
	lsReqPost := httptest.NewRequest(http.MethodPost, "/api/v1/fs/ls?path=dummy", nil)
	lsRecPost := httptest.NewRecorder()
	w.handleFsList(lsRecPost, lsReqPost)
	if lsRecPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("handleFsList expected 405 on POST, got %d", lsRecPost.Code)
	}

	// 4. handleFsMakeDir 拒绝 GET，只允许 POST
	mdReqGet := httptest.NewRequest(http.MethodGet, "/api/v1/fs/md?path=dummy", nil)
	mdRecGet := httptest.NewRecorder()
	w.handleFsMakeDir(mdRecGet, mdReqGet)
	if mdRecGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("handleFsMakeDir expected 405 on GET, got %d", mdRecGet.Code)
	}

	// 5. handleRunJob 拒绝 GET，只允许 POST
	runReqGet := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/run", nil)
	runRecGet := httptest.NewRecorder()
	w.handleRunJob(runRecGet, runReqGet)
	if runRecGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("handleRunJob expected 405 on GET, got %d", runRecGet.Code)
	}

	// 6. handleKillJob 拒绝 GET，只允许 POST
	killReqGet := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/kill", nil)
	killRecGet := httptest.NewRecorder()
	w.handleKillJob(killRecGet, killReqGet)
	if killRecGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("handleKillJob expected 405 on GET, got %d", killRecGet.Code)
	}
}

func TestWorker_TailFile_LargeChunks(t *testing.T) {
	tempDir := t.TempDir()
	largeFile := filepath.Join(tempDir, "large_log.txt")

	// 构建大于 64KB (如 150KB) 的大文件
	var contentBuilder strings.Builder
	totalLines := 3000
	for i := 1; i <= totalLines; i++ {
		fmt.Fprintf(&contentBuilder, "LOG_LINE_%05d_DATA_PADDING_TO_FILL_BUFFER_01234567890123456789\n", i)
	}
	if err := os.WriteFile(largeFile, []byte(contentBuilder.String()), 0644); err != nil {
		t.Fatalf("write large log file failed: %v", err)
	}

	f, err := os.Open(largeFile)
	if err != nil {
		t.Fatalf("open large log file failed: %v", err)
	}
	defer f.Close()

	// 截取末尾 5 行
	res, err := tailFile(f, 5)
	if err != nil {
		t.Fatalf("tailFile large chunk failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(res)), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d: %s", len(lines), string(res))
	}
	if !strings.Contains(lines[4], "LOG_LINE_03000") {
		t.Fatalf("expected last line to be LOG_LINE_03000, got %s", lines[4])
	}
	if !strings.Contains(lines[0], "LOG_LINE_02996") {
		t.Fatalf("expected first line of slice to be LOG_LINE_02996, got %s", lines[0])
	}
}

