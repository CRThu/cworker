package worker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	// 6. 测试 roots
	rootsReq := httptest.NewRequest(http.MethodGet, "/api/v1/fs/roots", nil)
	rootsRec := httptest.NewRecorder()
	w.handleFsRoots(rootsRec, rootsReq)
	if rootsRec.Code != http.StatusOK {
		t.Fatalf("handleFsRoots failed: %d", rootsRec.Code)
	}
	var roots []string
	if err := json.NewDecoder(rootsRec.Body).Decode(&roots); err != nil || len(roots) == 0 {
		t.Fatalf("failed to decode roots or empty: %v", err)
	}

	// 6.1 测试 roots 非法 Method
	badRootsReq := httptest.NewRequest(http.MethodPost, "/api/v1/fs/roots", nil)
	badRootsRec := httptest.NewRecorder()
	w.handleFsRoots(badRootsRec, badRootsReq)
	if badRootsRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 MethodNotAllowed for POST roots, got %d", badRootsRec.Code)
	}
}

func TestWorker_FsHash(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_hash_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{}

	// 准备测试文件结构
	subDir := filepath.Join(tempDir, "sub")
	_ = os.MkdirAll(subDir, 0755)
	file1Path := filepath.Join(subDir, "file1.txt")
	file2Path := filepath.Join(subDir, "file2.txt")
	content1 := []byte("hello_hash_content_1")
	content2 := []byte("hello_hash_content_2")
	_ = os.WriteFile(file1Path, content1, 0644)
	_ = os.WriteFile(file2Path, content2, 0644)

	expectedHash1 := sha256.Sum256(content1)
	expectedHash1Hex := hex.EncodeToString(expectedHash1[:])

	// 1. 测试单文件哈希
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/fs/hash?path="+file1Path, nil)
	rec1 := httptest.NewRecorder()
	w.handleFsHash(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("handleFsHash single file failed: %d, body: %s", rec1.Code, rec1.Body.String())
	}
	var res1 []protocol.FileInfo
	if err := json.NewDecoder(rec1.Body).Decode(&res1); err != nil {
		t.Fatalf("decode single file response failed: %v", err)
	}
	if len(res1) != 1 || res1[0].SHA256 != expectedHash1Hex || res1[0].Size != int64(len(content1)) {
		t.Fatalf("unexpected single file hash result: %+v", res1)
	}

	// 2. 测试目录递归哈希 (-r)
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/fs/hash?path="+subDir+"&recursive=true", nil)
	rec2 := httptest.NewRecorder()
	w.handleFsHash(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("handleFsHash recursive dir failed: %d, body: %s", rec2.Code, rec2.Body.String())
	}
	var res2 []protocol.FileInfo
	if err := json.NewDecoder(rec2.Body).Decode(&res2); err != nil {
		t.Fatalf("decode dir response failed: %v", err)
	}
	if len(res2) != 2 {
		t.Fatalf("expected 2 files in dir hash, got %d", len(res2))
	}

	// 3. 测试目录未加 recursive=true 拦截 (400 Bad Request)
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/fs/hash?path="+subDir, nil)
	rec3 := httptest.NewRecorder()
	w.handleFsHash(rec3, req3)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for dir without recursive flag, got %d", rec3.Code)
	}

	// 4. 测试不存在路径 (404 Not Found)
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/fs/hash?path="+filepath.Join(tempDir, "non_existent"), nil)
	rec4 := httptest.NewRecorder()
	w.handleFsHash(rec4, req4)
	if rec4.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent path, got %d", rec4.Code)
	}

	// 5. 测试非 GET 方法 (405 Method Not Allowed)
	req5 := httptest.NewRequest(http.MethodPost, "/api/v1/fs/hash?path="+file1Path, nil)
	rec5 := httptest.NewRecorder()
	w.handleFsHash(rec5, req5)
	if rec5.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST method, got %d", rec5.Code)
	}

	// 6. 测试 NDJSON 流式模式 (stream=true 参数)
	req6 := httptest.NewRequest(http.MethodGet, "/api/v1/fs/hash?path="+subDir+"&recursive=true&stream=true", nil)
	rec6 := httptest.NewRecorder()
	w.handleFsHash(rec6, req6)
	if rec6.Code != http.StatusOK {
		t.Fatalf("handleFsHash stream failed: %d, body: %s", rec6.Code, rec6.Body.String())
	}
	if ct := rec6.Header().Get("Content-Type"); !strings.Contains(ct, "application/x-ndjson") {
		t.Fatalf("expected Content-Type application/x-ndjson, got: %s", ct)
	}

	var streamEvents []protocol.FsHashEvent
	scanner := bufio.NewScanner(rec6.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev protocol.FsHashEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("unmarshal NDJSON line failed: %v, line: %s", err, string(line))
		}
		streamEvents = append(streamEvents, ev)
	}
	if len(streamEvents) < 3 {
		t.Fatalf("expected at least 3 events in stream (init, entries, done), got %d", len(streamEvents))
	}
	if streamEvents[0].Event != protocol.FsHashEventInit || streamEvents[0].TotalFiles != 2 {
		t.Fatalf("unexpected stream init event: %+v", streamEvents[0])
	}
	lastStreamEv := streamEvents[len(streamEvents)-1]
	if lastStreamEv.Event != protocol.FsHashEventDone || lastStreamEv.TotalFiles != 2 {
		t.Fatalf("unexpected stream done event: %+v", lastStreamEv)
	}

	// 7. 测试 Accept: application/x-ndjson 请求头触发流式响应
	req7 := httptest.NewRequest(http.MethodGet, "/api/v1/fs/hash?path="+file1Path, nil)
	req7.Header.Set("Accept", "application/x-ndjson")
	rec7 := httptest.NewRecorder()
	w.handleFsHash(rec7, req7)
	if rec7.Code != http.StatusOK {
		t.Fatalf("handleFsHash with Accept header failed: %d", rec7.Code)
	}
	if ct := rec7.Header().Get("Content-Type"); !strings.Contains(ct, "application/x-ndjson") {
		t.Fatalf("expected Content-Type application/x-ndjson from Accept header, got: %s", ct)
	}
}

// 验证旧版本客户端请求新版本 Worker 时，服务端准确识别并回退为纯净 application/json 单包响应
func TestWorker_FsHash_LegacyClient_Compatibility(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "legacy_compat.txt")
	_ = os.WriteFile(filePath, []byte("legacy payload"), 0644)

	w := &Worker{cfg: Config{DataDir: tempDir}}

	// 模拟老客户端：无 stream 参数，默认 Accept: */*
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fs/hash?path="+filePath, nil)
	rec := httptest.NewRecorder()

	w.handleFsHash(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleFsHash legacy client request failed: %d", rec.Code)
	}
	// 契约断言：必须为 application/json，绝不能返回 application/x-ndjson
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected Content-Type application/json for legacy client, got: %s", ct)
	}

	var list []protocol.FileInfo
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode legacy JSON response failed: %v", err)
	}
	if len(list) != 1 || list[0].Name != "legacy_compat.txt" {
		t.Fatalf("unexpected legacy response body: %+v", list)
	}
}

