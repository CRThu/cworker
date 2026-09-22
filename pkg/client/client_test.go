package client

import (
	"bytes"
	"context"
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
	"sync/atomic"
	"testing"
	"time"

	"cworker/pkg/pathutil"
	"cworker/pkg/protocol"
	"nhooyr.io/websocket"
)

func TestClient_KnownNodesLedger(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_client_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cli := NewClient()
	cli.dataDir = tempDir

	// 1. 测试空账本加载
	nodes, err := cli.LoadKnownNodes()
	if err != nil {
		t.Fatalf("load empty ledger failed: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(nodes))
	}

	// 2. 写入两个已知节点
	node1 := protocol.KnownNode{
		Name:   "node-1",
		Target: "192.168.1.10:19000",
		Token:  "token-111",
	}
	node2 := protocol.KnownNode{
		Name:   "node-2",
		Target: "node-2.tailnet.ts.net:19000",
		Token:  "token-222",
	}

	if err := cli.SaveKnownNode(node1); err != nil {
		t.Fatalf("save node1 failed: %v", err)
	}
	if err := cli.SaveKnownNode(node2); err != nil {
		t.Fatalf("save node2 failed: %v", err)
	}

	// 3. 重新加载验证
	loaded, err := cli.LoadKnownNodes()
	if err != nil {
		t.Fatalf("reload ledger failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(loaded))
	}
	if loaded["node-1"].Token != "token-111" {
		t.Fatalf("expected token-111, got %s", loaded["node-1"].Token)
	}
	if loaded["node-2"].Target != "node-2.tailnet.ts.net:19000" {
		t.Fatalf("expected target matching, got %s", loaded["node-2"].Target)
	}

	// 4. 测试删除节点
	if err := cli.RemoveKnownNode("node-1"); err != nil {
		t.Fatalf("remove node-1 failed: %v", err)
	}
	loadedAfterRemove, err := cli.LoadKnownNodes()
	if err != nil {
		t.Fatalf("reload after remove failed: %v", err)
	}
	if len(loadedAfterRemove) != 1 {
		t.Fatalf("expected 1 node after remove, got %d", len(loadedAfterRemove))
	}

	// 5. 验证 nodes.json 物理文件存在
	jsonPath := filepath.Join(tempDir, "nodes.json")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("nodes.json file not created on disk: %v", err)
	}
}

func TestClient_ResolveWorker(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_client_resolve_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cli := NewClient()
	cli.dataDir = tempDir

	// 测试从账本解析
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "alpha",
		Target: "127.0.0.1:19000",
		Token:  "secret-alpha",
	})

	rt, err := cli.ResolveWorker("alpha", "")
	if err != nil {
		t.Fatalf("resolve worker 'alpha' failed: %v", err)
	}
	if rt.Token != "secret-alpha" {
		t.Fatalf("expected secret-alpha token, got %s", rt.Token)
	}
	if rt.BaseURL != "http://127.0.0.1:19000" {
		t.Fatalf("expected http://127.0.0.1:19000, got %s", rt.BaseURL)
	}

	// 测试显式传入 Token 覆盖
	rt2, err := cli.ResolveWorker("alpha", "override-token")
	if err != nil {
		t.Fatalf("resolve with override failed: %v", err)
	}
	if rt2.Token != "override-token" {
		t.Fatalf("expected override-token, got %s", rt2.Token)
	}
}

func TestClient_Operations(t *testing.T) {
	// 启动 Mock Worker HTTP 服务器
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// 验证 Bearer Token
		if r.Header.Get("Authorization") != "Bearer mock-token" {
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}

		path := r.URL.Path
		switch {
		case path == "/api/v1/health":
			_ = json.NewEncoder(rw).Encode(protocol.NodeInfo{
				Name:    "mock-node",
				Address: "127.0.0.1:19000",
				Status:  protocol.NodeStatusOnline,
			})
		case path == "/api/v1/fs/upload":
			h := sha256.New()
			_, _ = io.Copy(h, r.Body)
			hashHex := hex.EncodeToString(h.Sum(nil))
			rw.Header().Set("X-File-SHA256", hashHex)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(hashHex))
		case path == "/api/v1/fs/download":
			data := []byte("mock-download-data")
			h := sha256.Sum256(data)
			rw.Header().Set("X-File-SHA256", hex.EncodeToString(h[:]))
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write(data)
		case path == "/api/v1/fs/ls":
			_ = json.NewEncoder(rw).Encode([]protocol.FileInfo{
				{Name: "file.txt", Size: 42, IsDir: false, ModTime: time.Now()},
			})
		case path == "/api/v1/fs/md":
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("CREATED"))
		case path == "/api/v1/fs/rm":
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("DELETED"))
		case path == "/api/v1/jobs/run":
			_ = json.NewEncoder(rw).Encode(protocol.JobInfo{
				ID:      "job-test-1",
				Node:    "mock-node",
				Command: "echo 1",
				Status:  protocol.JobStatusRunning,
			})
		case path == "/api/v1/jobs/ps":
			_ = json.NewEncoder(rw).Encode([]protocol.JobInfo{
				{ID: "job-test-1", Command: "echo 1", Status: protocol.JobStatusRunning},
			})
		case path == "/api/v1/jobs/kill":
			_ = json.NewEncoder(rw).Encode(protocol.JobInfo{
				ID:     "job-test-1",
				Status: protocol.JobStatusStopped,
			})
		case path == "/api/v1/jobs/logs":
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("log line 1\nlog line 2"))
		default:
			http.NotFound(rw, r)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	targetHostPort := u.Host

	tempDir, err := os.MkdirTemp("", "cw_client_ops_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cli := NewClient()
	cli.dataDir = tempDir

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-node",
		Target: targetHostPort,
		Token:  "mock-token",
	})

	// 1. 测试 ListNodes
	nodes, err := cli.ListNodes()
	if err != nil || len(nodes) != 1 {
		t.Fatalf("ListNodes failed: len=%d, err=%v", len(nodes), err)
	}
	if nodes[0].Status != protocol.NodeStatusOnline {
		t.Fatalf("expected node online, got %s", nodes[0].Status)
	}
	if nodes[0].Address != targetHostPort {
		t.Fatalf("expected node address %s, got %s", targetHostPort, nodes[0].Address)
	}

	// 2. 测试 UploadFile
	err = cli.UploadFile("mock-node", "dest.txt", strings.NewReader("upload content"))
	if err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	// 3. 测试 DownloadFile
	var downloadBuf bytes.Buffer
	err = cli.DownloadFile("mock-node", "dest.txt", &downloadBuf)
	if err != nil || downloadBuf.String() != "mock-download-data" {
		t.Fatalf("DownloadFile failed: %v, got %s", err, downloadBuf.String())
	}

	// 3.1 测试 DownloadToLocalFile
	localSavedFile := filepath.Join(tempDir, "saved_dest.txt")
	err = cli.DownloadToLocalFile(context.Background(), "mock-node", "dest.txt", localSavedFile, nil)
	if err != nil {
		t.Fatalf("DownloadToLocalFile failed: %v", err)
	}
	savedContent, err := os.ReadFile(localSavedFile)
	if err != nil || string(savedContent) != "mock-download-data" {
		t.Fatalf("DownloadToLocalFile content mismatch: %v, got %s", err, string(savedContent))
	}

	// 4. 测试 ListDir
	files, err := cli.ListDir("mock-node", ".")
	if err != nil || len(files) != 1 {
		t.Fatalf("ListDir failed: len=%d, err=%v", len(files), err)
	}

	// 5. 测试 MakeDir
	err = cli.MakeDir("mock-node", "new_dir")
	if err != nil {
		t.Fatalf("MakeDir failed: %v", err)
	}

	// 6. 测试 Delete
	err = cli.Delete("mock-node", "new_dir", true)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// 7. 测试 RelayCopy
	err = cli.RelayCopy("mock-node", "src.txt", "mock-node", "dst.txt")
	if err != nil {
		t.Fatalf("RelayCopy failed: %v", err)
	}

	// 8. 测试 RunJob
	job, err := cli.RunJob(protocol.RunJobRequest{
		Node:    "mock-node",
		Command: "echo 1",
	}, "")
	if err != nil || job.ID != "job-test-1" {
		t.Fatalf("RunJob failed: %v", err)
	}

	// 9. 测试 ListJobs
	jobs, err := cli.ListJobs()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobs failed: len=%d, err=%v", len(jobs), err)
	}

	// 10. 测试 KillJob
	killedJob, err := cli.KillJob("job-test-1")
	if err != nil || killedJob.Status != protocol.JobStatusStopped {
		t.Fatalf("KillJob failed: %v", err)
	}

	// 11. 测试 GetLogs
	logs, err := cli.GetLogs("job-test-1", 10)
	if err != nil || !strings.Contains(logs, "log line 1") {
		t.Fatalf("GetLogs failed: %v, logs=%s", err, logs)
	}
}

func TestClient_StreamLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/ps" {
			_ = json.NewEncoder(rw).Encode([]protocol.JobInfo{
				{ID: "stream-job-id", Command: "test", Status: protocol.JobStatusRunning},
			})
			return
		}
		if r.URL.Path == "/api/v1/jobs/stream" {
			conn, err := websocket.Accept(rw, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			_ = conn.Write(r.Context(), websocket.MessageText, []byte("streamed_client_log_chunk\n"))
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	tempDir, err := os.MkdirTemp("", "cw_client_stream_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cli := NewClient()
	cli.dataDir = tempDir
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "stream-node",
		Target: u.Host,
		Token:  "test-token",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var buf bytes.Buffer
	err = cli.StreamLogs(ctx, "stream-job-id", &buf)
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("StreamLogs returned unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "streamed_client_log_chunk") {
		t.Fatalf("expected streamed chunk, got: %s", buf.String())
	}
}

func TestClient_StreamLogs_Over32KB(t *testing.T) {
	// 生成 64KB 大小的数据帧，验证解除 32KB 限制后读取正常
	largePayload := strings.Repeat("A", 64*1024) + "\n"

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/ps" {
			_ = json.NewEncoder(rw).Encode([]protocol.JobInfo{
				{ID: "stream-large-job", Command: "test", Status: protocol.JobStatusRunning},
			})
			return
		}
		if r.URL.Path == "/api/v1/jobs/stream" {
			conn, err := websocket.Accept(rw, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(largePayload))
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	tempDir, err := os.MkdirTemp("", "cw_client_stream_large_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cli := NewClient()
	cli.dataDir = tempDir
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "stream-large-node",
		Target: u.Host,
		Token:  "test-token",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var buf bytes.Buffer
	err = cli.StreamLogs(ctx, "stream-large-job", &buf)
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("StreamLogs over 32KB returned unexpected error: %v", err)
	}
	if len(buf.String()) != len(largePayload) {
		t.Fatalf("expected %d bytes, got: %d", len(largePayload), len(buf.String()))
	}
}

func TestClient_ResolveWorker_IPv6(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_client_ipv6_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cli := NewClient()
	cli.dataDir = tempDir

	// 1. IPv6 带中括号和端口
	rt1, err := cli.ResolveWorker("[2001:db8::1]:19000", "")
	if err != nil {
		t.Fatalf("resolve IPv6 with port failed: %v", err)
	}
	if rt1.BaseURL != "http://[2001:db8::1]:19000" {
		t.Fatalf("expected http://[2001:db8::1]:19000, got %s", rt1.BaseURL)
	}

	// 2. 纯 IP 自动补充 19000
	rt2, err := cli.ResolveWorker("192.168.1.88", "")
	if err != nil {
		t.Fatalf("resolve pure IPv4 failed: %v", err)
	}
	if rt2.BaseURL != "http://192.168.1.88:19000" {
		t.Fatalf("expected http://192.168.1.88:19000, got %s", rt2.BaseURL)
	}
}

func TestClient_ListNodes_OfflineNode(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_client_offline_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cli := NewClient()
	cli.dataDir = tempDir

	// 记录一个不可达的高位端口节点
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "offline-box",
		Target: "127.0.0.1:59998",
		Token:  "token",
	})

	nodes, err := cli.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes should not fail when node is offline: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Status != protocol.NodeStatusOffline {
		t.Fatalf("expected node status OFFLINE, got %s", nodes[0].Status)
	}
	if nodes[0].Address != "127.0.0.1:59998" {
		t.Fatalf("expected offline node address '127.0.0.1:59998', got %s", nodes[0].Address)
	}
}

