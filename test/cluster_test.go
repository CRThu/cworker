//go:build windows

package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"cworker/pkg/protocol"
	"cworker/pkg/worker"
)

func getFreePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("getFreePort failed: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestCluster_FullLifecycle 原生双节点全生命周期在环集成测试 (绑定 127.0.0.1 纯回环，绕开外部防火墙)
func TestCluster_FullLifecycle(t *testing.T) {
	portAlpha := getFreePort(t)
	portBeta := getFreePort(t)

	dataAlpha := t.TempDir()
	dataBeta := t.TempDir()

	wAlpha, err := worker.NewWorker(worker.Config{
		Name:     "node-alpha",
		BindAddr: "127.0.0.1",
		Port:     portAlpha,
		DataDir:  dataAlpha,
	})
	if err != nil {
		t.Fatalf("NewWorker alpha failed: %v", err)
	}

	wBeta, err := worker.NewWorker(worker.Config{
		Name:     "node-beta",
		BindAddr: "127.0.0.1",
		Port:     portBeta,
		DataDir:  dataBeta,
	})
	if err != nil {
		t.Fatalf("NewWorker beta failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = wAlpha.Start(ctx) }()
	go func() { _ = wBeta.Start(ctx) }()

	// 等待两个 Worker 服务监听就绪
	time.Sleep(200 * time.Millisecond)

	// 1.1 验证进程单例锁 (Singleton Mutex Check)：相同端口启动第二实例必须被硬拦截
	wDup, err := worker.NewWorker(worker.Config{
		Name:     "node-duplicate",
		BindAddr: "127.0.0.1",
		Port:     portAlpha,
		DataDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewWorker duplicate failed: %v", err)
	}
	if err := wDup.Start(ctx); err == nil {
		t.Fatal("expected duplicate worker start on same port to fail due to singleton lock")
	}

	cliDataDir := t.TempDir()
	t.Setenv("USERPROFILE", cliDataDir)
	cli := client.NewClient()

	// 1.2 节点互信注册到本地账本 (纯 127.0.0.1 回环通信)
	targetAlpha := fmt.Sprintf("127.0.0.1:%d", portAlpha)
	targetBeta := fmt.Sprintf("127.0.0.1:%d", portBeta)

	if err := cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-alpha",
		Target: targetAlpha,
		Token:  wAlpha.Token(),
	}); err != nil {
		t.Fatalf("save node-alpha failed: %v", err)
	}

	if err := cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-beta",
		Target: targetBeta,
		Token:  wBeta.Token(),
	}); err != nil {
		t.Fatalf("save node-beta failed: %v", err)
	}

	// 2. 集群节点测活与负载扫描
	nodes, err := cli.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes failed: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 online nodes, got %d", len(nodes))
	}

	foundAlpha := false
	foundBeta := false
	for _, n := range nodes {
		if n.Name == "node-alpha" && n.Status == protocol.NodeStatusOnline {
			foundAlpha = true
		}
		if n.Name == "node-beta" && n.Status == protocol.NodeStatusOnline {
			foundBeta = true
		}
	}
	if !foundAlpha || !foundBeta {
		t.Fatalf("expected both node-alpha and node-beta to be ONLINE, nodes: %+v", nodes)
	}

	// 2.1 容错测试：混合集群扫描 (包含离线不可达节点，断言不挂死且正确标记 OFFLINE)
	if err := cli.SaveKnownNode(protocol.KnownNode{
		Name:   "dead-node",
		Target: "127.0.0.1:59997",
		Token:  "dummy-token",
	}); err != nil {
		t.Fatalf("save dead-node failed: %v", err)
	}
	mixedNodes, err := cli.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes with dead-node failed: %v", err)
	}
	foundDead := false
	for _, n := range mixedNodes {
		if n.Name == "dead-node" && n.Status == protocol.NodeStatusOffline {
			foundDead = true
		}
	}
	if !foundDead {
		t.Fatalf("expected dead-node to be marked OFFLINE: %+v", mixedNodes)
	}
	_ = cli.RemoveKnownNode("dead-node")

	// 3. 任务派发生命周期验证 (成功、失败退出码、自定义目录)
	// 3.1 成功任务
	jobAlpha, err := cli.RunJob(protocol.RunJobRequest{
		Node:    "node-alpha",
		Name:    "alpha-echo-job",
		Command: "echo CLUSTER_INT_ALPHA_OK",
	}, "")
	if err != nil {
		t.Fatalf("RunJob on node-alpha failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// 获取并验证日志
	logs, err := cli.GetLogs(jobAlpha.ID, 5)
	if err != nil {
		t.Fatalf("GetLogs failed: %v", err)
	}
	if !strings.Contains(logs, "CLUSTER_INT_ALPHA_OK") {
		t.Fatalf("expected log to contain CLUSTER_INT_ALPHA_OK, got: %s", logs)
	}

	// 3.1.1 测试多行日志与 -n 1 精确切片截取
	multiJob, err := cli.RunJob(protocol.RunJobRequest{
		Node:    "node-alpha",
		Name:    "alpha-multi-log",
		Command: "cmd /c echo FIRST_ROW && echo SECOND_ROW",
	}, "")
	if err != nil {
		t.Fatalf("RunJob multi log failed: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	tailOne, err := cli.GetLogs(multiJob.ID, 1)
	if err != nil {
		t.Fatalf("GetLogs -n 1 failed: %v", err)
	}
	if !strings.Contains(tailOne, "SECOND_ROW") || strings.Contains(tailOne, "FIRST_ROW") {
		t.Fatalf("expected tail 1 line to only contain SECOND_ROW, got: %s", tailOne)
	}

	// 3.1.2 任务自定义工作路径 (--dir) 实测
	workDirTest := filepath.Join(dataAlpha, "test_work_dir")
	if err := cli.MakeDir("node-alpha", workDirTest); err != nil {
		t.Fatalf("MakeDir workDirTest failed: %v", err)
	}
	_, err = cli.RunJob(protocol.RunJobRequest{
		Node:    "node-alpha",
		Name:    "alpha-dir-job",
		Dir:     workDirTest,
		Command: "cmd /c echo dir_marker_ok > marker.txt",
	}, "")
	if err != nil {
		t.Fatalf("RunJob with --dir failed: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	var markerBuf bytes.Buffer
	if err := cli.DownloadFile("node-alpha", filepath.Join(workDirTest, "marker.txt"), &markerBuf); err != nil {
		t.Fatalf("DownloadFile marker.txt failed: %v", err)
	}
	if !strings.Contains(markerBuf.String(), "dir_marker_ok") {
		t.Fatalf("expected marker.txt in --dir to contain dir_marker_ok, got: %s", markerBuf.String())
	}
	_ = cli.Delete("node-alpha", workDirTest, true)

	// 3.2 失败任务状态机流转 (exit 1 -> FAILED)
	jobFail, err := cli.RunJob(protocol.RunJobRequest{
		Node:    "node-alpha",
		Name:    "alpha-fail-job",
		Command: "cmd /c exit 1",
	}, "")
	if err != nil {
		t.Fatalf("RunJob fail task failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	jobsList, err := cli.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	var failJobInfo *protocol.JobInfo
	for i := range jobsList {
		if jobsList[i].ID == jobFail.ID {
			failJobInfo = &jobsList[i]
			break
		}
	}
	if failJobInfo == nil || failJobInfo.Status != protocol.JobStatusFailed {
		t.Fatalf("expected fail-job to be recorded as FAILED, got: %+v", failJobInfo)
	}

	// 3.3 Win32 Job Object 孤儿进程整树杀灭测试
	longJob, err := cli.RunJob(protocol.RunJobRequest{
		Node:    "node-alpha",
		Name:    "alpha-long-job",
		Command: "ping -n 30 127.0.0.1",
	}, "")
	if err != nil {
		t.Fatalf("RunJob long task failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	killedJob, err := cli.KillJob(longJob.ID)
	if err != nil {
		t.Fatalf("KillJob failed: %v", err)
	}
	if killedJob.Status != protocol.JobStatusStopped {
		t.Fatalf("expected killed job status STOPPED, got: %s", killedJob.Status)
	}

	// 4. 文件五件套与跨机器中继拷贝实测
	baseTestDir := filepath.Join(dataAlpha, "test_fs_cluster")
	remoteAlphaFile := filepath.Join(baseTestDir, "nested", "cluster_test.txt")
	remoteBetaFile := filepath.Join(dataBeta, "test_fs_cluster_beta", "received.txt")

	// 4.1 MakeDir
	if err := cli.MakeDir("node-alpha", filepath.Dir(remoteAlphaFile)); err != nil {
		t.Fatalf("MakeDir failed: %v", err)
	}

	// 4.2 UploadFile
	filePayload := "CLUSTER_FILE_PAYLOAD_ABC_12345"
	if err := cli.UploadFile("node-alpha", remoteAlphaFile, strings.NewReader(filePayload)); err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	// 4.3 DownloadFile
	var downloadBuf bytes.Buffer
	if err := cli.DownloadFile("node-alpha", remoteAlphaFile, &downloadBuf); err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}
	if downloadBuf.String() != filePayload {
		t.Fatalf("downloaded content mismatch: %s vs %s", downloadBuf.String(), filePayload)
	}

	// 4.4 ListDir
	files, err := cli.ListDir("node-alpha", filepath.Dir(remoteAlphaFile))
	if err != nil || len(files) != 1 {
		t.Fatalf("ListDir failed or unexpected count: len=%d, err=%v", len(files), err)
	}
	if files[0].Name != "cluster_test.txt" {
		t.Fatalf("unexpected file in ListDir: %s", files[0].Name)
	}

	// 4.5 跨节点中继拷贝 (alpha -> beta，CLI 内存管道直灌)
	if err := cli.RelayCopy("node-alpha", remoteAlphaFile, "node-beta", remoteBetaFile); err != nil {
		t.Fatalf("RelayCopy from alpha to beta failed: %v", err)
	}

	// 从 beta 节点读取核验内容
	var betaBuf bytes.Buffer
	if err := cli.DownloadFile("node-beta", remoteBetaFile, &betaBuf); err != nil {
		t.Fatalf("DownloadFile from beta failed: %v", err)
	}
	if betaBuf.String() != filePayload {
		t.Fatalf("beta content mismatch: %s", betaBuf.String())
	}

	// 4.5.1 测试非空目录删除安全拦截 (未加 -r 必须失败拦截)
	errNoR := cli.Delete("node-alpha", baseTestDir, false)
	if errNoR == nil || !strings.Contains(errNoR.Error(), "requires recursive flag") {
		t.Fatalf("expected delete non-empty directory without -r to fail, got: %v", errNoR)
	}

	// 4.6 物理删除清理 (带 -r)
	if err := cli.Delete("node-alpha", baseTestDir, true); err != nil {
		t.Fatalf("Delete on alpha failed: %v", err)
	}
	if err := cli.Delete("node-beta", filepath.Dir(remoteBetaFile), true); err != nil {
		t.Fatalf("Delete on beta failed: %v", err)
	}

	// 5. 递归文件夹传输全链路 (UploadDir -> RelayCopyDir -> DownloadDir，包含空目录守恒)
	srcLocalDir := filepath.Join(t.TempDir(), "dir_src")
	restoredDir := filepath.Join(t.TempDir(), "dir_restored")
	remoteDirAlpha := filepath.Join(dataAlpha, "remote_alpha_dir")
	remoteDirBeta := filepath.Join(dataBeta, "remote_beta_dir")

	_ = os.MkdirAll(filepath.Join(srcLocalDir, "nested", "empty_sub"), 0755)
	_ = os.MkdirAll(filepath.Join(srcLocalDir, "empty_root"), 0755)
	_ = os.WriteFile(filepath.Join(srcLocalDir, "file1.txt"), []byte("payload_file1"), 0644)
	_ = os.WriteFile(filepath.Join(srcLocalDir, "nested", "file2.txt"), []byte("payload_file2"), 0644)

	// 5.1 本地上传至 node-alpha
	if err := cli.UploadDir(ctx, "node-alpha", remoteDirAlpha, srcLocalDir, 4, nil); err != nil {
		t.Fatalf("UploadDir failed: %v", err)
	}

	// 5.2 node-alpha 中继拷贝至 node-beta
	if err := cli.RelayCopyDir(ctx, "node-alpha", remoteDirAlpha, "node-beta", remoteDirBeta, 4, nil); err != nil {
		t.Fatalf("RelayCopyDir failed: %v", err)
	}

	// 5.3 从 node-beta 下载到 restoredDir
	if err := cli.DownloadDir(ctx, "node-beta", remoteDirBeta, restoredDir, 4, nil); err != nil {
		t.Fatalf("DownloadDir failed: %v", err)
	}

	// 5.4 断言恢复出的文件和空目录守恒
	contentF1, err := os.ReadFile(filepath.Join(restoredDir, "file1.txt"))
	if err != nil || string(contentF1) != "payload_file1" {
		t.Fatalf("restored file1 mismatch: %s, err: %v", string(contentF1), err)
	}
	contentF2, err := os.ReadFile(filepath.Join(restoredDir, "nested", "file2.txt"))
	if err != nil || string(contentF2) != "payload_file2" {
		t.Fatalf("restored file2 mismatch: %s, err: %v", string(contentF2), err)
	}
	emptySubFi, err := os.Stat(filepath.Join(restoredDir, "nested", "empty_sub"))
	if err != nil || !emptySubFi.IsDir() {
		t.Fatalf("empty_sub directory not preserved")
	}
	emptyRootFi, err := os.Stat(filepath.Join(restoredDir, "empty_root"))
	if err != nil || !emptyRootFi.IsDir() {
		t.Fatalf("empty_root directory not preserved")
	}

	// 5.5 跨节点真实在环哈希比对 (node-alpha vs node-beta)
	alphaHashFiles, err := cli.HashRemotePath(ctx, "node-alpha", remoteDirAlpha, true)
	if err != nil {
		t.Fatalf("HashRemotePath on alpha failed: %v", err)
	}
	betaHashFiles, err := cli.HashRemotePath(ctx, "node-beta", remoteDirBeta, true)
	if err != nil {
		t.Fatalf("HashRemotePath on beta failed: %v", err)
	}

	diffIdentical := client.CompareFileInfos(alphaHashFiles, betaHashFiles)
	if diffIdentical.Matched != 2 || diffIdentical.Modified != 0 || diffIdentical.Added != 0 || diffIdentical.Deleted != 0 {
		t.Fatalf("expected 2 matched, 0 diffs across identical nodes, got: %+v", diffIdentical)
	}

	// 在 beta 上篡改 file1.txt，验证跨节点真实差异探测
	tamperedBetaFile := pathutil.JoinRemotePath(remoteDirBeta, "file1.txt")
	if err := cli.UploadFile("node-beta", tamperedBetaFile, strings.NewReader("tampered_content_xyz")); err != nil {
		t.Fatalf("tamper upload on beta failed: %v", err)
	}
	betaHashFilesTampered, err := cli.HashRemotePath(ctx, "node-beta", remoteDirBeta, true)
	if err != nil {
		t.Fatalf("HashRemotePath on beta tampered failed: %v", err)
	}
	diffTampered := client.CompareFileInfos(alphaHashFiles, betaHashFilesTampered)
	if diffTampered.Matched != 1 || diffTampered.Modified != 1 {
		t.Fatalf("expected 1 matched and 1 modified, got: %+v", diffTampered)
	}

	// 清理远程目录
	_ = cli.Delete("node-alpha", remoteDirAlpha, true)
	_ = cli.Delete("node-beta", remoteDirBeta, true)

	cancel()
	time.Sleep(100 * time.Millisecond)
}

// TestCluster_ConcurrencyStress_NoDeadlock 验证多协程高频并发读写不挂死、无死锁与句柄安全
func TestCluster_ConcurrencyStress_NoDeadlock(t *testing.T) {
	port := getFreePort(t)
	dataDir := t.TempDir()

	w, err := worker.NewWorker(worker.Config{
		Name:     "stress-node",
		BindAddr: "127.0.0.1",
		Port:     port,
		DataDir:  dataDir,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = w.Start(ctx) }()
	time.Sleep(150 * time.Millisecond)

	cliDataDir := t.TempDir()
	t.Setenv("USERPROFILE", cliDataDir)
	cli := client.NewClient()

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "stress-node",
		Target: fmt.Sprintf("127.0.0.1:%d", port),
		Token:  w.Token(),
	})

	var wg sync.WaitGroup
	var activeJobIDs sync.Map
	var dispatchErrors atomic.Int64
	var queryErrors atomic.Int64

	// 1. 并发派发任务 (10 个 Goroutine)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			job, err := cli.RunJob(protocol.RunJobRequest{
				Node:    "stress-node",
				Name:    fmt.Sprintf("stress-job-%d", idx),
				Command: "ping -n 5 127.0.0.1",
			}, "")
			if err != nil {
				dispatchErrors.Add(1)
				return
			}
			activeJobIDs.Store(job.ID, true)
		}(i)
	}

	// 2. 同时并发轮询 ps 与 nodes (10 个 Goroutine)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 5; round++ {
				if _, err := cli.ListJobs(); err != nil {
					queryErrors.Add(1)
				}
				if _, err := cli.ListNodes(); err != nil {
					queryErrors.Add(1)
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
	}

	// 等待任务全部成功派发并注册
	time.Sleep(200 * time.Millisecond)

	// 3. 同时并发 Kill 已经派发的任务
	activeJobIDs.Range(func(key, value any) bool {
		jobID := key.(string)
		wg.Add(1)
		go func(jID string) {
			defer wg.Done()
			_, _ = cli.KillJob(jID)
		}(jobID)
		return true
	})

	wg.Wait()

	if dispatchErrors.Load() > 0 {
		t.Fatalf("encountered %d dispatch errors under concurrency stress", dispatchErrors.Load())
	}
	if queryErrors.Load() > 0 {
		t.Fatalf("encountered %d query errors under concurrency stress", queryErrors.Load())
	}

	// 验证最终状态：集群依然健康在线
	nodes, err := cli.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes failed after stress: %v", err)
	}
	found := false
	for _, n := range nodes {
		if n.Name == "stress-node" && n.Status == protocol.NodeStatusOnline {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stress-node not online in cluster: %+v", nodes)
	}

	// 等待所有后台被 kill 的进程完全退出并释放 Windows 日志文件锁
	time.Sleep(400 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
}

// TestCluster_FaultInjection_InterruptedUpload 故障注入：文件传输中途网络中断防孤儿垃圾文件泄漏
func TestCluster_FaultInjection_InterruptedUpload(t *testing.T) {
	port := getFreePort(t)
	dataDir := t.TempDir()

	w, err := worker.NewWorker(worker.Config{
		Name:     "fault-node",
		BindAddr: "127.0.0.1",
		Port:     port,
		DataDir:  dataDir,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = w.Start(ctx) }()
	time.Sleep(150 * time.Millisecond)

	cliDataDir := t.TempDir()
	t.Setenv("USERPROFILE", cliDataDir)
	cli := client.NewClient()

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "fault-node",
		Target: fmt.Sprintf("127.0.0.1:%d", port),
		Token:  w.Token(),
	})

	targetDir := filepath.Join(dataDir, "dest_folder")
	targetFile := filepath.Join(targetDir, "broken_upload.dat")

	// 构造一个在读取 50 字节后强制断开并返回 io.ErrUnexpectedEOF 的流模拟网络中断
	brokenReader := &brokenStreamReader{
		limit: 50,
	}

	err = cli.UploadFile("fault-node", targetFile, brokenReader)
	if err == nil {
		t.Fatal("expected error on broken stream upload, got nil")
	}

	// 等待 Worker 端异步捕获连接断开并执行 defer 清理临时文件
	time.Sleep(100 * time.Millisecond)

	// 物理断言：验证目标目录中没有任何残留的 .cwupload- 隐藏临时文件
	entries, readErr := os.ReadDir(targetDir)
	if readErr == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), ".cwupload-") {
				t.Fatalf("leaked orphan temporary file on interrupted upload: %s", e.Name())
			}
		}
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
}

type brokenStreamReader struct {
	readBytes int
	limit     int
}

func (r *brokenStreamReader) Read(p []byte) (n int, err error) {
	if r.readBytes >= r.limit {
		return 0, io.ErrUnexpectedEOF
	}
	n = copy(p, bytes.Repeat([]byte("A"), len(p)))
	if r.readBytes+n > r.limit {
		n = r.limit - r.readBytes
	}
	r.readBytes += n
	if r.readBytes >= r.limit {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}