// 验证客户端在中途断开连接时，服务端的 HashStream 和 handleFsHash 能够立即侦测并优雅退出，零句柄/Goroutine 泄漏
func TestWorker_FsHash_ClientDisconnect_GracefulExit(t *testing.T) {
	tempDir := t.TempDir()
	// 创建多个文件
	for i := 1; i <= 20; i++ {
		_ = os.WriteFile(filepath.Join(tempDir, fmt.Sprintf("file_%d.txt", i)), []byte(fmt.Sprintf("content_%d", i)), 0644)
	}

	w := &Worker{cfg: Config{DataDir: tempDir}}

	// 使用带 Cancel 的 Context 模拟客户端读取首包后立即断连
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fs/hash?path="+tempDir+"&recursive=true&stream=true", nil).WithContext(ctx)

	// 创建可感知写入中断的自定义 ResponseWriter
	abortWriter := &abortableRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		onWrite: func() {
			// 在初次写入数据时立即触发客户端断连
			cancel()
		},
	}

	doneChan := make(chan struct{})
	go func() {
		w.handleFsHash(abortWriter, req)
		close(doneChan)
	}()

	select {
	case <-doneChan:
		// 成功快速安全退出
	case <-time.After(2 * time.Second):
		t.Fatal("handleFsHash failed to exit promptly upon client disconnection")
	}
}

type abortableRecorder struct {
	*httptest.ResponseRecorder
	onWrite func()
}

func (a *abortableRecorder) Write(buf []byte) (int, error) {
	if a.onWrite != nil {
		a.onWrite()
	}
	return a.ResponseRecorder.Write(buf)
}

func (a *abortableRecorder) Flush() {
	if a.ResponseRecorder.Flushed {
		return
	}
	a.ResponseRecorder.Flush()
}

// 验证真实 TCP Socket 物理层突然断开时，handleFsHash 能安全拦截 broken pipe 并安全释放句柄
func TestWorker_FsHash_RealSocketDisconnect(t *testing.T) {
	tempDir := t.TempDir()
	for i := 1; i <= 30; i++ {
		_ = os.WriteFile(filepath.Join(tempDir, fmt.Sprintf("file_%02d.txt", i)), []byte(fmt.Sprintf("data_%d", i)), 0644)
	}

	w := &Worker{cfg: Config{DataDir: tempDir}}
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.handleFsHash(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatalf("dial server failed: %v", err)
	}

	reqStr := fmt.Sprintf("GET /api/v1/fs/hash?path=%s&recursive=true&stream=true HTTP/1.1\r\nHost: %s\r\nAccept: application/x-ndjson\r\n\r\n",
		url.QueryEscape(tempDir), u.Host)
	_, _ = conn.Write([]byte(reqStr))

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(statusLine, "200") {
		t.Fatalf("expected 200 OK status line, got %s, err: %v", statusLine, err)
	}

	// 消费 Headers
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}

	// 读取 1~2 行 NDJSON
	_, _ = reader.ReadString('\n')
	_, _ = reader.ReadString('\n')

	// 模拟网络硬件断线或客户端进程崩溃：强制暴力关闭 TCP 连接
	_ = conn.Close()

	// 验证服务端不发生 panic，正常完成清理
	time.Sleep(100 * time.Millisecond)
}

// 验证面对密集小文件并发哈希时，服务端对网络 Flush 执行 100ms 节流合并（Batching），
// 并验证大文件 Progress 事件与 Init/Done 事件立即 Flush，全量事件无损解析
func TestWorker_FsHash_Stream_ThrottledFlushing_Batching(t *testing.T) {
	tempDir := t.TempDir()
	// 创建 30 个小文件
	for i := 1; i <= 30; i++ {
		_ = os.WriteFile(filepath.Join(tempDir, fmt.Sprintf("small_%02d.txt", i)), []byte(fmt.Sprintf("data_%d", i)), 0644)
	}

	w := &Worker{cfg: Config{DataDir: tempDir}}

	flushCount := 0
	flusherRecorder := &countingFlusherRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		onFlush: func() {
			flushCount++
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fs/hash?path="+tempDir+"&recursive=true&stream=true", nil)
	w.handleFsHash(flusherRecorder, req)

	if flusherRecorder.Code != http.StatusOK {
		t.Fatalf("handleFsHash stream failed: %d, body: %s", flusherRecorder.Code, flusherRecorder.Body.String())
	}

	// 验证所有事件均无损到达
	scanner := bufio.NewScanner(flusherRecorder.Body)
	var events []protocol.FsHashEvent
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev protocol.FsHashEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("unmarshal event failed: %v, line: %s", err, string(line))
		}
		events = append(events, ev)
	}

	// 30 个文件 + 1 个 Init + 1 个 Done = 32 个事件
	if len(events) != 32 {
		t.Fatalf("expected 32 events (1 init + 30 entries + 1 done), got %d", len(events))
	}

	if events[0].Event != protocol.FsHashEventInit || events[0].TotalFiles != 30 {
		t.Fatalf("unexpected init event: %+v", events[0])
	}
	if events[len(events)-1].Event != protocol.FsHashEventDone || events[len(events)-1].TotalFiles != 30 {
		t.Fatalf("unexpected done event: %+v", events[len(events)-1])
	}

	// 核心断言：32 个事件在毫秒级内跑完，经 100ms 节流合并后，Flush 调用次数必须远小于 30（发生显著 Batching 攒批）
	if flushCount >= 20 {
		t.Fatalf("expected flushCount to be throttled/batched (< 20), got %d flushes for 32 events", flushCount)
	}
}

type countingFlusherRecorder struct {
	*httptest.ResponseRecorder
	onFlush func()
}

func (c *countingFlusherRecorder) Flush() {
	if c.onFlush != nil {
		c.onFlush()
	}
	c.ResponseRecorder.Flush()
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

func TestWorker_FsRemove_Streaming(t *testing.T) {
	tempDir := t.TempDir()
	w, err := NewWorker(Config{
		Name:     "test-rm-worker",
		BindAddr: "127.0.0.1",
		Port:     0,
		DataDir:  tempDir,
	})
	if err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// 准备包含 10 个文件的多层级测试目录
	streamDir := filepath.Join(tempDir, "stream_rm")
	for i := 0; i < 3; i++ {
		sub := filepath.Join(streamDir, fmt.Sprintf("sub_%d", i))
		_ = os.MkdirAll(sub, 0755)
		for j := 0; j < 3; j++ {
			_ = os.WriteFile(filepath.Join(sub, fmt.Sprintf("file_%d.txt", j)), []byte("data"), 0644)
		}
	}
	// 额外加一个根级文件: 总共 1(streamDir) + 3(sub) + 9(files) + 1 = 14 项
	_ = os.WriteFile(filepath.Join(streamDir, "root.txt"), []byte("root"), 0644)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fs/rm?path="+streamDir+"&recursive=true&stream=true", nil)
	w.handleFsRemove(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/x-ndjson") {
		t.Fatalf("expected application/x-ndjson content type, got: %s", ct)
	}

	// 解析返回的 NDJSON 事件
	dec := json.NewDecoder(rec.Body)
	var gotDone bool
	var finalCount int64
	for {
		var ev protocol.FsRmEvent
		if err := dec.Decode(&ev); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode FsRmEvent failed: %v", err)
		}
		if ev.Event == protocol.FsRmEventDone {
			gotDone = true
			finalCount = ev.RemovedCount
		}
	}

	if !gotDone {
		t.Fatal("expected FsRmEventDone in stream output")
	}
	if finalCount != 14 {
		t.Fatalf("expected 14 items deleted, got %d", finalCount)
	}
	if _, err := os.Stat(streamDir); !os.IsNotExist(err) {
		t.Fatal("streamDir should be physically removed")
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

func TestWorker_CleanJobs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_clean_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{
		cfg: Config{
			Name:    "test-node",
			Port:    19001,
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 1. 无效请求 (既不是 all，days 也是 0) -> 400
	badBody, _ := json.Marshal(protocol.CleanJobsRequest{Days: 0, All: false})
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(badBody))
	badRec := httptest.NewRecorder()
	w.handleCleanJobs(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", badRec.Code)
	}

	// 2. 派发一个短任务并等待结束
	runBody, _ := json.Marshal(protocol.RunJobRequest{
		Name:    "echo-job",
		Command: "cmd.exe /c echo clean_test_ok",
	})
	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/run", bytes.NewReader(runBody))
	runRec := httptest.NewRecorder()
	w.handleRunJob(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("handleRunJob failed: %d", runRec.Code)
	}
	var jobInfo protocol.JobInfo
	_ = json.NewDecoder(runRec.Body).Decode(&jobInfo)

	time.Sleep(400 * time.Millisecond)

	// 确保磁盘目录存在
	jobDir := filepath.Join(tempDir, "jobs", jobInfo.ID)
	if _, err := os.Stat(jobDir); os.IsNotExist(err) {
		t.Fatalf("expected job dir to exist: %s", jobDir)
	}

	// 3. 执行 All 清理
	cleanBody, _ := json.Marshal(protocol.CleanJobsRequest{All: true})
	cleanReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(cleanBody))
	cleanRec := httptest.NewRecorder()
	w.handleCleanJobs(cleanRec, cleanReq)
	if cleanRec.Code != http.StatusOK {
		t.Fatalf("handleCleanJobs failed: %d", cleanRec.Code)
	}

	var cleanResp protocol.CleanJobsResponse
	_ = json.NewDecoder(cleanRec.Body).Decode(&cleanResp)
	if cleanResp.CleanedCount != 1 {
		t.Fatalf("expected 1 cleaned job, got %d", cleanResp.CleanedCount)
	}

	// 验证内存已删除
	w.mu.RLock()
	_, exists := w.jobs[jobInfo.ID]
	w.mu.RUnlock()
	if exists {
		t.Fatal("expected job to be deleted from memory map")
	}

	// 验证磁盘目录已删除
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatal("expected job disk directory to be removed")
	}
}