// TestClient_ListNodes_ProxyImmunity 验证 ListNodes 严格免疫系统外部 HTTP_PROXY 环境变量干扰
func TestClient_ListNodes_ProxyImmunity(t *testing.T) {
	// 强行注入无法访问的黑洞代理地址
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:59999")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:59999")

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.NodeInfo{
				Name:    "proxy-immune-node",
				Address: "127.0.0.1:19000",
				Status:  protocol.NodeStatusOnline,
			})
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "proxy-immune-node",
		Target: u.Host,
	})

	nodes, err := cli.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes failed under HTTP_PROXY: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Status != protocol.NodeStatusOnline {
		t.Fatalf("expected node to be ONLINE ignoring HTTP_PROXY, got: %+v", nodes)
	}
}

// TestClient_ListNodes_TimeoutFailsafe 验证目标节点假死时，3000ms 短超时即时熔断且不阻塞全局
func TestClient_ListNodes_TimeoutFailsafe(t *testing.T) {
	// 模拟挂起延迟 5 秒的假死节点（监听 r.Context().Done() 以便在客户端超时取消后快速释放连接）
	slowServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
			_ = json.NewEncoder(rw).Encode(protocol.NodeInfo{
				Name:   "slow-node",
				Status: protocol.NodeStatusOnline,
			})
		case <-r.Context().Done():
			return
		}
	}))
	defer slowServer.Close()

	u, _ := url.Parse(slowServer.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "slow-node",
		Target: u.Host,
	})

	start := time.Now()
	nodes, err := cli.ListNodes()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ListNodes unexpected error on timeout: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Status != protocol.NodeStatusOffline {
		t.Fatalf("expected slow node to be marked OFFLINE on timeout, got: %+v", nodes)
	}
	// 耗时应在 3000ms 左右，大幅小于服务端的 5 秒延迟
	if elapsed > 4500*time.Millisecond {
		t.Fatalf("ListNodes timeout took too long: %v (expected ~3000ms)", elapsed)
	}
}

// TestClient_ListNodes_AddressSource_SSOT 验证节点的真实物理通信地址始终以客户端动态寻址与握手成功的 BaseURL 为 SSOT，
// 严禁被远端 Worker 内部网卡自报的无效/虚假地址（如 169.254.x.x 链路本地地址或孤岛 IP）污染
func TestClient_ListNodes_AddressSource_SSOT(t *testing.T) {
	// 远端 Worker 模拟：自报了一个完全不可达的 APIPA 假地址
	deceptiveWorkerIP := "169.254.164.158:19000"
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.NodeInfo{
				Name:    "worker-reporting-bad-ip",
				Address: deceptiveWorkerIP,
				Status:  protocol.NodeStatusOnline,
			})
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "worker-node",
		Target: u.Host,
		Token:  "test-token",
	})

	nodes, err := cli.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	n := nodes[0]
	if n.Status != protocol.NodeStatusOnline {
		t.Fatalf("expected node status ONLINE, got %s", n.Status)
	}

	// 核心断言：Address 必须是客户端实际连接的真实物理 HostPort (u.Host)，绝对不能是远端自报的 deceptiveWorkerIP
	if n.Address != u.Host {
		t.Fatalf("Address SSOT violation: expected client connect host %s, got %s", u.Host, n.Address)
	}
	if n.Address == deceptiveWorkerIP {
		t.Fatalf("Address SSOT failure: node Address was polluted by remote worker's self-reported IP: %s", deceptiveWorkerIP)
	}
}

// TestClient_ListNodes_ResolveError_Offline 验证当节点寻址/解析彻底异常时，优雅降级为 OFFLINE 并保留 Target，绝不挂死或静默丢失
func TestClient_ListNodes_ResolveError_Offline(t *testing.T) {
	cli := NewClient()
	cli.dataDir = t.TempDir()

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "unresolvable-node",
		Target: "nonexistent.domain.that.does.not.exist.at.all:19000",
		Token:  "some-token",
	})

	nodes, err := cli.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes failed on unresolvable node: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Status != protocol.NodeStatusOffline {
		t.Fatalf("expected OFFLINE, got %s", nodes[0].Status)
	}
	if nodes[0].Address != "nonexistent.domain.that.does.not.exist.at.all:19000" {
		t.Fatalf("expected preserved target, got %s", nodes[0].Address)
	}
}

// TestClient_ListNodes_IPv6 验证 IPv6 目标节点的标准括号包裹格式与 Address SSOT 对齐
func TestClient_ListNodes_IPv6(t *testing.T) {
	cli := NewClient()
	cli.dataDir = t.TempDir()

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "ipv6-node",
		Target: "[::1]:59996",
		Token:  "token",
	})

	nodes, err := cli.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Status != protocol.NodeStatusOffline {
		t.Fatalf("expected OFFLINE for unused port, got %s", nodes[0].Status)
	}
	if nodes[0].Address != "[::1]:59996" {
		t.Fatalf("expected [::1]:59996, got %s", nodes[0].Address)
	}
}

func TestClient_ErrorResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		http.Error(rw, "simulated error", http.StatusInternalServerError)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	tempDir, err := os.MkdirTemp("", "cw_client_err_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cli := NewClient()
	cli.dataDir = tempDir
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "err-node",
		Target: u.Host,
	})

	// 各接口应正确捕获非 200 返回值错误
	if err := cli.UploadFile("err-node", "a.txt", strings.NewReader("")); err == nil {
		t.Fatal("expected error on 500 in UploadFile")
	}
	var buf bytes.Buffer
	if err := cli.DownloadFile("err-node", "a.txt", &buf); err == nil {
		t.Fatal("expected error on 500 in DownloadFile")
	}
	if _, err := cli.ListDir("err-node", "."); err == nil {
		t.Fatal("expected error on 500 in ListDir")
	}
	if err := cli.MakeDir("err-node", "sub"); err == nil {
		t.Fatal("expected error on 500 in MakeDir")
	}
	if err := cli.Delete("err-node", "sub", false); err == nil {
		t.Fatal("expected error on 500 in Delete")
	}
	if _, err := cli.RunJob(protocol.RunJobRequest{Node: "err-node", Command: "echo 1"}, ""); err == nil {
		t.Fatal("expected error on 500 in RunJob")
	}
	if _, err := cli.KillJob("fake-job"); err == nil {
		t.Fatal("expected error on 500 in KillJob")
	}
	if _, err := cli.GetLogs("fake-job", 10); err == nil {
		t.Fatal("expected error on 500 in GetLogs")
	}
}

func TestClient_DownloadFile_Sha256Mismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("X-File-SHA256", "0000000000000000000000000000000000000000000000000000000000000000")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("some data that does not match dummy hash"))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "hash-node",
		Target: u.Host,
	})

	var buf bytes.Buffer
	err := cli.DownloadFile("hash-node", "file.txt", &buf)
	if err == nil || !strings.Contains(err.Error(), "sha256 checksum mismatch") {
		t.Fatalf("expected sha256 checksum mismatch error, got: %v", err)
	}
}

func TestClient_UploadDir_SuccessAndPreserveEmptyDirs(t *testing.T) {
	var mu sync.Mutex
	createdDirs := make(map[string]bool)
	uploadedFiles := make(map[string]string)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		pQuery := r.URL.Query().Get("path")
		switch path {
		case "/api/v1/fs/md":
			mu.Lock()
			createdDirs[pQuery] = true
			mu.Unlock()
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("CREATED"))
		case "/api/v1/fs/upload":
			h := sha256.New()
			body, _ := io.ReadAll(io.TeeReader(r.Body, h))
			hashHex := hex.EncodeToString(h.Sum(nil))
			mu.Lock()
			uploadedFiles[pQuery] = string(body)
			mu.Unlock()
			rw.Header().Set("X-File-SHA256", hashHex)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("OK"))
		default:
			http.NotFound(rw, r)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "upload-node", Target: u.Host})

	// 构建本地复杂嵌套测试目录
	localRoot := t.TempDir()
	_ = os.WriteFile(filepath.Join(localRoot, "root_file.txt"), []byte("root content"), 0644)
	subDir := filepath.Join(localRoot, "sub")
	_ = os.MkdirAll(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "sub_file.txt"), []byte("sub content"), 0644)
	emptySub := filepath.Join(subDir, "empty_sub")
	_ = os.MkdirAll(emptySub, 0755)
	emptyRoot := filepath.Join(localRoot, "empty_root")
	_ = os.MkdirAll(emptyRoot, 0755)

	tracker := NewProgressTracker(2, int64(len("root content")+len("sub content")))
	tracker.SetTTY(false)

	err := cli.UploadDir(context.Background(), "upload-node", "D:/remote_target", localRoot, 4, tracker)
	if err != nil {
		t.Fatalf("UploadDir failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// 验证空目录与普通目录全部在远端预建
	if !createdDirs["D:/remote_target"] {
		t.Fatal("expected remote root dir created")
	}
	if !createdDirs["D:/remote_target/sub"] {
		t.Fatal("expected sub dir created")
	}
	if !createdDirs["D:/remote_target/sub/empty_sub"] {
		t.Fatal("expected empty_sub dir preserved")
	}
	if !createdDirs["D:/remote_target/empty_root"] {
		t.Fatal("expected empty_root dir preserved")
	}

	// 验证文件上传路径与内容
	if uploadedFiles["D:/remote_target/root_file.txt"] != "root content" {
		t.Fatalf("root_file.txt content mismatch: %s", uploadedFiles["D:/remote_target/root_file.txt"])
	}
	if uploadedFiles["D:/remote_target/sub/sub_file.txt"] != "sub content" {
		t.Fatalf("sub_file.txt content mismatch: %s", uploadedFiles["D:/remote_target/sub/sub_file.txt"])
	}
}

func TestClient_UploadDir_EarlyCancelOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/v1/fs/md" {
			rw.WriteHeader(http.StatusOK)
			return
		}
		if path == "/api/v1/fs/upload" {
			http.Error(rw, "disk full", http.StatusInternalServerError)
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "err-upload-node", Target: u.Host})

	localRoot := t.TempDir()
	_ = os.WriteFile(filepath.Join(localRoot, "file1.txt"), []byte("content1"), 0644)
	_ = os.WriteFile(filepath.Join(localRoot, "file2.txt"), []byte("content2"), 0644)

	err := cli.UploadDir(context.Background(), "err-upload-node", "D:/target", localRoot, 2, nil)
	if err == nil || !strings.Contains(err.Error(), "upload") {
		t.Fatalf("expected upload failure error, got %v", err)
	}
}