func TestWorker_CleanJobs_ProtectionAndRetention(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_clean_retention_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{
		cfg: Config{
			Name:    "test-node",
			Port:    19002,
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 1. 派发一个长期运行的活跃任务 (RUNNING)
	runBody, _ := json.Marshal(protocol.RunJobRequest{
		Name:    "running-task",
		Command: "ping 127.0.0.1 -n 30",
	})
	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/run", bytes.NewReader(runBody))
	runRec := httptest.NewRecorder()
	w.handleRunJob(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("handleRunJob failed: %d", runRec.Code)
	}
	var runningJob protocol.JobInfo
	_ = json.NewDecoder(runRec.Body).Decode(&runningJob)

	// 确保运行任务退出时一定被终止
	defer func() {
		killBody, _ := json.Marshal(protocol.KillJobRequest{JobID: runningJob.ID})
		killReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/kill", bytes.NewReader(killBody))
		killRec := httptest.NewRecorder()
		w.handleKillJob(killRec, killReq)
	}()

	// 2. 派发一个立即结束的任务 (近期任务)
	runBody2, _ := json.Marshal(protocol.RunJobRequest{
		Name:    "recent-task",
		Command: "cmd.exe /c echo recent_done",
	})
	runReq2 := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/run", bytes.NewReader(runBody2))
	runRec2 := httptest.NewRecorder()
	w.handleRunJob(runRec2, runReq2)
	var recentJob protocol.JobInfo
	_ = json.NewDecoder(runRec2.Body).Decode(&recentJob)

	// 等待 recentJob 执行完成
	time.Sleep(400 * time.Millisecond)

	// 3. 执行 days=7 清理 (保留最近 7 天任务)
	cleanBody7, _ := json.Marshal(protocol.CleanJobsRequest{Days: 7})
	cleanReq7 := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(cleanBody7))
	cleanRec7 := httptest.NewRecorder()
	w.handleCleanJobs(cleanRec7, cleanReq7)
	if cleanRec7.Code != http.StatusOK {
		t.Fatalf("clean days=7 failed: %d", cleanRec7.Code)
	}
	var resp7 protocol.CleanJobsResponse
	_ = json.NewDecoder(cleanRec7.Body).Decode(&resp7)
	if resp7.CleanedCount != 0 {
		t.Fatalf("expected 0 cleaned jobs under days=7, got %d", resp7.CleanedCount)
	}

	// 4. 执行 all=true 清理 -> 必须清理 recent-task，但绝对严禁清理 running-task！
	cleanBodyAll, _ := json.Marshal(protocol.CleanJobsRequest{All: true})
	cleanReqAll := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(cleanBodyAll))
	cleanRecAll := httptest.NewRecorder()
	w.handleCleanJobs(cleanRecAll, cleanReqAll)
	if cleanRecAll.Code != http.StatusOK {
		t.Fatalf("clean all failed: %d", cleanRecAll.Code)
	}
	var respAll protocol.CleanJobsResponse
	_ = json.NewDecoder(cleanRecAll.Body).Decode(&respAll)
	if respAll.CleanedCount != 1 {
		t.Fatalf("expected 1 cleaned job (recent-task), got %d", respAll.CleanedCount)
	}

	// 验证 running 任务严格受到保护
	w.mu.RLock()
	activeJob, exists := w.jobs[runningJob.ID]
	w.mu.RUnlock()
	if !exists {
		t.Fatal("CRITICAL: running job was evicted from memory!")
	}
	if activeJob.GetInfo().Status != protocol.JobStatusRunning {
		t.Fatalf("running job status mutated unexpectedly: %s", activeJob.GetInfo().Status)
	}
	runningDir := filepath.Join(tempDir, "jobs", runningJob.ID)
	if _, err := os.Stat(runningDir); os.IsNotExist(err) {
		t.Fatal("CRITICAL: running job disk directory was deleted!")
	}
}

func TestWorker_RefreshToken(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 初始化生成一个 Token
	t1, err := LoadOrCreateToken(tempDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken failed: %v", err)
	}
	if len(t1) != 64 {
		t.Fatalf("expected 64-char hex token, got %d chars: %s", len(t1), t1)
	}

	// 2. 轮换刷新 Token
	t2, err := RefreshToken(tempDir)
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}
	if len(t2) != 64 {
		t.Fatalf("expected 64-char hex refreshed token, got %d chars", len(t2))
	}
	if t1 == t2 {
		t.Fatal("refreshed token must be distinct from original token")
	}

	// 3. 再次加载验证落盘一致性
	loaded, err := LoadOrCreateToken(tempDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken after refresh failed: %v", err)
	}
	if loaded != t2 {
		t.Fatalf("expected loaded token %s, got %s", t2, loaded)
	}
}

func TestWorker_CollectNodeInfo(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_nodeinfo_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w, err := NewWorker(Config{
		Name:    "node-test",
		Port:    9992,
		DataDir: tempDir,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	info := w.collectNodeInfo()
	if info.Name != "node-test" {
		t.Fatalf("expected node name 'node-test', got '%s'", info.Name)
	}
	if info.Metrics.CPUCores <= 0 {
		t.Fatalf("expected positive CPUCores, got %d", info.Metrics.CPUCores)
	}
	if info.Metrics.MemTotalMB == 0 {
		t.Fatalf("expected non-zero MemTotalMB")
	}
}

func TestWorker_FsRoots_AuthMiddleware(t *testing.T) {
	w := &Worker{
		cfg: Config{
			Token: "secure-roots-token-1234",
		},
	}
	handler := w.authMiddleware(w.handleFsRoots)

	// 1. 无 Token 访问 -> 401
	reqNoAuth := httptest.NewRequest(http.MethodGet, "/api/v1/fs/roots", nil)
	recNoAuth := httptest.NewRecorder()
	handler(recNoAuth, reqNoAuth)
	if recNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", recNoAuth.Code)
	}

	// 2. 错误 Token 访问 -> 401
	reqBadAuth := httptest.NewRequest(http.MethodGet, "/api/v1/fs/roots", nil)
	reqBadAuth.Header.Set("Authorization", "Bearer wrong-token")
	recBadAuth := httptest.NewRecorder()
	handler(recBadAuth, reqBadAuth)
	if recBadAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad token, got %d", recBadAuth.Code)
	}

	// 3. Header Bearer Token 正确访问 -> 200
	reqGoodHeader := httptest.NewRequest(http.MethodGet, "/api/v1/fs/roots", nil)
	reqGoodHeader.Header.Set("Authorization", "Bearer secure-roots-token-1234")
	recGoodHeader := httptest.NewRecorder()
	handler(recGoodHeader, reqGoodHeader)
	if recGoodHeader.Code != http.StatusOK {
		t.Fatalf("expected 200 with Bearer token, got %d", recGoodHeader.Code)
	}

	// 4. Query param token 正确访问 -> 200
	reqGoodQuery := httptest.NewRequest(http.MethodGet, "/api/v1/fs/roots?token=secure-roots-token-1234", nil)
	recGoodQuery := httptest.NewRecorder()
	handler(recGoodQuery, reqGoodQuery)
	if recGoodQuery.Code != http.StatusOK {
		t.Fatalf("expected 200 with query token, got %d", recGoodQuery.Code)
	}
}