func TestClient_DownloadDir_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch path {
		case "/api/v1/fs/ls":
			list := []protocol.FileInfo{
				{Name: "file1.txt", Path: "file1.txt", IsDir: false, Size: 6},
				{Name: "sub", Path: "sub", IsDir: true},
				{Name: "file2.txt", Path: "sub/file2.txt", IsDir: false, Size: 6},
				{Name: "empty_dir", Path: "empty_dir", IsDir: true},
			}
			_ = json.NewEncoder(rw).Encode(list)
		case "/api/v1/fs/download":
			pQuery := r.URL.Query().Get("path")
			var content string
			if strings.HasSuffix(pQuery, "file1.txt") {
				content = "data_1"
			} else if strings.HasSuffix(pQuery, "file2.txt") {
				content = "data_2"
			}
			h := sha256.Sum256([]byte(content))
			hashHex := hex.EncodeToString(h[:])
			rw.Header().Set("X-File-Size", fmt.Sprintf("%d", len(content)))
			rw.Header().Set("X-File-SHA256", hashHex)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(content))
		default:
			http.NotFound(rw, r)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "dl-node", Target: u.Host})

	localDest := t.TempDir()
	tracker := NewProgressTracker(2, 12)
	tracker.SetTTY(false)

	err := cli.DownloadDir(context.Background(), "dl-node", "D:/remote_source", localDest, 4, tracker)
	if err != nil {
		t.Fatalf("DownloadDir failed: %v", err)
	}

	// 验证下载文件内容与空目录
	c1, err := os.ReadFile(filepath.Join(localDest, "file1.txt"))
	if err != nil || string(c1) != "data_1" {
		t.Fatalf("file1.txt content mismatch: %s, err: %v", string(c1), err)
	}

	c2, err := os.ReadFile(filepath.Join(localDest, "sub", "file2.txt"))
	if err != nil || string(c2) != "data_2" {
		t.Fatalf("sub/file2.txt content mismatch: %s, err: %v", string(c2), err)
	}

	emptyFi, err := os.Stat(filepath.Join(localDest, "empty_dir"))
	if err != nil || !emptyFi.IsDir() {
		t.Fatalf("empty_dir was not preserved: %v", err)
	}
}

func TestClient_DownloadDir_EarlyCancelOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/v1/fs/ls" {
			list := []protocol.FileInfo{
				{Name: "f1.txt", Path: "f1.txt", IsDir: false, Size: 10},
			}
			_ = json.NewEncoder(rw).Encode(list)
			return
		}
		if path == "/api/v1/fs/download" {
			http.Error(rw, "corrupt block", http.StatusInternalServerError)
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "err-dl-node", Target: u.Host})

	localDest := t.TempDir()
	err := cli.DownloadDir(context.Background(), "err-dl-node", "D:/remote", localDest, 2, nil)
	if err == nil || !strings.Contains(err.Error(), "download") {
		t.Fatalf("expected download failure error, got %v", err)
	}
}

func TestClient_DownloadDir_FailureDoesNotDestroyExistingFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/v1/fs/ls" {
			list := []protocol.FileInfo{
				{Name: "critical.txt", Path: "critical.txt", IsDir: false, Size: 10},
			}
			_ = json.NewEncoder(rw).Encode(list)
			return
		}
		if path == "/api/v1/fs/download" {
			http.Error(rw, "remote download exploded", http.StatusInternalServerError)
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "err-dl-node2", Target: u.Host})

	localDest := t.TempDir()
	targetFile := filepath.Join(localDest, "critical.txt")
	originalContent := "original-local-critical-content"
	if err := os.WriteFile(targetFile, []byte(originalContent), 0644); err != nil {
		t.Fatalf("write original file failed: %v", err)
	}

	err := cli.DownloadDir(context.Background(), "err-dl-node2", "D:/remote", localDest, 2, nil)
	if err == nil {
		t.Fatal("expected error from DownloadDir, got nil")
	}

	// 校验目标文件未被截断或删除，依然完好
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("target file was deleted: %v", err)
	}
	if string(content) != originalContent {
		t.Fatalf("target file content altered! expected %q, got %q", originalContent, string(content))
	}

	// 校验临时文件已被清理
	entries, err := os.ReadDir(localDest)
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".cwtemp-") {
			t.Fatalf("temporary download file was leaked: %s", e.Name())
		}
	}
}

func TestClient_RelayCopyDir_Success(t *testing.T) {
	var mu sync.Mutex
	dstCreatedDirs := make(map[string]bool)
	dstFiles := make(map[string]string)

	// 1. 源端 Mock Worker
	srcServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch path {
		case "/api/v1/fs/ls":
			list := []protocol.FileInfo{
				{Name: "fileA.txt", Path: "fileA.txt", IsDir: false, Size: 9},
				{Name: "empty_folder", Path: "empty_folder", IsDir: true},
			}
			_ = json.NewEncoder(rw).Encode(list)
		case "/api/v1/fs/download":
			content := "data-from-src"
			h := sha256.Sum256([]byte(content))
			rw.Header().Set("X-File-Size", fmt.Sprintf("%d", len(content)))
			rw.Header().Set("X-File-SHA256", hex.EncodeToString(h[:]))
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(content))
		default:
			http.NotFound(rw, r)
		}
	}))
	defer srcServer.Close()

	// 2. 目的端 Mock Worker
	dstServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		pQuery := r.URL.Query().Get("path")
		switch path {
		case "/api/v1/fs/md":
			mu.Lock()
			dstCreatedDirs[pQuery] = true
			mu.Unlock()
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("CREATED"))
		case "/api/v1/fs/upload":
			h := sha256.New()
			body, _ := io.ReadAll(io.TeeReader(r.Body, h))
			hashHex := hex.EncodeToString(h.Sum(nil))
			mu.Lock()
			dstFiles[pQuery] = string(body)
			mu.Unlock()
			rw.Header().Set("X-File-SHA256", hashHex)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("OK"))
		default:
			http.NotFound(rw, r)
		}
	}))
	defer dstServer.Close()

	uSrc, _ := url.Parse(srcServer.URL)
	uDst, _ := url.Parse(dstServer.URL)

	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "relay-src", Target: uSrc.Host})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "relay-dst", Target: uDst.Host})

	tracker := NewProgressTracker(1, 13)
	tracker.SetTTY(false)

	err := cli.RelayCopyDir(context.Background(), "relay-src", "D:/src_dir", "relay-dst", "D:/dst_dir", 2, tracker)
	if err != nil {
		t.Fatalf("RelayCopyDir failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if !dstCreatedDirs["D:/dst_dir"] {
		t.Fatal("expected dst base dir created")
	}
	if !dstCreatedDirs["D:/dst_dir/empty_folder"] {
		t.Fatal("expected dst empty_folder created")
	}
	if dstFiles["D:/dst_dir/fileA.txt"] != "data-from-src" {
		t.Fatalf("fileA.txt content mismatch: %s", dstFiles["D:/dst_dir/fileA.txt"])
	}
}

func TestClient_DownloadDir_Security_PathTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/fs/ls" {
			list := []protocol.FileInfo{
				{Name: "evil.txt", Path: "../../evil.txt", IsDir: false, Size: 10},
			}
			_ = json.NewEncoder(rw).Encode(list)
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "rogue-worker", Target: u.Host})

	targetDir := t.TempDir()
	err := cli.DownloadDir(context.Background(), "rogue-worker", "D:/data", targetDir, 2, nil)
	if err == nil {
		t.Fatal("expected security path traversal error, got nil")
	}
	if !strings.Contains(err.Error(), "security") && !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 确认未在父级逃逸创建文件
	parentEvil := filepath.Join(targetDir, "..", "evil.txt")
	if _, statErr := os.Stat(parentEvil); !os.IsNotExist(statErr) {
		t.Fatalf("evil.txt was written outside target dir: %s", parentEvil)
	}
}

func TestClient_RelayCopyDir_Security_PathTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/fs/ls" {
			list := []protocol.FileInfo{
				{Name: "evil.txt", Path: "../escaped/evil.txt", IsDir: false, Size: 10},
			}
			_ = json.NewEncoder(rw).Encode(list)
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "rogue-src", Target: u.Host})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "dst-node", Target: u.Host})

	err := cli.RelayCopyDir(context.Background(), "rogue-src", "D:/src", "dst-node", "D:/dst", 2, nil)
	if err == nil {
		t.Fatal("expected security error on relay copy dir, got nil")
	}
	if !strings.Contains(err.Error(), "security") && !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestClient_DirOperations_ContextCanceled(t *testing.T) {
	cli := NewClient()
	cli.dataDir = t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预先取消

	// 1. UploadDir 面对已取消 context 必须显式报错而不是吞掉返回 nil
	err := cli.UploadDir(ctx, "any-node", "D:/remote", t.TempDir(), 2, nil)
	if err == nil {
		t.Fatal("expected error on cancelled ctx in UploadDir, got nil")
	}

	// 2. DownloadDir 面对已取消 context 必须显式报错
	err = cli.DownloadDir(ctx, "any-node", "D:/remote", t.TempDir(), 2, nil)
	if err == nil {
		t.Fatal("expected error on cancelled ctx in DownloadDir, got nil")
	}

	// 3. RelayCopyDir 面对已取消 context 必须显式报错
	err = cli.RelayCopyDir(ctx, "src-node", "D:/src", "dst-node", "D:/dst", 2, nil)
	if err == nil {
		t.Fatal("expected error on cancelled ctx in RelayCopyDir, got nil")
	}
}