func TestWorker_HydrateJobsAndRebootSimulation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_hydrate_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jobsDir := filepath.Join(tempDir, "jobs")
	_ = os.MkdirAll(jobsDir, 0755)

	// 1. 模拟已正常完成的任务 job-completed
	jobCompDir := filepath.Join(jobsDir, "job-comp-1")
	_ = os.MkdirAll(jobCompDir, 0755)
	_ = os.WriteFile(filepath.Join(jobCompDir, "output.log"), []byte("comp task log\n"), 0644)
	compTime := time.Now().Add(-1 * time.Hour)
	_ = process.SaveJobMeta(jobCompDir, protocol.JobInfo{
		ID:        "job-comp-1",
		Name:      "task-comp",
		Command:   "echo comp",
		Status:    protocol.JobStatusCompleted,
		StartTime: compTime,
		EndTime:   &compTime,
		ExitCode:  0,
	})

	// 2. 模拟断电/崩溃遗留的任务 job-crash-running (原状态为 RUNNING)
	jobCrashDir := filepath.Join(jobsDir, "job-crash-2")
	_ = os.MkdirAll(jobCrashDir, 0755)
	_ = os.WriteFile(filepath.Join(jobCrashDir, "output.log"), []byte("running interrupted output\n"), 0644)
	crashStartTime := time.Now().Add(-30 * time.Minute)
	_ = process.SaveJobMeta(jobCrashDir, protocol.JobInfo{
		ID:        "job-crash-2",
		Name:      "task-crash",
		Command:   "ping 127.0.0.1 -n 100",
		Status:    protocol.JobStatusRunning,
		StartTime: crashStartTime,
		PID:       99999,
	})

	// 3. 模拟旧版本遗留的孤儿任务 job-legacy-3 (仅有 output.log，无 job.json)
	jobLegacyDir := filepath.Join(jobsDir, "job-legacy-3")
	_ = os.MkdirAll(jobLegacyDir, 0755)
	_ = os.WriteFile(filepath.Join(jobLegacyDir, "output.log"), []byte("legacy log content\n"), 0644)

	// 4. 初始化 Worker (模拟服务冷启动 / 重启拉起)
	w, err := NewWorker(Config{
		Name:    "test-reboot-node",
		DataDir: tempDir,
		Token:   "test-token",
		Port:    19098,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	// 5. 校验内存中恢复的状态
	w.mu.RLock()
	totalJobs := len(w.jobs)
	jobComp := w.jobs["job-comp-1"]
	jobCrash := w.jobs["job-crash-2"]
	jobLegacy := w.jobs["job-legacy-3"]
	w.mu.RUnlock()

	if totalJobs != 3 {
		t.Fatalf("expected 3 hydrated jobs, got %d", totalJobs)
	}

	// 校验已完成任务保持不变
	if jobComp == nil || jobComp.GetInfo().Status != protocol.JobStatusCompleted {
		t.Errorf("job-comp-1 status unexpected: %+v", jobComp)
	}

	// 校验未完成的 RUNNING 任务被自愈为 STOPPED，且退出码为 -1
	if jobCrash == nil {
		t.Fatal("job-crash-2 not found in memory")
	}
	infoCrash := jobCrash.GetInfo()
	if infoCrash.Status != protocol.JobStatusStopped {
		t.Errorf("job-crash-2 status = %s, want STOPPED", infoCrash.Status)
	}
	if infoCrash.ExitCode != -1 {
		t.Errorf("job-crash-2 exit code = %d, want -1", infoCrash.ExitCode)
	}
	if infoCrash.EndTime == nil {
		t.Error("job-crash-2 EndTime should be set")
	}

	// 验证磁盘上的 job.json 是否已同步更新为 STOPPED
	diskMetaCrash, err := process.LoadJobMeta(jobCrashDir)
	if err != nil {
		t.Fatalf("failed to load job-crash-2 meta from disk: %v", err)
	}
	if diskMetaCrash.Status != protocol.JobStatusStopped || diskMetaCrash.ExitCode != -1 {
		t.Errorf("disk job.json for job-crash-2 mismatch: %+v", diskMetaCrash)
	}

	// 校验旧版本无 job.json 的孤儿目录已被合成并补齐 job.json
	if jobLegacy == nil || jobLegacy.GetInfo().Status != protocol.JobStatusStopped {
		t.Errorf("job-legacy-3 status unexpected: %+v", jobLegacy)
	}
	if _, err := os.Stat(filepath.Join(jobLegacyDir, "job.json")); err != nil {
		t.Errorf("expected job.json to be synthesized for legacy job: %v", err)
	}

	// 6. 测试 handleListJobs 列表接口能正确返回全部 3 个历史任务
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/ps", nil)
	listRec := httptest.NewRecorder()
	w.handleListJobs(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("handleListJobs failed: %d", listRec.Code)
	}
	var jobsList []protocol.JobInfo
	_ = json.NewDecoder(listRec.Body).Decode(&jobsList)
	if len(jobsList) != 3 {
		t.Fatalf("expected 3 jobs in list endpoint, got %d", len(jobsList))
	}

	// 7. 测试 handleGetLogs 能读取历史日志
	logsReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/logs?job_id=job-crash-2", nil)
	logsRec := httptest.NewRecorder()
	w.handleGetLogs(logsRec, logsReq)
	if logsRec.Code != http.StatusOK {
		t.Fatalf("handleGetLogs failed: %d", logsRec.Code)
	}
	if !strings.Contains(logsRec.Body.String(), "running interrupted output") {
		t.Errorf("expected log content in response, got: %s", logsRec.Body.String())
	}
}

func TestWorker_CleanJobs_OrphanAndStopped(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_clean_orphan_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w, err := NewWorker(Config{
		Name:    "clean-test-node",
		DataDir: tempDir,
		Token:   "clean-token",
		Port:    19097,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	// 1. 派发一个正在运行的活跃任务
	runBody, _ := json.Marshal(protocol.RunJobRequest{
		Name:    "active-clean-test",
		Command: "ping 127.0.0.1 -n 30",
	})
	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/run", bytes.NewReader(runBody))
	runRec := httptest.NewRecorder()
	w.handleRunJob(runRec, runReq)
	var activeJob protocol.JobInfo
	_ = json.NewDecoder(runRec.Body).Decode(&activeJob)

	defer func() {
		killBody, _ := json.Marshal(protocol.KillJobRequest{JobID: activeJob.ID})
		killReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/kill", bytes.NewReader(killBody))
		killRec := httptest.NewRecorder()
		w.handleKillJob(killRec, killReq)
	}()

	// 2. 人工塞入一个历史 STOPPED 任务 (模拟重启恢复的非活跃任务)
	stoppedDir := filepath.Join(tempDir, "jobs", "job-clean-stopped")
	_ = os.MkdirAll(stoppedDir, 0755)
	_ = os.WriteFile(filepath.Join(stoppedDir, "output.log"), []byte("stopped log data\n"), 0644)
	stInfo := protocol.JobInfo{
		ID:        "job-clean-stopped",
		Name:      "stopped-task",
		Status:    protocol.JobStatusStopped,
		StartTime: time.Now().Add(-10 * time.Minute),
		ExitCode:  -1,
	}
	_ = process.SaveJobMeta(stoppedDir, stInfo)
	w.mu.Lock()
	w.jobs["job-clean-stopped"] = process.NewHistoricJob(stInfo, stoppedDir)
	w.mu.Unlock()

	// 3. 在磁盘塞入一个完全未进内存的孤儿任务目录
	orphanDir := filepath.Join(tempDir, "jobs", "job-clean-orphan")
	_ = os.MkdirAll(orphanDir, 0755)
	_ = os.WriteFile(filepath.Join(orphanDir, "output.log"), []byte("orphan file data\n"), 0644)

	// 4. 执行全量 clean
	cleanBody, _ := json.Marshal(protocol.CleanJobsRequest{All: true})
	cleanReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(cleanBody))
	cleanRec := httptest.NewRecorder()
	w.handleCleanJobs(cleanRec, cleanReq)
	if cleanRec.Code != http.StatusOK {
		t.Fatalf("handleCleanJobs failed: %d", cleanRec.Code)
	}

	var resp protocol.CleanJobsResponse
	_ = json.NewDecoder(cleanRec.Body).Decode(&resp)

	// 必须清理 2 个任务 (stopped 任务 + 孤儿任务)
	if resp.CleanedCount != 2 {
		t.Fatalf("expected 2 cleaned jobs, got %d", resp.CleanedCount)
	}

	// 验证活跃任务受到严格保护
	w.mu.RLock()
	_, activeExists := w.jobs[activeJob.ID]
	w.mu.RUnlock()
	if !activeExists {
		t.Error("active running job should not be cleaned!")
	}
	if _, err := os.Stat(filepath.Join(tempDir, "jobs", activeJob.ID)); err != nil {
		t.Error("active running job folder should still exist!")
	}

	// 验证历史任务与孤儿目录已在物理磁盘上被删除
	if _, err := os.Stat(stoppedDir); !os.IsNotExist(err) {
		t.Error("stopped job dir should be deleted from disk")
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Error("orphan job dir should be deleted from disk")
	}
}

func TestWorker_StreamLogs_WebSocket_HistoricalAndFallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_ws_hist_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w := &Worker{
		cfg: Config{
			Name:    "ws-hist-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	server := httptest.NewServer(http.HandlerFunc(w.handleStreamLogs))
	defer server.Close()

	// 1. 测试历史任务（在内存中，但 Broadcaster 为 nil，状态为 STOPPED）
	jobDir1 := filepath.Join(tempDir, "jobs", "job-hist-1")
	_ = os.MkdirAll(jobDir1, 0755)
	_ = os.WriteFile(filepath.Join(jobDir1, "output.log"), []byte("historical log output line 1\nline 2\n"), 0644)
	info1 := protocol.JobInfo{
		ID:        "job-hist-1",
		Name:      "hist-task",
		Status:    protocol.JobStatusStopped,
		StartTime: time.Now().Add(-10 * time.Minute),
		ExitCode:  -1,
	}
	w.jobs["job-hist-1"] = process.NewHistoricJob(info1, jobDir1)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel1()

	wsURL1 := strings.Replace(server.URL, "http://", "ws://", 1) + "?job_id=job-hist-1"
	conn1, _, err := websocket.Dial(ctx1, wsURL1, nil)
	if err != nil {
		t.Fatalf("dial historical job websocket failed: %v", err)
	}
	defer conn1.Close(websocket.StatusNormalClosure, "")

	_, msg1, err := conn1.Read(ctx1)
	if err != nil {
		t.Fatalf("read from historical websocket failed: %v", err)
	}
	if !strings.Contains(string(msg1), "historical log output line 1") {
		t.Fatalf("unexpected message: %s", string(msg1))
	}

	// 2. 测试不在内存中（!exists），但磁盘上存在 output.log 的历史孤儿任务
	jobDir2 := filepath.Join(tempDir, "jobs", "job-orphan-2")
	_ = os.MkdirAll(jobDir2, 0755)
	_ = os.WriteFile(filepath.Join(jobDir2, "output.log"), []byte("orphan disk log content\n"), 0644)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	wsURL2 := strings.Replace(server.URL, "http://", "ws://", 1) + "?job_id=job-orphan-2"
	conn2, _, err := websocket.Dial(ctx2, wsURL2, nil)
	if err != nil {
		t.Fatalf("dial orphan job websocket failed: %v", err)
	}
	defer conn2.Close(websocket.StatusNormalClosure, "")

	_, msg2, err := conn2.Read(ctx2)
	if err != nil {
		t.Fatalf("read from orphan websocket failed: %v", err)
	}
	if !strings.Contains(string(msg2), "orphan disk log content") {
		t.Fatalf("unexpected message: %s", string(msg2))
	}
}

func TestWorker_HydrateJobs_CorruptedMetaFallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_worker_hydrate_corrupt_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 在磁盘中构造一个 job.json 损坏（非法 JSON）但包含有效 output.log 的历史任务目录
	corruptDir := filepath.Join(tempDir, "jobs", "job-corrupt-001")
	_ = os.MkdirAll(corruptDir, 0755)
	_ = os.WriteFile(filepath.Join(corruptDir, "job.json"), []byte("{invalid json corrupt content..."), 0644)
	_ = os.WriteFile(filepath.Join(corruptDir, "output.log"), []byte("log of corrupted job\n"), 0644)

	w, err := NewWorker(Config{
		Name:    "corrupt-test-node",
		DataDir: tempDir,
		Port:    19098,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	w.mu.RLock()
	job, exists := w.jobs["job-corrupt-001"]
	w.mu.RUnlock()

	if !exists {
		t.Fatal("corrupted job was not hydrated using fallback!")
	}
	info := job.GetInfo()
	if info.Status != protocol.JobStatusStopped || info.ExitCode != -1 {
		t.Fatalf("expected STOPPED with exit code -1, got status=%s code=%d", info.Status, info.ExitCode)
	}

	// 验证 job.json 已被自愈重写为合法的 JSON
	repairedMeta, err := process.LoadJobMeta(corruptDir)
	if err != nil {
		t.Fatalf("failed to load repaired job.json: %v", err)
	}
	if repairedMeta.Status != protocol.JobStatusStopped || repairedMeta.ExitCode != -1 {
		t.Fatalf("unexpected repaired metadata: %+v", repairedMeta)
	}
}

func TestHandleCleanJobs_SingleJob(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "clean-test-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 1. 已完成任务
	jobDir1 := filepath.Join(tempDir, "jobs", "job-comp-1")
	_ = os.MkdirAll(jobDir1, 0755)
	_ = os.WriteFile(filepath.Join(jobDir1, "output.log"), []byte("output-1"), 0644)
	w.jobs["job-comp-1"] = process.NewHistoricJob(protocol.JobInfo{
		ID:     "job-comp-1",
		Status: protocol.JobStatusCompleted,
	}, jobDir1)

	// 2. 正在运行任务
	w.jobs["job-run-1"] = process.NewHistoricJob(protocol.JobInfo{
		ID:     "job-run-1",
		Status: protocol.JobStatusRunning,
	}, "")

	// 3. 测试正在运行的任务清理被拒绝 (409 Conflict)
	runReqBody, _ := json.Marshal(protocol.CleanJobsRequest{JobID: "job-run-1"})
	runRec := httptest.NewRecorder()
	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(runReqBody))
	w.handleCleanJobs(runRec, runReq)
	if runRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for running job, got %d", runRec.Code)
	}

	// 4. 测试已完成任务正常清理 (200 OK)
	compReqBody, _ := json.Marshal(protocol.CleanJobsRequest{JobID: "job-comp-1"})
	compRec := httptest.NewRecorder()
	compReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(compReqBody))
	w.handleCleanJobs(compRec, compReq)
	if compRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for completed job, got %d: %s", compRec.Code, compRec.Body.String())
	}
	var resp protocol.CleanJobsResponse
	_ = json.Unmarshal(compRec.Body.Bytes(), &resp)
	if resp.CleanedCount != 1 || resp.FreedBytes <= 0 {
		t.Fatalf("unexpected clean response: %+v", resp)
	}
	if _, err := os.Stat(jobDir1); !os.IsNotExist(err) {
		t.Fatalf("expected job directory to be deleted")
	}

	// 5. 测试不存在任务 (404 Not Found)
	missReqBody, _ := json.Marshal(protocol.CleanJobsRequest{JobID: "job-missing-404"})
	missRec := httptest.NewRecorder()
	missReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(missReqBody))
	w.handleCleanJobs(missRec, missReq)
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing job, got %d", missRec.Code)
	}
}

func TestHandleFsCat_And_GetLogs_Slice(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "cat-slice-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 创建测试文件
	filePath := filepath.Join(tempDir, "sample.txt")
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&sb, "line %02d\n", i)
	}
	_ = os.WriteFile(filePath, []byte(sb.String()), 0644)

	// 1. handleFsCat: head 3
	reqHead := httptest.NewRequest(http.MethodGet, "/api/v1/fs/cat?path="+filePath+"&head=3", nil)
	recHead := httptest.NewRecorder()
	w.handleFsCat(recHead, reqHead)
	if recHead.Code != http.StatusOK {
		t.Fatalf("handleFsCat head failed: %d", recHead.Code)
	}
	headLines := strings.Split(strings.TrimSpace(recHead.Body.String()), "\n")
	if len(headLines) != 3 || headLines[0] != "line 01" || headLines[2] != "line 03" {
		t.Fatalf("unexpected head lines: %v", headLines)
	}

	// 2. handleFsCat: tail 3
	reqTail := httptest.NewRequest(http.MethodGet, "/api/v1/fs/cat?path="+filePath+"&tail=3", nil)
	recTail := httptest.NewRecorder()
	w.handleFsCat(recTail, reqTail)
	if recTail.Code != http.StatusOK {
		t.Fatalf("handleFsCat tail failed: %d", recTail.Code)
	}
	tailLines := strings.Split(strings.TrimSpace(recTail.Body.String()), "\n")
	if len(tailLines) != 3 || tailLines[2] != "line 20" {
		t.Fatalf("unexpected tail lines: %v", tailLines)
	}

	// 3. handleGetLogs: range 5:8
	jobDir := filepath.Join(tempDir, "jobs", "job-log-1")
	_ = os.MkdirAll(jobDir, 0755)
	_ = os.WriteFile(filepath.Join(jobDir, "output.log"), []byte(sb.String()), 0644)

	reqLogRange := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/logs?job_id=job-log-1&range=5:8", nil)
	recLogRange := httptest.NewRecorder()
	w.handleGetLogs(recLogRange, reqLogRange)
	if recLogRange.Code != http.StatusOK {
		t.Fatalf("handleGetLogs range failed: %d", recLogRange.Code)
	}
	logLines := strings.Split(strings.TrimSpace(recLogRange.Body.String()), "\n")
	if len(logLines) != 4 || logLines[0] != "line 05" || logLines[3] != "line 08" {
		t.Fatalf("unexpected log range lines: %v", logLines)
	}
}