func TestClient_GetLogs_ErrorPropagation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/jobs/ps":
			jobs := []protocol.JobInfo{
				{ID: "job-exist-err", Status: protocol.JobStatusFailed},
			}
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(jobs)
		case "/api/v1/jobs/logs":
			http.Error(rw, "log file corrupted", http.StatusInternalServerError)
		default:
			http.NotFound(rw, r)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "err-node", Target: u.Host})

	_, err := cli.GetLogs("job-exist-err", 50)
	if err == nil {
		t.Fatal("expected error when server returns 500 on GetLogs, got nil")
	}
	if !strings.Contains(err.Error(), "get logs failed (500)") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestClient_SSOT_EmptyLedgerReturnsEmpty(t *testing.T) {
	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir

	// 当账本为空时，LoadKnownNodes 严格遵从 SSOT 返回空映射，不伪造隐式节点
	nodes, err := cli.LoadKnownNodes()
	if err != nil {
		t.Fatalf("LoadKnownNodes failed: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected exactly 0 nodes (empty ledger), got %d", len(nodes))
	}
}

func TestClient_GetRoots_Local(t *testing.T) {
	cli := NewClient()
	roots, err := cli.GetRoots("")
	if err != nil {
		t.Fatalf("GetRoots failed: %v", err)
	}
	if len(roots) == 0 {
		t.Fatal("expected at least 1 root drive for local, got 0")
	}
}

func TestClient_GetRoots_Remote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/fs/roots" {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode([]string{"C:/", "D:/"})
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()
	u, _ := url.Parse(server.URL)
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-remote",
		Target: u.Host,
	})

	roots, err := cli.GetRoots("mock-remote")
	if err != nil {
		t.Fatalf("GetRoots remote failed: %v", err)
	}
	if len(roots) != 2 || roots[0] != "C:/" || roots[1] != "D:/" {
		t.Fatalf("unexpected remote roots: %v", roots)
	}
}

func TestClient_GetRoots_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		http.Error(rw, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()
	targetHost := strings.TrimPrefix(server.URL, "http://")
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "err-roots-node",
		Target: targetHost,
	})

	_, err := cli.GetRoots("err-roots-node")
	if err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}

	// 目标节点解析失败或无法连通
	_, err = cli.GetRoots("invalid-target:99999")
	if err == nil {
		t.Fatal("expected error on bad host port, got nil")
	}
}

func TestClient_UploadFile_Sha256Mismatch_Rollback(t *testing.T) {
	var deleteCalled atomic.Bool
	var deletePath string
	var deleteLock sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/rm") {
			deleteLock.Lock()
			deleteCalled.Store(true)
			deletePath = r.URL.Query().Get("path")
			deleteLock.Unlock()
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("DELETED"))
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/upload") {
			// 故意返回错误的 SHA-256 头部模拟传输篡改/校验不匹配
			rw.Header().Set("X-File-SHA256", "wrong_hash_11223344556677889900aabbccddeeff")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("OK"))
			return
		}

		rw.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "rollback-node", Target: u.Host})

	err := cli.UploadFile("rollback-node", "D:/test/dest.txt", strings.NewReader("sample payload"))
	if err == nil {
		t.Fatal("expected error on sha256 mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "sha256 checksum mismatch") || !strings.Contains(err.Error(), "remote file deleted") {
		t.Fatalf("unexpected error message: %v", err)
	}

	if !deleteCalled.Load() {
		t.Fatal("expected Delete to be called to rollback remote file, but it was not called")
	}
	deleteLock.Lock()
	if deletePath != "D:/test/dest.txt" {
		t.Fatalf("expected delete path D:/test/dest.txt, got %s", deletePath)
	}
	deleteLock.Unlock()

	// 再次测试服务端完全未返回 X-File-SHA256 头部的情况
	deleteCalled.Store(false)
	serverNoHash := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/rm") {
			deleteCalled.Store(true)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("DELETED"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/upload") {
			// 不返回 X-File-SHA256 头部
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("OK"))
			return
		}
	}))
	defer serverNoHash.Close()

	uNoHash, _ := url.Parse(serverNoHash.URL)
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "nohash-node", Target: uNoHash.Host})
	errNoHash := cli.UploadFile("nohash-node", "D:/test/nohash.txt", strings.NewReader("payload"))
	if errNoHash == nil || !strings.Contains(errNoHash.Error(), "did not return X-File-SHA256 header") {
		t.Fatalf("expected missing header rollback error, got: %v", errNoHash)
	}
	if !deleteCalled.Load() {
		t.Fatal("expected Delete to be called when worker omits X-File-SHA256 header")
	}
}

func TestClient_RelayCopy_Sha256Mismatch_Rollback(t *testing.T) {
	content := "relay_payload_data"
	h := sha256.New()
	h.Write([]byte(content))
	realHash := hex.EncodeToString(h.Sum(nil))

	var deleteCalled atomic.Bool

	// 1. 模拟目标节点返回错误 Hash
	srcServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("X-File-Size", fmt.Sprintf("%d", len(content)))
		rw.Header().Set("X-File-SHA256", realHash)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(content))
	}))
	defer srcServer.Close()

	dstServerWrongHash := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/rm") {
			deleteCalled.Store(true)
			rw.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/upload") {
			rw.Header().Set("X-File-SHA256", "tampered_dst_hash")
			rw.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer dstServerWrongHash.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()

	srcURL, _ := url.Parse(srcServer.URL)
	dstURL, _ := url.Parse(dstServerWrongHash.URL)

	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "src-node", Target: srcURL.Host})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "dst-node", Target: dstURL.Host})

	err := cli.RelayCopy("src-node", "src.txt", "dst-node", "dst.txt")
	if err == nil || !strings.Contains(err.Error(), "dst file rolled back") {
		t.Fatalf("expected relay copy mismatch rollback error, got: %v", err)
	}
	if !deleteCalled.Load() {
		t.Fatal("expected Delete to be called on dst worker when dst hash mismatches")
	}

	// 2. 模拟源节点返回错误 Hash
	deleteCalled.Store(false)
	srcServerWrongHash := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("X-File-Size", fmt.Sprintf("%d", len(content)))
		rw.Header().Set("X-File-SHA256", "wrong_src_hash")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(content))
	}))
	defer srcServerWrongHash.Close()

	dstServerGood := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/rm") {
			deleteCalled.Store(true)
			rw.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/upload") {
			rw.Header().Set("X-File-SHA256", realHash)
			rw.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer dstServerGood.Close()

	srcWrongURL, _ := url.Parse(srcServerWrongHash.URL)
	dstGoodURL, _ := url.Parse(dstServerGood.URL)
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "src-wrong", Target: srcWrongURL.Host})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "dst-good", Target: dstGoodURL.Host})

	err = cli.RelayCopy("src-wrong", "src.txt", "dst-good", "dst.txt")
	if err == nil || !strings.Contains(err.Error(), "dst file rolled back") {
		t.Fatalf("expected relay copy src mismatch rollback error, got: %v", err)
	}
	if !deleteCalled.Load() {
		t.Fatal("expected Delete to be called on dst worker when src hash mismatches")
	}
}

func TestClient_LoadKnownNodes_CorruptedJson(t *testing.T) {
	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir

	nodesFile := filepath.Join(tempDir, "nodes.json")
	// 写入损坏的非合法 JSON
	if err := os.WriteFile(nodesFile, []byte("{corrupted-json-data:"), 0644); err != nil {
		t.Fatalf("write corrupted json failed: %v", err)
	}

	// 验证加载返回错误且不发生 panic
	nodes, err := cli.LoadKnownNodes()
	if err == nil {
		t.Fatal("expected error on corrupted nodes.json, got nil")
	}
	if nodes != nil {
		t.Fatalf("expected nil nodes on corruption, got: %v", nodes)
	}
}

func TestClient_JoinRemotePath_EdgeCases(t *testing.T) {
	cases := []struct {
		base     string
		rel      string
		expected string
	}{
		{"D:/folder", "sub/file.txt", "D:/folder/sub/file.txt"},
		{"D:\\folder\\", "\\sub\\file.txt", "D:/folder/sub/file.txt"},
		{"", "rel/file.txt", "rel/file.txt"},
		{"/", "rel/file.txt", "/rel/file.txt"},
		{"/var/data", "", "/var/data"},
		{"", "", ""},
		{"/", "", "/"},
	}

	for _, tc := range cases {
		got := pathutil.JoinRemotePath(tc.base, tc.rel)
		if got != tc.expected {
			t.Errorf("pathutil.JoinRemotePath(%q, %q) = %q, want %q", tc.base, tc.rel, got, tc.expected)
		}
	}
}

// TestClient_RunJob_AutoMemorizeToken 验证显式传入 Token 且派发成功后自动记忆持久化入本地账本
func TestClient_RunJob_AutoMemorizeToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer auto-remember-token-xyz" {
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/v1/jobs/run" {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.JobInfo{
				ID:     "job-auto-remember",
				Node:   "new-auto-node",
				Status: protocol.JobStatusRunning,
			})
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := NewClient()
	cli.dataDir = t.TempDir()

	// 此时账本中尚未存在该节点
	nodesBefore, _ := cli.LoadKnownNodes()
	if _, ok := nodesBefore["new-auto-node"]; ok {
		t.Fatal("node should not exist in ledger before run")
	}

	// 派发任务并显式传入 Token
	job, err := cli.RunJob(protocol.RunJobRequest{
		Node:    u.Host,
		Name:    "test-task",
		Command: "echo ok",
	}, "auto-remember-token-xyz")
	if err != nil {
		t.Fatalf("RunJob failed: %v", err)
	}
	if job.ID != "job-auto-remember" {
		t.Fatalf("unexpected job info: %+v", job)
	}

	// 验证账本中已自动持久化记忆该节点与 Token
	nodesAfter, err := cli.LoadKnownNodes()
	if err != nil {
		t.Fatalf("LoadKnownNodes failed: %v", err)
	}
	kn, ok := nodesAfter[strings.ToLower(u.Host)]
	if !ok {
		t.Fatalf("expected node %q to be memorized in ledger, got: %+v", u.Host, nodesAfter)
	}
	if kn.Token != "auto-remember-token-xyz" {
		t.Fatalf("expected memorized token 'auto-remember-token-xyz', got: %q", kn.Token)
	}
}

func TestClient_HashLocalPath(t *testing.T) {
	tempDir := t.TempDir()
	file1 := filepath.Join(tempDir, "f1.txt")
	content1 := []byte("content_hash_1")
	if err := os.WriteFile(file1, content1, 0644); err != nil {
		t.Fatal(err)
	}

	// 1. 单文件哈希
	res, err := HashLocalPath(file1, false)
	if err != nil {
		t.Fatalf("HashLocalPath failed: %v", err)
	}
	if len(res) != 1 || res[0].Size != int64(len(content1)) {
		t.Fatalf("unexpected hash res: %+v", res)
	}
	expectedHash := sha256.Sum256(content1)
	if res[0].SHA256 != hex.EncodeToString(expectedHash[:]) {
		t.Fatalf("sha256 mismatch: expected %s, got %s", hex.EncodeToString(expectedHash[:]), res[0].SHA256)
	}

	// 2. 目录未加 recursive=true 报错
	_, err = HashLocalPath(tempDir, false)
	if err == nil || !strings.Contains(err.Error(), "requires recursive flag (-r)") {
		t.Fatalf("expected recursive flag error, got: %v", err)
	}

	// 3. 目录递归哈希
	subDir := filepath.Join(tempDir, "sub")
	_ = os.MkdirAll(subDir, 0755)
	file2 := filepath.Join(subDir, "f2.txt")
	_ = os.WriteFile(file2, []byte("content_hash_2"), 0644)

	resDir, err := HashLocalPath(tempDir, true)
	if err != nil {
		t.Fatalf("HashLocalPath dir failed: %v", err)
	}
	if len(resDir) != 2 {
		t.Fatalf("expected 2 files in dir, got %d", len(resDir))
	}

	// 4. 不存在路径报错
	_, err = HashLocalPath(filepath.Join(tempDir, "non_existent"), false)
	if err == nil {
		t.Fatal("expected error for non existent path")
	}
}