func TestWorker_HandleFsCat_And_HandleCleanJobs_EdgeCases(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "edge-worker",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 1. handleFsCat: 非 GET 请求 -> 405
	rec405 := httptest.NewRecorder()
	req405 := httptest.NewRequest(http.MethodPost, "/api/v1/fs/cat?path=sample.txt", nil)
	w.handleFsCat(rec405, req405)
	if rec405.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST cat, got %d", rec405.Code)
	}

	// 2. handleFsCat: 不存在文件 -> 404
	rec404 := httptest.NewRecorder()
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/fs/cat?path="+filepath.Join(tempDir, "notfound.txt"), nil)
	w.handleFsCat(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing file, got %d", rec404.Code)
	}

	// 3. handleFsCat: 路径为目录 -> 400
	subDir := filepath.Join(tempDir, "subdir")
	_ = os.MkdirAll(subDir, 0755)
	recDir := httptest.NewRecorder()
	reqDir := httptest.NewRequest(http.MethodGet, "/api/v1/fs/cat?path="+subDir, nil)
	w.handleFsCat(recDir, reqDir)
	if recDir.Code != http.StatusBadRequest || !strings.Contains(recDir.Body.String(), "directory") {
		t.Fatalf("expected 400 for directory cat, got %d: %s", recDir.Code, recDir.Body.String())
	}

	// 4. handleFsCat: 大文件 (>1MB) 截断并包含 X-Content-Truncated 响应头
	largeFilePath := filepath.Join(tempDir, "large_cat.txt")
	lf, err := os.Create(largeFilePath)
	if err != nil {
		t.Fatalf("create large file failed: %v", err)
	}
	lineChunk := strings.Repeat("L", 95) + "\n"
	for i := 0; i < 12000; i++ {
		_, _ = lf.WriteString(lineChunk)
	}
	lf.Close()

	recLarge := httptest.NewRecorder()
	reqLarge := httptest.NewRequest(http.MethodGet, "/api/v1/fs/cat?path="+largeFilePath, nil)
	w.handleFsCat(recLarge, reqLarge)
	if recLarge.Code != http.StatusOK {
		t.Fatalf("expected 200 for large cat, got %d", recLarge.Code)
	}
	if recLarge.Header().Get("X-Content-Truncated") != "true" {
		t.Fatalf("expected X-Content-Truncated header to be true")
	}
	if !strings.Contains(recLarge.Body.String(), "[NOTICE] File size") {
		t.Fatalf("expected notice message in truncated output, got %s", recLarge.Body.String())
	}

	// 4.1 handleFsCat: 大文件附加 all=true 不截断
	recLargeAll := httptest.NewRecorder()
	reqLargeAll := httptest.NewRequest(http.MethodGet, "/api/v1/fs/cat?path="+largeFilePath+"&all=true", nil)
	w.handleFsCat(recLargeAll, reqLargeAll)
	if recLargeAll.Code != http.StatusOK {
		t.Fatalf("expected 200 for large cat with all, got %d", recLargeAll.Code)
	}
	if recLargeAll.Header().Get("X-Content-Truncated") == "true" {
		t.Fatalf("expected X-Content-Truncated to be absent when all=true")
	}

	// 5. handleCleanJobs: 非 POST 请求 -> 405
	recClean405 := httptest.NewRecorder()
	reqClean405 := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/clean", nil)
	w.handleCleanJobs(recClean405, reqClean405)
	if recClean405.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET clean, got %d", recClean405.Code)
	}

	// 6. handleCleanJobs: 非法参数 -> 400
	recClean400 := httptest.NewRecorder()
	reqClean400 := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", strings.NewReader(`{}`))
	w.handleCleanJobs(recClean400, reqClean400)
	if recClean400.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty clean request, got %d", recClean400.Code)
	}

	// 7. handleCleanJobs: 非法 JobID 格式 -> 400
	recCleanBadID := httptest.NewRecorder()
	reqCleanBadID := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", strings.NewReader(`{"job_id": "../invalid-id"}`))
	w.handleCleanJobs(recCleanBadID, reqCleanBadID)
	if recCleanBadID.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad job_id, got %d", recCleanBadID.Code)
	}

	// 8. handleCleanJobs: 孤儿磁盘目录清理 (内存中无 job，但磁盘 jobs/<id> 存在)
	orphanDir := filepath.Join(tempDir, "jobs", "job-orphan-999")
	_ = os.MkdirAll(orphanDir, 0755)
	_ = os.WriteFile(filepath.Join(orphanDir, "output.log"), []byte("orphan logs data"), 0644)

	recOrphan := httptest.NewRecorder()
	reqOrphan := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/clean", strings.NewReader(`{"job_id": "job-orphan-999"}`))
	w.handleCleanJobs(recOrphan, reqOrphan)
	if recOrphan.Code != http.StatusOK {
		t.Fatalf("expected 200 for orphan clean, got %d: %s", recOrphan.Code, recOrphan.Body.String())
	}
	var orphanResp protocol.CleanJobsResponse
	_ = json.Unmarshal(recOrphan.Body.Bytes(), &orphanResp)
	if orphanResp.CleanedCount != 1 || orphanResp.FreedBytes <= 0 {
		t.Fatalf("unexpected orphan clean response: %+v", orphanResp)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("expected orphan dir to be deleted")
	}

	// 9. handleGetLogs: 非法 / 缺失 job_id -> 400
	recLogBad := httptest.NewRecorder()
	reqLogBad := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/logs?job_id=bad/path", nil)
	w.handleGetLogs(recLogBad, reqLogBad)
	if recLogBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad job_id, got %d", recLogBad.Code)
	}

	// 10. handleGetLogs: 大日志文件截断 (>1MB)
	largeJobDir := filepath.Join(tempDir, "jobs", "job-large-log")
	_ = os.MkdirAll(largeJobDir, 0755)
	logFile, _ := os.Create(filepath.Join(largeJobDir, "output.log"))
	for i := 0; i < 12000; i++ {
		_, _ = logFile.WriteString(lineChunk)
	}
	logFile.Close()

	recLargeLog := httptest.NewRecorder()
	reqLargeLog := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/logs?job_id=job-large-log", nil)
	w.handleGetLogs(recLargeLog, reqLargeLog)
	if recLargeLog.Code != http.StatusOK {
		t.Fatalf("expected 200 for large log, got %d", recLargeLog.Code)
	}
	if recLargeLog.Header().Get("X-Content-Truncated") != "true" {
		t.Fatalf("expected X-Content-Truncated for large log")
	}
	if !strings.Contains(recLargeLog.Body.String(), "[NOTICE] Log size") {
		t.Fatalf("expected notice message in log output")
	}
}

func TestWorker_NodeInfo_Version_And_JobsSorting(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "version-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 1. 验证 collectNodeInfo 包含 Version 与 OSVersion
	info := w.collectNodeInfo()
	if info.Version != protocol.Version {
		t.Fatalf("expected Version %q, got %q", protocol.Version, info.Version)
	}
	if !strings.Contains(info.OSVersion, "Windows") {
		t.Fatalf("expected OSVersion to contain 'Windows', got %q", info.OSVersion)
	}

	// 2. 验证 handleListJobs 权威时序下沉 (RUNNING 优先置顶，其余按 StartTime 倒序)
	now := time.Now()
	w.jobs["job-completed-old"] = process.NewHistoricJob(protocol.JobInfo{
		ID:        "job-completed-old",
		Status:    protocol.JobStatusCompleted,
		StartTime: now.Add(-10 * time.Minute),
	}, tempDir)
	w.jobs["job-completed-new"] = process.NewHistoricJob(protocol.JobInfo{
		ID:        "job-completed-new",
		Status:    protocol.JobStatusCompleted,
		StartTime: now.Add(-1 * time.Minute),
	}, tempDir)
	w.jobs["job-running-1"] = process.NewHistoricJob(protocol.JobInfo{
		ID:        "job-running-1",
		Status:    protocol.JobStatusRunning,
		StartTime: now.Add(-5 * time.Minute),
	}, tempDir)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/ps", nil)
	w.handleListJobs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleListJobs failed: %d", rec.Code)
	}

	var jobs []protocol.JobInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("unmarshal jobs failed: %v", err)
	}

	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}

	// 第一位必须是 RUNNING 任务
	if jobs[0].ID != "job-running-1" {
		t.Fatalf("expected first job to be RUNNING 'job-running-1', got %q", jobs[0].ID)
	}
	// 之后必须是较新的 completed 任务
	if jobs[1].ID != "job-completed-new" {
		t.Fatalf("expected second job to be 'job-completed-new', got %q", jobs[1].ID)
	}
	// 最后是较旧的 completed 任务
	if jobs[2].ID != "job-completed-old" {
		t.Fatalf("expected third job to be 'job-completed-old', got %q", jobs[2].ID)
	}
}

func TestWorker_ListJobs_MultipleRunningAndHistoricSorting(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "sort-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	now := time.Now()
	// 构造多组不同状态与启动时间的任务
	w.jobs["job-run-older"] = process.NewHistoricJob(protocol.JobInfo{
		ID:        "job-run-older",
		Status:    protocol.JobStatusRunning,
		StartTime: now.Add(-10 * time.Minute),
	}, tempDir)
	w.jobs["job-run-newer"] = process.NewHistoricJob(protocol.JobInfo{
		ID:        "job-run-newer",
		Status:    protocol.JobStatusRunning,
		StartTime: now.Add(-2 * time.Minute),
	}, tempDir)
	w.jobs["job-failed-mid"] = process.NewHistoricJob(protocol.JobInfo{
		ID:        "job-failed-mid",
		Status:    protocol.JobStatusFailed,
		StartTime: now.Add(-5 * time.Minute),
	}, tempDir)
	w.jobs["job-stopped-newest"] = process.NewHistoricJob(protocol.JobInfo{
		ID:        "job-stopped-newest",
		Status:    protocol.JobStatusStopped,
		StartTime: now.Add(-1 * time.Minute),
	}, tempDir)
	w.jobs["job-completed-oldest"] = process.NewHistoricJob(protocol.JobInfo{
		ID:        "job-completed-oldest",
		Status:    protocol.JobStatusCompleted,
		StartTime: now.Add(-20 * time.Minute),
	}, tempDir)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/ps", nil)
	w.handleListJobs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleListJobs failed: %d", rec.Code)
	}

	var jobs []protocol.JobInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(jobs) != 5 {
		t.Fatalf("expected 5 jobs, got %d", len(jobs))
	}

	// 顺序期望：
	// 0: job-run-newer (RUNNING, -2m)
	// 1: job-run-older (RUNNING, -10m)
	// 2: job-stopped-newest (STOPPED, -1m)
	// 3: job-failed-mid (FAILED, -5m)
	// 4: job-completed-oldest (COMPLETED, -20m)
	expectedIDs := []string{
		"job-run-newer",
		"job-run-older",
		"job-stopped-newest",
		"job-failed-mid",
		"job-completed-oldest",
	}

	for i, expID := range expectedIDs {
		if jobs[i].ID != expID {
			t.Errorf("job[%d] = %q, want %q", i, jobs[i].ID, expID)
		}
	}
}