func TestClient_CompareSingleFile(t *testing.T) {
	f1 := protocol.FileInfo{
		Name:   "file.txt",
		Size:   100,
		SHA256: "hash123",
	}
	f2Identical := protocol.FileInfo{
		Name:   "file.txt",
		Size:   100,
		SHA256: "hash123",
	}
	f3DiffHash := protocol.FileInfo{
		Name:   "file.txt",
		Size:   100,
		SHA256: "hash456",
	}
	f4DiffSize := protocol.FileInfo{
		Name:   "file.txt",
		Size:   200,
		SHA256: "hash123",
	}

	resMatch := CompareSingleFile(f1, f2Identical)
	if resMatch.Matched != 1 || resMatch.Modified != 0 || resMatch.Entries[0].Status != protocol.DiffStatusMatch {
		t.Fatalf("expected match, got: %+v", resMatch)
	}

	resDiffHash := CompareSingleFile(f1, f3DiffHash)
	if resDiffHash.Matched != 0 || resDiffHash.Modified != 1 || resDiffHash.Entries[0].Status != protocol.DiffStatusModified {
		t.Fatalf("expected modified on hash diff, got: %+v", resDiffHash)
	}

	resDiffSize := CompareSingleFile(f1, f4DiffSize)
	if resDiffSize.Matched != 0 || resDiffSize.Modified != 1 || resDiffSize.Entries[0].Status != protocol.DiffStatusModified {
		t.Fatalf("expected modified on size diff, got: %+v", resDiffSize)
	}
}

func TestClient_CompareFileInfos(t *testing.T) {
	src := []protocol.FileInfo{
		{Path: "same.txt", Size: 10, SHA256: "hashA"},
		{Path: "modified_hash.txt", Size: 20, SHA256: "hashB"},
		{Path: "modified_size.txt", Size: 30, SHA256: "hashC"},
		{Path: "added.txt", Size: 40, SHA256: "hashD"},
	}
	dst := []protocol.FileInfo{
		{Path: "same.txt", Size: 10, SHA256: "hashA"},
		{Path: "modified_hash.txt", Size: 20, SHA256: "hashB_diff"},
		{Path: "modified_size.txt", Size: 35, SHA256: "hashC_diff"},
		{Path: "deleted.txt", Size: 50, SHA256: "hashE"},
	}

	res := CompareFileInfos(src, dst)
	if res.Matched != 1 {
		t.Fatalf("expected 1 matched, got %d", res.Matched)
	}
	if res.Modified != 2 {
		t.Fatalf("expected 2 modified, got %d", res.Modified)
	}
	if res.Added != 1 {
		t.Fatalf("expected 1 added, got %d", res.Added)
	}
	if res.Deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", res.Deleted)
	}
	if len(res.Entries) != 5 {
		t.Fatalf("expected 5 total entries, got %d", len(res.Entries))
	}
}

func TestClient_HashRemotePath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/fs/hash" {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("path") == "error" {
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte("bad request"))
			return
		}
		list := []protocol.FileInfo{
			{Name: "test.txt", Path: "test.txt", Size: 123, SHA256: "testhash"},
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(list)
	}))
	defer server.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()

	u, _ := url.Parse(server.URL)
	list, err := cli.HashRemotePath(context.Background(), u.Host, "some/path", false)
	if err != nil {
		t.Fatalf("HashRemotePath failed: %v", err)
	}
	if len(list) != 1 || list[0].SHA256 != "testhash" {
		t.Fatalf("unexpected remote hash response: %+v", list)
	}

	_, err = cli.HashRemotePath(context.Background(), u.Host, "error", false)
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("expected bad request error, got: %v", err)
	}
}

// 验证 HashRemotePath 对 NDJSON 流式协议的解析以及对 ProgressTracker 的驱动
func TestClient_HashRemotePath_NDJSON_Stream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/fs/hash" {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("path") == "stream_err" {
			rw.Header().Set("Content-Type", "application/x-ndjson")
			rw.WriteHeader(http.StatusOK)
			flusher := rw.(http.Flusher)
			flusher.Flush()
			ev := protocol.FsHashEvent{
				Event: protocol.FsHashEventError,
				Error: "fatal permission denied",
			}
			_ = json.NewEncoder(rw).Encode(ev)
			flusher.Flush()
			return
		}

		rw.Header().Set("Content-Type", "application/x-ndjson")
		rw.WriteHeader(http.StatusOK)
		flusher := rw.(http.Flusher)
		flusher.Flush()

		enc := json.NewEncoder(rw)
		// 1. Init event
		_ = enc.Encode(protocol.FsHashEvent{
			Event:      protocol.FsHashEventInit,
			TotalFiles: 2,
			TotalBytes: 3000,
		})
		flusher.Flush()

		// 2. Progress event
		_ = enc.Encode(protocol.FsHashEvent{
			Event:       protocol.FsHashEventProgress,
			CurrentFile: "file1.bin",
			DoneBytes:   500,
			TotalBytes:  1000,
		})
		flusher.Flush()

		// 3. Entry 1
		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventEntry,
			Entry: &protocol.FileInfo{
				Name:   "file1.bin",
				Path:   "file1.bin",
				Size:   1000,
				SHA256: "hash111",
			},
		})
		flusher.Flush()

		// 4. Entry 2
		_ = enc.Encode(protocol.FsHashEvent{
			Event: protocol.FsHashEventEntry,
			Entry: &protocol.FileInfo{
				Name:   "file2.bin",
				Path:   "sub/file2.bin",
				Size:   2000,
				SHA256: "hash222",
			},
		})
		flusher.Flush()

		// 5. Done event
		_ = enc.Encode(protocol.FsHashEvent{
			Event:      protocol.FsHashEventDone,
			TotalFiles: 2,
			TotalBytes: 3000,
		})
		flusher.Flush()
	}))
	defer server.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()

	u, _ := url.Parse(server.URL)
	tracker := NewProgressTracker(0, 0)
	tracker.SetOutput(io.Discard)

	list, err := cli.HashRemotePath(context.Background(), u.Host, "test/dir", true, tracker)
	if err != nil {
		t.Fatalf("HashRemotePath NDJSON failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 items, got %d", len(list))
	}
	if list[0].SHA256 != "hash111" || list[1].SHA256 != "hash222" {
		t.Fatalf("unexpected hash list: %+v", list)
	}

	snap := tracker.Snapshot()
	if snap.TotalFiles != 2 || snap.CompletedFiles != 2 || snap.TotalBytes != 3000 || snap.TransferredBytes != 3000 {
		t.Fatalf("unexpected tracker snapshot: %+v", snap)
	}

	// 测试流式错误事件抛出强类型错误
	_, err = cli.HashRemotePath(context.Background(), u.Host, "stream_err", true)
	if err == nil || !strings.Contains(err.Error(), "fatal permission denied") {
		t.Fatalf("expected remote error event propagation, got: %v", err)
	}
}

// 验证新版本客户端连接旧版本 Worker (仅返回普通 application/json) 时的完全向下兼容性与进度回退驱动
func TestClient_HashRemotePath_LegacyWorker_Compatibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/fs/hash" {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		// 模拟老版本 Worker：严格返回 Content-Type: application/json 与普通 JSON 数组
		rw.Header().Set("Content-Type", "application/json")
		list := []protocol.FileInfo{
			{Name: "legacy1.txt", Path: "legacy1.txt", Size: 500, SHA256: "hash_legacy_1"},
			{Name: "legacy2.txt", Path: "sub/legacy2.txt", Size: 1500, SHA256: "hash_legacy_2"},
		}
		_ = json.NewEncoder(rw).Encode(list)
	}))
	defer server.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()

	u, _ := url.Parse(server.URL)
	tracker := NewProgressTracker(0, 0)
	tracker.SetOutput(io.Discard)

	list, err := cli.HashRemotePath(context.Background(), u.Host, "legacy/dir", true, tracker)
	if err != nil {
		t.Fatalf("HashRemotePath against legacy worker failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 items from legacy worker, got %d", len(list))
	}
	if list[0].SHA256 != "hash_legacy_1" || list[1].SHA256 != "hash_legacy_2" {
		t.Fatalf("unexpected legacy hash list: %+v", list)
	}

	// 验证 tracker 即使面对老 Worker 也能正确补齐 totals 并完成
	snap := tracker.Snapshot()
	if snap.TotalFiles != 2 || snap.CompletedFiles != 2 || snap.TotalBytes != 2000 || snap.TransferredBytes != 2000 {
		t.Fatalf("unexpected tracker snapshot on legacy worker: %+v", snap)
	}
}