func TestWorker_StreamLogs_KillOnDisconnect(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "eph-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 1. 启动一个带有 KillOnDisconnect=true 的慢速长任务
	runReq := protocol.RunJobRequest{
		Name:             "ephemeral-long-job",
		Command:          "cmd.exe /c ping -n 10 127.0.0.1 >nul",
		KillOnDisconnect: true,
	}
	jobID := "eph-job-1"
	job, err := process.StartJob(runReq, jobID, w.cfg.Name, tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}
	w.jobs[jobID] = job
	defer job.Kill()

	server := httptest.NewServer(http.HandlerFunc(w.handleStreamLogs))
	defer server.Close()

	// 作为专属看门狗连接 (附加 &watchdog=1)
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "?job_id=" + jobID + "&watchdog=1"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial failed: %v", err)
	}

	// 确保连接已建立且任务仍处于 RUNNING 状态
	if job.GetInfo().Status != protocol.JobStatusRunning {
		t.Fatalf("expected job to be RUNNING initially")
	}

	// 模拟客户端外部硬杀/崩溃：直接粗暴关闭 WebSocket 连接
	_ = conn.Close(websocket.StatusGoingAway, "client terminated")

	// 轮询验证服务端在检测到断开后，毫秒级将任务强杀为 STOPPED
	dead := false
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		if job.GetInfo().Status == protocol.JobStatusStopped {
			dead = true
			break
		}
	}
	if !dead {
		t.Fatalf("expected job to be killed (STOPPED) on disconnect, but got: %s", job.GetInfo().Status)
	}
}

func TestWorker_StreamLogs_CleanOnDisconnect(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "clean-eph-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 启动同时声明了 KillOnDisconnect 与 CleanOnDisconnect 的瞬时任务
	runReq := protocol.RunJobRequest{
		Name:              "clean-eph-job",
		Command:           "cmd.exe /c ping -n 10 127.0.0.1 >nul",
		KillOnDisconnect:  true,
		CleanOnDisconnect: true,
	}
	jobID := "clean-job-1"
	job, err := process.StartJob(runReq, jobID, w.cfg.Name, tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}
	w.jobs[jobID] = job
	defer job.Kill()

	server := httptest.NewServer(http.HandlerFunc(w.handleStreamLogs))
	defer server.Close()

	// 附带 watchdog=1 证明所有权
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "?job_id=" + jobID + "&watchdog=1"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial failed: %v", err)
	}

	// 模拟客户端突发掉线
	_ = conn.Close(websocket.StatusGoingAway, "abrupt disconnect")

	// 验证任务被移出内存且目录被物理清理
	cleaned := false
	dirCleaned := false
	jobDir := filepath.Join(tempDir, "jobs", jobID)
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		w.mu.RLock()
		_, exists := w.jobs[jobID]
		w.mu.RUnlock()
		if !exists {
			cleaned = true
			if _, err := os.Stat(jobDir); os.IsNotExist(err) {
				dirCleaned = true
				break
			}
		}
	}
	if !cleaned {
		t.Fatalf("expected job to be cleaned and removed from w.jobs on disconnect")
	}
	if !dirCleaned {
		t.Errorf("expected job directory %s to be deleted, but it still exists", jobDir)
	}
}

func TestWorker_StreamLogs_SpectatorDisconnectDoesNotKillJob(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "spectator-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 瞬时任务 (带有 KillOnDisconnect)
	runReq := protocol.RunJobRequest{
		Name:             "ephemeral-job-with-spectator",
		Command:          "cmd.exe /c ping -n 10 127.0.0.1 >nul",
		KillOnDisconnect: true,
	}
	jobID := "spec-job-1"
	job, err := process.StartJob(runReq, jobID, w.cfg.Name, tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}
	w.jobs[jobID] = job
	defer job.Kill()

	server := httptest.NewServer(http.HandlerFunc(w.handleStreamLogs))
	defer server.Close()

	// 关键测试：旁观者连接 (例如 Web 控制台或 cw logs -f)，未携带 watchdog=1
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "?job_id=" + jobID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial failed: %v", err)
	}

	// 旁观者关闭连接
	_ = conn.Close(websocket.StatusNormalClosure, "")

	// 延时等待验证：旁观者断开绝不误杀主任务！
	time.Sleep(200 * time.Millisecond)
	if job.GetInfo().Status != protocol.JobStatusRunning {
		t.Fatalf("spectator disconnect must NOT kill ephemeral job, but got: %s", job.GetInfo().Status)
	}
}

func TestWorker_StreamLogs_NormalJobNotKilledOnDisconnect(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "normal-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	// 普通后台任务 (KillOnDisconnect=false)
	runReq := protocol.RunJobRequest{
		Name:             "normal-detached-job",
		Command:          "cmd.exe /c ping -n 10 127.0.0.1 >nul",
		KillOnDisconnect: false,
	}
	jobID := "normal-job-1"
	job, err := process.StartJob(runReq, jobID, w.cfg.Name, tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}
	w.jobs[jobID] = job
	defer job.Kill()

	server := httptest.NewServer(http.HandlerFunc(w.handleStreamLogs))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "?job_id=" + jobID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial failed: %v", err)
	}

	// 客户端断开 (例如用户用 cw logs -f 看看后 Ctrl+C 退出查看)
	_ = conn.Close(websocket.StatusNormalClosure, "")

	// 延时等待确认，任务依然稳定处于 RUNNING
	time.Sleep(200 * time.Millisecond)
	if job.GetInfo().Status != protocol.JobStatusRunning {
		t.Fatalf("normal background job should NOT be killed on disconnect, but got: %s", job.GetInfo().Status)
	}
}

func TestWorker_EphemeralJob_WatchdogNeverConnectedTimeout(t *testing.T) {
	tempDir := t.TempDir()
	w := &Worker{
		cfg: Config{
			Name:    "timeout-node",
			DataDir: tempDir,
		},
		jobs: make(map[string]*process.ManagedJob),
	}

	oldPeriod := watchdogGracePeriod
	watchdogGracePeriod = 100 * time.Millisecond
	defer func() { watchdogGracePeriod = oldPeriod }()

	runReq := protocol.RunJobRequest{
		Name:              "never-connected-eph-job",
		Command:           "cmd.exe /c ping -n 10 127.0.0.1 >nul",
		KillOnDisconnect:  true,
		CleanOnDisconnect: true,
	}
	body, _ := json.Marshal(runReq)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/run", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	w.handleRunJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleRunJob failed: %d", rec.Code)
	}

	var info protocol.JobInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &info)

	// 验证在 100ms 后由于看门狗从未连接，任务被超时自动强杀并清理
	cleaned := false
	jobDir := filepath.Join(tempDir, "jobs", info.ID)
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		w.mu.RLock()
		_, exists := w.jobs[info.ID]
		w.mu.RUnlock()
		if !exists {
			cleaned = true
			if _, statErr := os.Stat(jobDir); os.IsNotExist(statErr) {
				break
			}
		}
	}
	if !cleaned {
		t.Fatalf("expected ephemeral job without watchdog connection to be auto-killed after grace period")
	}
}