// 验证在弱网、中途断开、乱码包或服务端突发故障时的容错与安全退出
func TestClient_HashRemotePath_NetworkFailuresAndResilience(t *testing.T) {
	// Case 1: 弱网/服务端挂死，客户端 Context 超时迅速中断退出 (不泄漏协程与句柄)
	t.Run("ContextTimeoutMidway", func(t *testing.T) {
		hangServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/x-ndjson")
			rw.WriteHeader(http.StatusOK)
			flusher := rw.(http.Flusher)
			flusher.Flush()
			// 发送首包后故意阻塞网络
			_ = json.NewEncoder(rw).Encode(protocol.FsHashEvent{
				Event:      protocol.FsHashEventInit,
				TotalFiles: 10,
				TotalBytes: 10000,
			})
			flusher.Flush()
			<-r.Context().Done()
		}))
		defer hangServer.Close()

		cli := NewClient()
		cli.dataDir = t.TempDir()
		u, _ := url.Parse(hangServer.URL)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := cli.HashRemotePath(ctx, u.Host, "timeout/path", true)
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("expected error on timed out context, got nil")
		}
		if elapsed > 300*time.Millisecond {
			t.Errorf("expected fast timeout exit < 300ms, took %v", elapsed)
		}
	})

	// Case 2: 传输中途网络掉线/连接重置 (Broken Stream)
	t.Run("AbruptConnectionDrop", func(t *testing.T) {
		dropServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/x-ndjson")
			rw.WriteHeader(http.StatusOK)
			flusher := rw.(http.Flusher)
			flusher.Flush()

			// 发送首包后直接暴力 Hijack 连接并关闭 TCP Socket
			hj, ok := rw.(http.Hijacker)
			if !ok {
				t.Fatal("server does not support hijacking")
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}))
		defer dropServer.Close()

		cli := NewClient()
		cli.dataDir = t.TempDir()
		u, _ := url.Parse(dropServer.URL)

		_, err := cli.HashRemotePath(context.Background(), u.Host, "drop/path", true)
		if err == nil {
			t.Fatal("expected error on broken stream, got nil")
		}
	})

	// Case 3: 网络抖动出现个别 JSON 脏行 (自动跳过坏行并继续解析后续有效行)
	t.Run("MalformedLineTolerance", func(t *testing.T) {
		dirtyServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/x-ndjson")
			rw.WriteHeader(http.StatusOK)
			flusher := rw.(http.Flusher)
			flusher.Flush()

			_, _ = rw.Write([]byte("{\"event\":\"init\",\"total_files\":1,\"total_bytes\":100}\n"))
			flusher.Flush()
			// 插入破损脏行
			_, _ = rw.Write([]byte("{this is corrupted json packet\n"))
			flusher.Flush()
			// 插入有效条目
			_, _ = rw.Write([]byte("{\"event\":\"entry\",\"entry\":{\"name\":\"ok.txt\",\"path\":\"ok.txt\",\"size\":100,\"sha256\":\"good_hash\"}}\n"))
			flusher.Flush()
			_, _ = rw.Write([]byte("{\"event\":\"done\",\"total_files\":1,\"total_bytes\":100}\n"))
			flusher.Flush()
		}))
		defer dirtyServer.Close()

		cli := NewClient()
		cli.dataDir = t.TempDir()
		u, _ := url.Parse(dirtyServer.URL)

		list, err := cli.HashRemotePath(context.Background(), u.Host, "dirty/path", true)
		if err != nil {
			t.Fatalf("expected graceful tolerance on dirty lines, got error: %v", err)
		}
		if len(list) != 1 || list[0].SHA256 != "good_hash" {
			t.Fatalf("expected 1 valid entry parsed, got: %+v", list)
		}
	})

	// Case 4: 远端计算中途抛出显式 error 事件 (例如磁盘故障或只读权限拦截)
	t.Run("RemoteWorkerReportedErrorEvent", func(t *testing.T) {
		errServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/x-ndjson")
			rw.WriteHeader(http.StatusOK)
			flusher := rw.(http.Flusher)
			flusher.Flush()

			_, _ = rw.Write([]byte("{\"event\":\"init\",\"total_files\":2,\"total_bytes\":200}\n"))
			flusher.Flush()
			_, _ = rw.Write([]byte("{\"event\":\"entry\",\"entry\":{\"name\":\"f1.txt\",\"path\":\"f1.txt\",\"size\":100,\"sha256\":\"hash1\"}}\n"))
			flusher.Flush()
			// 模拟中途发生 I/O 错误
			_, _ = rw.Write([]byte("{\"event\":\"error\",\"error\":\"disk I/O failure on remote sector 4096\"}\n"))
			flusher.Flush()
		}))
		defer errServer.Close()

		cli := NewClient()
		cli.dataDir = t.TempDir()
		u, _ := url.Parse(errServer.URL)

		_, err := cli.HashRemotePath(context.Background(), u.Host, "err/path", true)
		if err == nil {
			t.Fatal("expected error on remote error event, got nil")
		}
		if !strings.Contains(err.Error(), "remote hash error: disk I/O failure on remote sector 4096") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	// Case 5: 物理网络不可达 / 连接拒绝 (零挂起，即刻抛错)
	t.Run("ConnectionRefusedOrDeadHost", func(t *testing.T) {
		cli := NewClient()
		cli.dataDir = t.TempDir()

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := cli.HashRemotePath(ctx, "127.0.0.1:59998", "dead/path", true)
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("expected error connecting to dead host, got nil")
		}
		if elapsed > 1*time.Second {
			t.Errorf("expected quick refusal error, took %v", elapsed)
		}
	})
}

// 验证客户端对 5000 级海量批量文件 NDJSON 流的持续读取、ProgressTracker 驱动与比对性能
func TestClient_HashRemotePath_LargeBatch_5000Files(t *testing.T) {
	const batchCount = 5000
	const perFileSize = 128
	const totalExpectedBytes = int64(batchCount * perFileSize)

	batchServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/x-ndjson")
		rw.WriteHeader(http.StatusOK)
		flusher, _ := rw.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}

		enc := json.NewEncoder(rw)
		_ = enc.Encode(protocol.FsHashEvent{
			Event:      protocol.FsHashEventInit,
			TotalFiles: batchCount,
			TotalBytes: totalExpectedBytes,
		})
		if flusher != nil {
			flusher.Flush()
		}

		for i := 1; i <= batchCount; i++ {
			p := fmt.Sprintf("dir_%02d/sub/file_%04d.dat", i%50, i)
			_ = enc.Encode(protocol.FsHashEvent{
				Event: protocol.FsHashEventEntry,
				Entry: &protocol.FileInfo{
					Name:   filepath.Base(p),
					Path:   p,
					Size:   perFileSize,
					SHA256: fmt.Sprintf("%064x", i),
				},
			})
			// 每 200 个刷新一次缓冲
			if i%200 == 0 && flusher != nil {
				flusher.Flush()
			}
		}

		_ = enc.Encode(protocol.FsHashEvent{
			Event:      protocol.FsHashEventDone,
			TotalFiles: batchCount,
			TotalBytes: totalExpectedBytes,
		})
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer batchServer.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()
	u, _ := url.Parse(batchServer.URL)

	tracker := NewProgressTracker(0, 0)
	tracker.SetLabel("Hashed")

	start := time.Now()
	list, err := cli.HashRemotePath(context.Background(), u.Host, "big/batch", true, tracker)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("HashRemotePath 5000 files failed: %v", err)
	}
	if len(list) != batchCount {
		t.Fatalf("expected %d files received, got %d", batchCount, len(list))
	}
	snap := tracker.Snapshot()
	if snap.CompletedFiles != batchCount {
		t.Fatalf("expected tracker.CompletedFiles %d, got %d", batchCount, snap.CompletedFiles)
	}
	if snap.TransferredBytes != totalExpectedBytes {
		t.Fatalf("expected tracker.TransferredBytes %d, got %d", totalExpectedBytes, snap.TransferredBytes)
	}

	// 验证 5000 个文件的内存级差分计算极速完成 (< 100ms)
	diffStart := time.Now()
	diffRes := CompareFileInfos(list, list)
	diffElapsed := time.Since(diffStart)

	if diffRes.Matched != batchCount || diffRes.Modified != 0 || diffRes.Added != 0 || diffRes.Deleted != 0 {
		t.Fatalf("unexpected diff result on identical 5000 files: %+v", diffRes)
	}
	if diffElapsed > 200*time.Millisecond {
		t.Errorf("CompareFileInfos on 5000 items took too long: %v", diffElapsed)
	}
	t.Logf("Streaming and parsing 5000 files took %v, diffing took %v", elapsed, diffElapsed)
}


// 验证多文件、大文件、空目录与嵌套多层级混合场景端到端比对
func TestClient_Hash_MixedHierarchy_EndToEnd(t *testing.T) {
	tempDir := t.TempDir()

	// 构建复合层级结构:
	// 1. 根目录空文件 (0 字节)
	_ = os.WriteFile(filepath.Join(tempDir, "empty.txt"), []byte(""), 0644)
	// 2. 根目录小文件
	_ = os.WriteFile(filepath.Join(tempDir, "small.txt"), []byte("small file text"), 0644)
	// 3. 空子目录 (应被跳过不产生 entry)
	_ = os.MkdirAll(filepath.Join(tempDir, "empty_sub"), 0755)
	// 4. 多层深层嵌套子目录文件
	deepDir := filepath.Join(tempDir, "a", "b", "c")
	_ = os.MkdirAll(deepDir, 0755)
	_ = os.WriteFile(filepath.Join(deepDir, "deep.txt"), []byte("deep file content"), 0644)
	// 5. 大文件 (11MB，触发分块心跳)
	largeFile := filepath.Join(deepDir, "large.bin")
	lf, err := os.Create(largeFile)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("K"), 1024*1024)
	for i := 0; i < 11; i++ {
		_, _ = lf.Write(chunk)
	}
	_ = lf.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()

	tracker := NewProgressTracker(0, 0)
	tracker.SetOutput(io.Discard)

	list, err := cli.Hash(context.Background(), "", tempDir, true, tracker)
	if err != nil {
		t.Fatalf("Hash mixed hierarchy failed: %v", err)
	}

	// 必须包含 4 个有效文件 (empty.txt, small.txt, deep.txt, large.bin)
	if len(list) != 4 {
		t.Fatalf("expected 4 files in mixed hierarchy, got %d: %+v", len(list), list)
	}

	snap := tracker.Snapshot()
	if snap.TotalFiles != 4 || snap.CompletedFiles != 4 {
		t.Fatalf("unexpected tracker snapshot files: %+v", snap)
	}
	expectedTotalBytes := int64(0 + len("small file text") + len("deep file content") + 11*1024*1024)
	if snap.TotalBytes != expectedTotalBytes || snap.TransferredBytes != expectedTotalBytes {
		t.Fatalf("expected total bytes %d, got %d in snapshot", expectedTotalBytes, snap.TotalBytes)
	}
}



func TestClient_LocalCopy(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	dstDir := filepath.Join(tempDir, "dst")
	_ = os.MkdirAll(filepath.Join(srcDir, "sub"), 0755)

	content1 := []byte("local copy file 1")
	content2 := []byte("local copy file 2 in sub")
	_ = os.WriteFile(filepath.Join(srcDir, "f1.txt"), content1, 0644)
	_ = os.WriteFile(filepath.Join(srcDir, "sub", "f2.txt"), content2, 0644)

	cli := NewClient()

	// 1. 测试单文件本地拷贝
	singleDst := filepath.Join(tempDir, "single_dst.txt")
	if err := cli.LocalCopyFile(filepath.Join(srcDir, "f1.txt"), singleDst, nil); err != nil {
		t.Fatalf("LocalCopyFile failed: %v", err)
	}
	read1, err := os.ReadFile(singleDst)
	if err != nil || string(read1) != string(content1) {
		t.Fatalf("LocalCopyFile content mismatch: %v, got %s", err, string(read1))
	}

	// 2. 测试目录本地并发拷贝
	if err := cli.LocalCopyDir(context.Background(), srcDir, dstDir, 4, nil); err != nil {
		t.Fatalf("LocalCopyDir failed: %v", err)
	}
	readSub, err := os.ReadFile(filepath.Join(dstDir, "sub", "f2.txt"))
	if err != nil || string(readSub) != string(content2) {
		t.Fatalf("LocalCopyDir nested content mismatch: %v, got %s", err, string(readSub))
	}
}

func TestClient_LocalOperations(t *testing.T) {
	tempDir := t.TempDir()

	// 1. MakeLocalDir
	targetDir := filepath.Join(tempDir, "x", "y", "z")
	if err := MakeLocalDir(targetDir); err != nil {
		t.Fatalf("MakeLocalDir failed: %v", err)
	}
	if fi, err := os.Stat(targetDir); err != nil || !fi.IsDir() {
		t.Fatalf("directory was not created by MakeLocalDir")
	}

	// 2. ListLocalDir
	file1 := filepath.Join(targetDir, "f1.txt")
	_ = os.WriteFile(file1, []byte("content"), 0644)
	subDir := filepath.Join(targetDir, "sub")
	_ = os.Mkdir(subDir, 0755)

	entries, err := ListLocalDir(targetDir)
	if err != nil {
		t.Fatalf("ListLocalDir failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in ListLocalDir, got %d", len(entries))
	}

	// 3. DeleteLocal
	// Non-empty dir without recursive -> should fail
	if err := DeleteLocal(targetDir, false); err == nil {
		t.Fatal("expected error deleting non-empty dir without recursive")
	}

	// Delete file
	if err := DeleteLocal(file1, false); err != nil {
		t.Fatalf("DeleteLocal file failed: %v", err)
	}

	// Delete remaining with recursive
	if err := DeleteLocal(targetDir, true); err != nil {
		t.Fatalf("DeleteLocal recursive failed: %v", err)
	}
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Fatalf("targetDir should be deleted")
	}
}

func TestClient_CleanJobs(t *testing.T) {
	var requestedClean protocol.CleanJobsRequest
	var requestCount int
	var mu sync.Mutex

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()

		if r.URL.Path != "/api/v1/jobs/clean" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&requestedClean)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.CleanJobsResponse{
			CleanedCount: 2,
			FreedBytes:   2048,
		})
	}))
	defer mockServer.Close()

	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-clean",
		Target: strings.TrimPrefix(mockServer.URL, "http://"),
		Token:  "valid-token",
	})

	// 1. 定向节点成功清理
	resp, err := cli.CleanJobs("node-clean", 3, false)
	if err != nil {
		t.Fatalf("CleanJobs targeted failed: %v", err)
	}
	if requestedClean.Days != 3 || requestedClean.All != false {
		t.Fatalf("unexpected request payload: %+v", requestedClean)
	}
	if nodeResp, ok := resp["node-clean"]; !ok || nodeResp.CleanedCount != 2 || nodeResp.FreedBytes != 2048 {
		t.Fatalf("unexpected clean response: %+v", resp)
	}

	// 2. 全集群清理
	respAll, err := cli.CleanJobs("", 0, true)
	if err != nil {
		t.Fatalf("CleanJobs all failed: %v", err)
	}
	if nodeResp, ok := respAll["node-clean"]; !ok || nodeResp.CleanedCount != 2 {
		t.Fatalf("unexpected cluster clean response: %+v", respAll)
	}

	// 3. 鉴权失败测试
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-badauth",
		Target: strings.TrimPrefix(mockServer.URL, "http://"),
		Token:  "wrong-token",
	})
	if _, err := cli.CleanJobs("node-badauth", 0, true); err == nil {
		t.Fatal("expected error on 401 unauthorized")
	}

	// 4. 不存在的节点
	if _, err := cli.CleanJobs("node-nonexistent", 0, true); err == nil {
		t.Fatal("expected error on nonexistent node")
	}
}

func TestClient_ListJobs_TargetFilter(t *testing.T) {
	var node1Hits, node2Hits atomic.Int32

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node1Hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]protocol.JobInfo{
			{ID: "job-node1", Name: "task1", Status: protocol.JobStatusRunning},
		})
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node2Hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]protocol.JobInfo{
			{ID: "job-node2", Name: "task2", Status: protocol.JobStatusCompleted},
		})
	}))
	defer s2.Close()

	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-1",
		Target: strings.TrimPrefix(s1.URL, "http://"),
		Token:  "t1",
	})
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-2",
		Target: strings.TrimPrefix(s2.URL, "http://"),
		Token:  "t2",
	})

	// 1. 仅定向查询 node-1
	jobs1, err := cli.ListJobs("node-1")
	if err != nil {
		t.Fatalf("ListJobs node-1 failed: %v", err)
	}
	if len(jobs1) != 1 || jobs1[0].ID != "job-node1" {
		t.Fatalf("unexpected jobs returned for node-1: %+v", jobs1)
	}
	if node1Hits.Load() != 1 || node2Hits.Load() != 0 {
		t.Fatalf("expected only node-1 to be hit, got node1=%d, node2=%d", node1Hits.Load(), node2Hits.Load())
	}

	// 2. 全集群查询
	jobsAll, err := cli.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs all failed: %v", err)
	}
	if len(jobsAll) != 2 {
		t.Fatalf("expected 2 jobs from cluster, got %d", len(jobsAll))
	}
}

func TestClient_ListJobs_NodeNormalization(t *testing.T) {
	// 模拟远程 Worker 返回其内部物理主机名
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]protocol.JobInfo{
			{
				ID:      "job-test-norm",
				Node:    "RAW-WORKER-HOSTNAME", // Worker 自报主机名
				Command: "echo test",
				Status:  protocol.JobStatusCompleted,
			},
		})
	}))
	defer s.Close()

	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "canonical-node",
		Target: strings.TrimPrefix(s.URL, "http://"),
		Token:  "valid-token",
	})

	// 1. 定向查询
	jobs, err := cli.ListJobs("canonical-node")
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Node != "canonical-node" {
		t.Errorf("expected job.Node to be normalized to 'canonical-node', got '%s'", jobs[0].Node)
	}

	// 2. 全集群查询
	clusterJobs, err := cli.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs cluster failed: %v", err)
	}
	if len(clusterJobs) != 1 {
		t.Fatalf("expected 1 cluster job, got %d", len(clusterJobs))
	}
	if clusterJobs[0].Node != "canonical-node" {
		t.Errorf("expected cluster job.Node to be normalized to 'canonical-node', got '%s'", clusterJobs[0].Node)
	}
}

func TestClient_ResolveTargetForJob_Strict(t *testing.T) {
	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "carrot-work",
		Target: "100.93.237.16:19000",
		Token:  "token-secret",
	})

	// 1. 精确匹配账本节点 (支持大小写忽略)
	rt, err := cli.resolveTargetForJob("CARROT-WORK", "job-1")
	if err != nil {
		t.Fatalf("resolveTargetForJob failed: %v", err)
	}
	if rt.Name != "carrot-work" || rt.Token != "token-secret" {
		t.Errorf("unexpected rt: %+v", rt)
	}

	// 2. 未在账本中的节点，拒绝静默兜底，必须显式报错
	_, err = cli.resolveTargetForJob("unknown-node", "job-1")
	if err == nil {
		t.Fatal("expected error for unknown node, got nil")
	}
	if !strings.Contains(err.Error(), "not found in known nodes ledger") {
		t.Errorf("expected 'not found in known nodes ledger' error, got: %v", err)
	}
}

func TestClient_KillJobNode(t *testing.T) {
	var killedID string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/kill" {
			var req protocol.KillJobRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			killedID = req.JobID
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(protocol.JobInfo{
				ID:     req.JobID,
				Node:   "RAW-HOST",
				Status: protocol.JobStatusStopped,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer s.Close()

	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "kill-target",
		Target: strings.TrimPrefix(s.URL, "http://"),
		Token:  "token-kill",
	})

	info, err := cli.KillJobNode("kill-target", "job-target-99")
	if err != nil {
		t.Fatalf("KillJobNode failed: %v", err)
	}
	if killedID != "job-target-99" {
		t.Errorf("expected killed job ID 'job-target-99', got '%s'", killedID)
	}
	if info.Node != "kill-target" {
		t.Errorf("expected info.Node normalized to 'kill-target', got '%s'", info.Node)
	}
}

func TestClient_RunJob_NodeNormalization(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/run" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(protocol.JobInfo{
				ID:      "job-run-norm",
				Node:    "RAW-WORKER-INTERNAL-HOST",
				Command: "ping 127.0.0.1",
				Status:  protocol.JobStatusRunning,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer s.Close()

	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "run-target",
		Target: strings.TrimPrefix(s.URL, "http://"),
		Token:  "token-run",
	})

	info, err := cli.RunJob(protocol.RunJobRequest{
		Node:    "run-target",
		Command: "ping 127.0.0.1",
	}, "")
	if err != nil {
		t.Fatalf("RunJob failed: %v", err)
	}
	if info.Node != "run-target" {
		t.Errorf("expected info.Node normalized to 'run-target', got '%s'", info.Node)
	}
}

// 验证 HTTP 客户端双轨设计：RPC 超时 30s 防死锁，流式客户端超时 0 由 Context 约束
func TestClient_HTTPClients_TimeoutConfiguration(t *testing.T) {
	cli := NewClient()
	if cli.httpClient == nil {
		t.Fatal("httpClient is nil")
	}
	if cli.httpClient.Timeout != 30*time.Second {
		t.Errorf("expected httpClient.Timeout to be 30s, got %v", cli.httpClient.Timeout)
	}
	if cli.streamClient == nil {
		t.Fatal("streamClient is nil")
	}
	if cli.streamClient.Timeout != 0 {
		t.Errorf("expected streamClient.Timeout to be 0 (unbounded), got %v", cli.streamClient.Timeout)
	}
}

// 验证单一内核下 Client 的统一文件系统接口 (node == "")
func TestClient_UnifiedLocalFS(t *testing.T) {
	cli := NewClient()
	tempDir := t.TempDir()

	testFolder := filepath.Join(tempDir, "nested", "folder")
	// 1. MakeDirWithContext
	if err := cli.MakeDirWithContext(context.Background(), "", testFolder); err != nil {
		t.Fatalf("MakeDirWithContext failed: %v", err)
	}
	if info, err := os.Stat(testFolder); err != nil || !info.IsDir() {
		t.Fatalf("expected folder to exist, err: %v", err)
	}

	// 2. 写入文件并用 Cat 输出
	filePath := filepath.Join(testFolder, "test.txt")
	expectedContent := "hello single kernel filesystem"
	if err := os.WriteFile(filePath, []byte(expectedContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	var buf bytes.Buffer
	if err := cli.Cat(context.Background(), "", filePath, &buf); err != nil {
		t.Fatalf("Cat failed: %v", err)
	}
	if buf.String() != expectedContent {
		t.Errorf("expected Cat content '%s', got '%s'", expectedContent, buf.String())
	}

	// 3. ListDirWithContext
	entries, err := cli.ListDirWithContext(context.Background(), "", testFolder)
	if err != nil {
		t.Fatalf("ListDirWithContext failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "test.txt" {
		t.Fatalf("expected 1 entry 'test.txt', got %v", entries)
	}

	// 4. Hash
	hashEntries, err := cli.Hash(context.Background(), "", testFolder, true)
	if err != nil {
		t.Fatalf("Hash failed: %v", err)
	}
	if len(hashEntries) != 1 || hashEntries[0].SHA256 == "" {
		t.Fatalf("expected 1 hashed file, got %v", hashEntries)
	}

	// 5. DeleteWithContext
	if err := cli.DeleteWithContext(context.Background(), "", filePath, false); err != nil {
		t.Fatalf("DeleteWithContext file failed: %v", err)
	}
	if err := cli.DeleteWithContext(context.Background(), "", testFolder, true); err != nil {
		t.Fatalf("DeleteWithContext dir failed: %v", err)
	}
}

// 验证任务全网并发发现与 Fast-path 熔断短路逻辑
func TestClient_FindJobWorkerWithContext_FastPathConcurrent(t *testing.T) {
	// Worker 1: 含有目标任务，立即返回
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/ps" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]protocol.JobInfo{
				{ID: "target-job-fastpath", Status: protocol.JobStatusRunning},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer s1.Close()

	// Worker 2 & 3: 响应极其缓慢 (1 秒延迟)，验证 Fast-path 熔断不会等待它们
	slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(1 * time.Second):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]protocol.JobInfo{})
		case <-r.Context().Done():
			// 受到 probeCancel() 立即熔断
			return
		}
	})
	s2 := httptest.NewServer(slowHandler)
	defer s2.Close()
	s3 := httptest.NewServer(slowHandler)
	defer s3.Close()

	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir

	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "worker-fast", Target: strings.TrimPrefix(s1.URL, "http://")})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "worker-slow1", Target: strings.TrimPrefix(s2.URL, "http://")})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "worker-slow2", Target: strings.TrimPrefix(s3.URL, "http://")})

	start := time.Now()
	target, err := cli.resolveTargetForJobWithContext(context.Background(), "", "target-job-fastpath")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("resolveTargetForJobWithContext failed: %v", err)
	}
	if target.Name != "worker-fast" {
		t.Errorf("expected target 'worker-fast', got '%s'", target.Name)
	}
	// 关键断言：Fast-path 并发短路耗时远小于 1s
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected fast-path short circuit < 500ms, took %v", elapsed)
	}
}

// 验证 Context 取消可迅速终止各客户端操作
func TestClient_ContextCancellation(t *testing.T) {
	hangServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hangServer.Close()

	tempDir := t.TempDir()
	cli := NewClient()
	cli.dataDir = tempDir
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "hang-worker", Target: strings.TrimPrefix(hangServer.URL, "http://")})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := cli.ListJobsWithContext(ctx, "hang-worker")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("expected context cancellation within 300ms, took %v", elapsed)
	}
}

func TestClient_CleanJob_And_GetJobInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/jobs/clean":
			var req protocol.CleanJobsRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.JobID == "job-test-1" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(protocol.CleanJobsResponse{CleanedCount: 1, FreedBytes: 1024})
				return
			}
			http.NotFound(w, r)
		case "/api/v1/jobs/ps":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]protocol.JobInfo{
				{
					ID:       "job-test-1",
					Status:   protocol.JobStatusCompleted,
					ExitCode: 42,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()
	u, _ := url.Parse(server.URL)
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "mock-worker", Target: u.Host})

	// 1. GetJobInfoWithContext
	info, err := cli.GetJobInfoWithContext(context.Background(), "mock-worker", "job-test-1")
	if err != nil {
		t.Fatalf("GetJobInfoWithContext failed: %v", err)
	}
	if info.ExitCode != 42 || info.Status != protocol.JobStatusCompleted {
		t.Fatalf("unexpected info: %+v", info)
	}

	// 2. CleanJobWithContext
	res, err := cli.CleanJobWithContext(context.Background(), "mock-worker", "job-test-1")
	if err != nil {
		t.Fatalf("CleanJobWithContext failed: %v", err)
	}
	if res.CleanedCount != 1 || res.FreedBytes != 1024 {
		t.Fatalf("unexpected clean res: %+v", res)
	}
}

func TestClient_CatWithSlice(t *testing.T) {
	tempDir := t.TempDir()
	localFile := filepath.Join(tempDir, "local.txt")
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&sb, "data %02d\n", i)
	}
	_ = os.WriteFile(localFile, []byte(sb.String()), 0644)

	cli := NewClient()

	// 本地 head 5
	var outHead bytes.Buffer
	err := cli.CatWithSlice(context.Background(), "", localFile, protocol.TextSliceOptions{Head: 5}, &outHead)
	if err != nil {
		t.Fatalf("CatWithSlice local failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(outHead.String()), "\n")
	if len(lines) != 5 || lines[0] != "data 01" || lines[4] != "data 05" {
		t.Fatalf("unexpected lines: %v", lines)
	}

	// 本地不存在文件报错
	var outErr bytes.Buffer
	if err := cli.CatWithSlice(context.Background(), "", filepath.Join(tempDir, "missing.txt"), protocol.TextSliceOptions{}, &outErr); err == nil {
		t.Fatal("expected error on missing local file, got nil")
	}

	// 本地大文件 (>1MB) 触发智能安全提示
	largeLocal := filepath.Join(tempDir, "large_local.txt")
	llf, _ := os.Create(largeLocal)
	chunk := strings.Repeat("C", 90) + "\n"
	for i := 0; i < 12000; i++ {
		_, _ = llf.WriteString(chunk)
	}
	llf.Close()

	var outLarge bytes.Buffer
	if err := cli.CatWithSlice(context.Background(), "", largeLocal, protocol.TextSliceOptions{}, &outLarge); err != nil {
		t.Fatalf("expected local large cat to succeed, got %v", err)
	}
	if !strings.Contains(outLarge.String(), "[NOTICE] File size") {
		t.Fatalf("expected [NOTICE] File size in output for >1MB local file")
	}
}

func TestClient_CatWithSlice_Remote_And_Fallback(t *testing.T) {
	tempDir := t.TempDir()
	remoteContent := "line 01 remote\nline 02 remote\nline 03 remote\nline 04 remote\n"

	// 1. 模拟现代 Worker：支持 /api/v1/fs/cat
	serverModern := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/fs/cat" {
			q := r.URL.Query()
			if q.Get("path") != "/remote/file.txt" {
				http.Error(w, "bad path", http.StatusBadRequest)
				return
			}
			if q.Get("tail") == "2" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("line 03 remote\nline 04 remote\n"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(remoteContent))
			return
		}
		http.NotFound(w, r)
	}))
	defer serverModern.Close()

	cli := NewClient()
	cli.dataDir = tempDir
	uModern, _ := url.Parse(serverModern.URL)
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "modern-worker", Target: uModern.Host})

	// 测试远程获取并传参 tail=2, head=1, range=1:2, all=true
	var outModern bytes.Buffer
	opts := protocol.TextSliceOptions{
		Tail:      2,
		Head:      1,
		LineRange: "1:2",
		All:       true,
	}
	err := cli.CatWithSlice(context.Background(), "modern-worker", "/remote/file.txt", opts, &outModern)
	if err != nil {
		t.Fatalf("remote CatWithSlice failed: %v", err)
	}
	if !strings.Contains(outModern.String(), "line 03 remote") {
		t.Fatalf("unexpected remote output: %q", outModern.String())
	}

	// 2. 模拟老版本 Worker：/api/v1/fs/cat 返回 404，回退到 /api/v1/fs/download 全量下载并在客户端切片
	h := sha256.Sum256([]byte(remoteContent))
	expectedLegacyHash := hex.EncodeToString(h[:])

	serverLegacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/fs/cat" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api/v1/fs/download" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("X-File-SHA256", expectedLegacyHash)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(remoteContent))
			return
		}
		http.NotFound(w, r)
	}))
	defer serverLegacy.Close()

	uLegacy, _ := url.Parse(serverLegacy.URL)
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "legacy-worker", Target: uLegacy.Host})

	var outFallback bytes.Buffer
	err = cli.CatWithSlice(context.Background(), "legacy-worker", "/remote/legacy.txt", protocol.TextSliceOptions{Head: 2}, &outFallback)
	if err != nil {
		t.Fatalf("fallback CatWithSlice failed: %v", err)
	}
	fallbackLines := strings.Split(strings.TrimSpace(outFallback.String()), "\n")
	if len(fallbackLines) != 2 || fallbackLines[0] != "line 01 remote" || fallbackLines[1] != "line 02 remote" {
		t.Fatalf("unexpected fallback lines: %v", fallbackLines)
	}

	// 3. 模拟服务端报错 500 InternalServerError
	serverErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal disk error", http.StatusInternalServerError)
	}))
	defer serverErr.Close()

	uErr, _ := url.Parse(serverErr.URL)
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "err-worker", Target: uErr.Host})

	var outErr2 bytes.Buffer
	err = cli.CatWithSlice(context.Background(), "err-worker", "/err.txt", protocol.TextSliceOptions{}, &outErr2)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected error containing 500, got: %v", err)
	}
}

func TestClient_CleanJob_And_GetJobInfo_Errors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/jobs/clean":
			var req protocol.CleanJobsRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.JobID == "job-running" {
				http.Error(w, "cannot clean running job", http.StatusConflict)
				return
			}
			if req.JobID == "job-notfound" {
				http.Error(w, "job not found", http.StatusNotFound)
				return
			}
			http.Error(w, "unexpected error", http.StatusInternalServerError)
		case "/api/v1/jobs/ps":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]protocol.JobInfo{
				{
					ID:     "job-other",
					Status: protocol.JobStatusRunning,
				},
			})
		case "/api/v1/jobs/logs":
			q := r.URL.Query()
			if q.Get("head") == "3" && q.Get("tail") == "5" && q.Get("range") == "1:10" && q.Get("all") == "true" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("mock query logs verified"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("default logs"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()
	u, _ := url.Parse(server.URL)
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "error-node", Target: u.Host})

	// 1. CleanJob 冲突 (409)
	_, err := cli.CleanJobWithContext(context.Background(), "error-node", "job-running")
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("expected 409 conflict error, got: %v", err)
	}

	// 2. CleanJob 未找到 (404)
	_, err = cli.CleanJobWithContext(context.Background(), "error-node", "job-notfound")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 not found error, got: %v", err)
	}

	// 3. GetJobInfo 任务不在列表中
	_, err = cli.GetJobInfoWithContext(context.Background(), "error-node", "job-non-existent")
	if err == nil || !strings.Contains(err.Error(), "not found on node") {
		t.Fatalf("expected 'not found on node' error, got: %v", err)
	}

	// 4. GetLogsWithOptionsWithContext 全参数透传测试
	logsOut, err := cli.GetLogsWithOptionsWithContext(context.Background(), "error-node", "job-test", protocol.TextSliceOptions{
		Head:      3,
		Tail:      5,
		LineRange: "1:10",
		All:       true,
	})
	if err != nil {
		t.Fatalf("GetLogsWithOptionsWithContext failed: %v", err)
	}
	if logsOut != "mock query logs verified" {
		t.Fatalf("expected 'mock query logs verified', got: %q", logsOut)
	}
}








