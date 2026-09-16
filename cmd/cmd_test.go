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
	"sync/atomic"
	"testing"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
	"cworker/pkg/updater"
)

func TestCmd_Show(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_show_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 设置临时 USERPROFILE 避开污染真实环境
	t.Setenv("USERPROFILE", tempDir)

	var buf bytes.Buffer
	showCmd.SetOut(&buf)
	showCmd.SetErr(&buf)

	if err := showCmd.RunE(showCmd, []string{}); err != nil {
		t.Fatalf("showCmd failed: %v", err)
	}

	// 再次测试带 --refresh
	showRefresh = true
	defer func() { showRefresh = false }()

	if err := showCmd.RunE(showCmd, []string{}); err != nil {
		t.Fatalf("showCmd with refresh failed: %v", err)
	}

	tokenFile := filepath.Join(tempDir, ".cworker", "token")
	if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
		t.Fatal("token file was not created by showCmd")
	}
}

func TestCmd_NodeAddAndRm(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_node_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("USERPROFILE", tempDir)

	// 1. 单参数模式
	nodeToken = "secret-token-123"
	if err := nodeAddCmd.RunE(nodeAddCmd, []string{"my-test-node"}); err != nil {
		t.Fatalf("nodeAddCmd single arg failed: %v", err)
	}

	// 2. 双参数模式
	nodeToken = "secret-token-456"
	if err := nodeAddCmd.RunE(nodeAddCmd, []string{"custom-alias", "192.168.1.50:19000"}); err != nil {
		t.Fatalf("nodeAddCmd dual args failed: %v", err)
	}

	// 3. 删除节点
	if err := nodeRmCmd.RunE(nodeRmCmd, []string{"my-test-node"}); err != nil {
		t.Fatalf("nodeRmCmd failed: %v", err)
	}
}

func TestCmd_RootHelp(t *testing.T) {
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"--help"})

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("RootCmd help failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "cw") {
		t.Fatalf("expected help output to contain 'cw', got: %s", output)
	}
}

func TestCmd_Nodes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_nodes_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("USERPROFILE", tempDir)

	// 空账本时执行 cw node 及 cw node ls
	if err := nodeCmd.RunE(nodeCmd, []string{}); err != nil {
		t.Fatalf("nodeCmd failed: %v", err)
	}
	if err := nodeLsCmd.RunE(nodeLsCmd, []string{}); err != nil {
		t.Fatalf("nodeLsCmd failed: %v", err)
	}
}

func TestCmd_Ps(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_ps_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("USERPROFILE", tempDir)

	// 空状态时执行 cw ps
	if err := psCmd.RunE(psCmd, []string{}); err != nil {
		t.Fatalf("psCmd failed: %v", err)
	}
}

func TestCmd_ServiceHelpers(t *testing.T) {
	cfg := getServiceConfig("C:\\test\\cw.exe")
	if cfg.Name != "cworker" || cfg.Executable != "C:\\test\\cw.exe" {
		t.Fatalf("unexpected service config: %+v", cfg)
	}

	// 触发环境变量广播函数确保不 panic
	notifyEnvironmentChange()
}

func TestCmd_FsValidations(t *testing.T) {
	tempDir := t.TempDir()

	// mdCmd 本地创建目录支持
	localMdDir := filepath.Join(tempDir, "local_md_test")
	if err := mdCmd.RunE(mdCmd, []string{localMdDir}); err != nil {
		t.Fatalf("expected local mdCmd to succeed, got: %v", err)
	}
	if fi, err := os.Stat(localMdDir); err != nil || !fi.IsDir() {
		t.Fatalf("local mdCmd did not create directory: %v", err)
	}

	// catCmd 本地读取支持与不存在报错
	localCatFile := filepath.Join(tempDir, "cat_test.txt")
	_ = os.WriteFile(localCatFile, []byte("cat content"), 0644)
	if err := catCmd.RunE(catCmd, []string{localCatFile}); err != nil {
		t.Fatalf("expected local catCmd to succeed, got: %v", err)
	}
	if err := catCmd.RunE(catCmd, []string{filepath.Join(tempDir, "non_existent.txt")}); err == nil {
		t.Fatal("expected error for non-existent file in catCmd")
	}

	// lsCmd 路径不存在时报错
	err := lsCmd.RunE(lsCmd, []string{filepath.Join(tempDir, "non_existent_dir")})
	if err == nil {
		t.Fatal("expected error for non-existent dir in lsCmd")
	}

	// rmCmd 路径不存在时报错
	err = rmCmd.RunE(rmCmd, []string{filepath.Join(tempDir, "non_existent_file")})
	if err == nil {
		t.Fatal("expected error for non-existent path in rmCmd")
	}

	// cpCmd 参数校验：本地不存在源文件时报错
	err = cpCmd.RunE(cpCmd, []string{filepath.Join(tempDir, "missing.txt"), filepath.Join(tempDir, "dst.txt")})
	if err == nil {
		t.Fatal("expected error when local source does not exist in cpCmd")
	}
}

func TestCmd_JobValidations(t *testing.T) {
	// killCmd 验证
	err := killCmd.RunE(killCmd, []string{"fake-job-id"})
	if err == nil {
		t.Fatal("expected error for kill fake job")
	}

	// logsCmd 验证
	err = logsCmd.RunE(logsCmd, []string{"fake-job-id"})
	if err == nil {
		t.Fatal("expected error for logs fake job")
	}
}

func TestCmd_ServiceConfig_Variations(t *testing.T) {
	svcPort = 19888
	svcWorkerName = "custom-node-name"
	defer func() {
		svcPort = 0
		svcWorkerName = ""
	}()

	cfg := getServiceConfig("C:\\bin\\cw.exe")
	if len(cfg.Arguments) < 5 {
		t.Fatalf("expected custom arguments, got: %v", cfg.Arguments)
	}

	foundPort := false
	foundName := false
	for i, arg := range cfg.Arguments {
		if arg == "--port" && i+1 < len(cfg.Arguments) && cfg.Arguments[i+1] == "19888" {
			foundPort = true
		}
		if arg == "--name" && i+1 < len(cfg.Arguments) && cfg.Arguments[i+1] == "custom-node-name" {
			foundName = true
		}
	}
	if !foundPort || !foundName {
		t.Fatalf("expected custom port and name in arguments, got: %v", cfg.Arguments)
	}
}

func TestCmd_NodeAdd_EdgeCases(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_node_edge_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("USERPROFILE", tempDir)

	// 非交互环境无 token 仍可添加，自动打印警告
	nodeToken = ""
	err = nodeAddCmd.RunE(nodeAddCmd, []string{"unauthed-node"})
	if err != nil {
		t.Fatalf("expected nodeAddCmd to succeed with empty token warning, got: %v", err)
	}

	// 验证删除不存在的节点不 panic
	err = nodeRmCmd.RunE(nodeRmCmd, []string{"never-existed"})
	if err != nil {
		t.Fatalf("nodeRmCmd non-existent failed: %v", err)
	}
}

func TestCmd_Version(t *testing.T) {
	if Version == "" {
		t.Fatal("expected Version to be non-empty")
	}
	versionCmd.Run(versionCmd, []string{})

	// 验证 -v 与 --version 标志存在
	vf := RootCmd.Flags().Lookup("version")
	if vf == nil || vf.Shorthand != "v" {
		t.Fatalf("expected version flag with -v shorthand, got: %v", vf)
	}
}

func TestCmd_Ps_UTF8Truncation(t *testing.T) {
	// 测试包含多字节中文命令截断不乱码
	cmdStr := "echo 这是一条包含非常长非常长的中文测试命令且保证超过三十五个字符的测试用例"
	cmdRunes := []rune(cmdStr)
	var displayCmd string
	if len(cmdRunes) > 35 {
		displayCmd = string(cmdRunes[:32]) + "..."
	}
	if !strings.HasSuffix(displayCmd, "...") {
		t.Fatalf("expected '...' suffix, got %s", displayCmd)
	}
	if strings.ContainsRune(displayCmd, '\ufffd') {
		t.Fatalf("displayCmd contains invalid UTF-8 replacement char: %s", displayCmd)
	}
	if len([]rune(displayCmd)) != 35 { // 32 runes + 3 dots = 35 runes
		t.Fatalf("expected 35 runes, got %d runes in %s", len([]rune(displayCmd)), displayCmd)
	}
}

func captureStdout(fn func() error) (string, error) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w

	outChan := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		outChan <- buf.String()
	}()

	cmdErr := fn()

	_ = w.Close()
	os.Stdout = oldStdout
	output := <-outChan
	_ = r.Close()

	return output, cmdErr
}

func TestCmd_Rm_InteractiveConfirmation(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	var deleteCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/fs/rm") {
			deleteCalled.Store(true)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("DELETED"))
			return
		}
		rw.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "rm-node", Target: u.Host})

	// 1. 用户输入 "y\n" 确认删除
	deleteCalled.Store(false)
	rmRecursive = true
	rmYes = false
	defer func() {
		rmRecursive = false
		rmYes = false
	}()

	oldStdin := os.Stdin
	rPipe, wPipe, _ := os.Pipe()
	os.Stdin = rPipe
	_, _ = wPipe.Write([]byte("y\n"))
	_ = wPipe.Close()

	out, err := captureStdout(func() error {
		return rmCmd.RunE(rmCmd, []string{"rm-node:D:/test/dir"})
	})
	os.Stdin = oldStdin
	if err != nil {
		t.Fatalf("rmCmd with 'y' confirmation failed: %v", err)
	}
	if !deleteCalled.Load() {
		t.Fatal("expected delete to be called on 'y'")
	}
	if !strings.Contains(out, "[OK] Deleted 'rm-node:D:/test/dir'") {
		t.Fatalf("unexpected rm output: %s", out)
	}

	// 2. 用户输入 "n\n" 取消删除
	deleteCalled.Store(false)
	rPipe2, wPipe2, _ := os.Pipe()
	os.Stdin = rPipe2
	_, _ = wPipe2.Write([]byte("n\n"))
	_ = wPipe2.Close()

	out2, err2 := captureStdout(func() error {
		return rmCmd.RunE(rmCmd, []string{"rm-node:D:/test/dir"})
	})
	os.Stdin = oldStdin
	if err2 != nil {
		t.Fatalf("rmCmd with 'n' canceled returned unexpected error: %v", err2)
	}
	if deleteCalled.Load() {
		t.Fatal("expected delete NOT to be called on 'n'")
	}
	if !strings.Contains(out2, "Operation canceled.") {
		t.Fatalf("expected 'Operation canceled.' in output, got: %s", out2)
	}

	// 3. 用户输入空回车 "\n" (默认 N) 取消删除
	deleteCalled.Store(false)
	rPipe3, wPipe3, _ := os.Pipe()
	os.Stdin = rPipe3
	_, _ = wPipe3.Write([]byte("\n"))
	_ = wPipe3.Close()

	out3, err3 := captureStdout(func() error {
		return rmCmd.RunE(rmCmd, []string{"rm-node:D:/test/dir"})
	})
	os.Stdin = oldStdin
	if err3 != nil {
		t.Fatalf("rmCmd with empty newline canceled returned error: %v", err3)
	}
	if deleteCalled.Load() {
		t.Fatal("expected delete NOT to be called on empty newline")
	}
	if !strings.Contains(out3, "Operation canceled.") {
		t.Fatalf("expected 'Operation canceled.' in output, got: %s", out3)
	}

	// 4. 带 -y 自动确认，跳过 Stdin
	deleteCalled.Store(false)
	rmYes = true
	out4, err4 := captureStdout(func() error {
		return rmCmd.RunE(rmCmd, []string{"rm-node:D:/test/dir"})
	})
	if err4 != nil {
		t.Fatalf("rmCmd with -y failed: %v", err4)
	}
	if !deleteCalled.Load() {
		t.Fatal("expected delete to be called with -y")
	}
	if !strings.Contains(out4, "[OK] Deleted") {
		t.Fatalf("expected [OK] Deleted, got: %s", out4)
	}
}

func TestCmd_Commands_WithMockServer(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	downloadPayload := "mock downloaded content 12345"
	h := sha256.New()
	h.Write([]byte(downloadPayload))
	downloadHash := hex.EncodeToString(h.Sum(nil))

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/jobs/run"):
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.JobInfo{
				ID:     "job-mock1",
				Node:   "mock-node",
				PID:    9999,
				Status: protocol.JobStatusRunning,
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/jobs/kill"):
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.JobInfo{
				ID:       "job-mock1",
				Status:   protocol.JobStatusStopped,
				ExitCode: -1,
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/jobs/ps"):
			endTime := time.Now().Add(-5 * time.Second)
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode([]protocol.JobInfo{
				{
					ID:        "job-mock1",
					Node:      "mock-node",
					Name:      "named-job",
					Status:    protocol.JobStatusRunning,
					StartTime: time.Now().Add(-10 * time.Second),
					Command:   "echo running_task",
				},
				{
					ID:        "job-2",
					Node:      "mock-node",
					Name:      "",
					Status:    protocol.JobStatusCompleted,
					StartTime: time.Now().Add(-60 * time.Second),
					EndTime:   &endTime,
					Command:   "echo 这是一个非常长非常长非常长非常长超过三十五个字符的中文命令截断测试用例且确认超过长度限制",
				},
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/jobs/logs"):
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = rw.Write([]byte("log line 1\nlog line 2\n"))
		case strings.HasPrefix(r.URL.Path, "/api/v1/health"):
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.NodeInfo{
				Name:       "mock-node",
				Address:    "127.0.0.1:19000",
				Status:     protocol.NodeStatusOnline,
				ActiveJobs: 2,
				Metrics: protocol.NodeMetrics{
					CPUPercent: 12.8,
					MemFreeMB:  8192,
					MemTotalMB: 16384,
				},
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/fs/download"):
			rw.Header().Set("X-File-Size", fmt.Sprintf("%d", len(downloadPayload)))
			rw.Header().Set("X-File-SHA256", downloadHash)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(downloadPayload))
		case strings.HasPrefix(r.URL.Path, "/api/v1/fs/ls"):
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode([]protocol.FileInfo{
				{Name: "sub_dir", IsDir: true, ModTime: time.Now()},
				{Name: "file.txt", Size: 2048, IsDir: false, ModTime: time.Now()},
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/fs/md"):
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("CREATED"))
		case strings.HasPrefix(r.URL.Path, "/api/v1/fs/rm"):
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("DELETED"))
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "mock-node", Target: u.Host})

	// 1. 测试 runCmd.RunE
	runNodeName = "mock-node"
	runJobName = "custom-name"
	runDir = "D:/workspace"
	defer func() {
		runNodeName = ""
		runJobName = ""
		runDir = ""
	}()

	runOut, err := captureStdout(func() error {
		return runCmd.RunE(runCmd, []string{"echo", "hello_world"})
	})
	if err != nil {
		t.Fatalf("runCmd.RunE failed: %v", err)
	}
	if !strings.Contains(runOut, "[OK] Job job-mock1 dispatched to node 'mock-node' (PID: 9999)") {
		t.Fatalf("unexpected run output: %s", runOut)
	}

	// 2. 测试 catCmd.RunE
	catOut, err := captureStdout(func() error {
		return catCmd.RunE(catCmd, []string{"mock-node:D:/test.txt"})
	})
	if err != nil {
		t.Fatalf("catCmd.RunE failed: %v", err)
	}
	if !strings.Contains(catOut, "mock downloaded content 12345") {
		t.Fatalf("unexpected cat output: %s", catOut)
	}

	// 3. 测试 lsCmd.RunE
	lsOut, err := captureStdout(func() error {
		return lsCmd.RunE(lsCmd, []string{"mock-node:D:/test"})
	})
	if err != nil {
		t.Fatalf("lsCmd.RunE failed: %v", err)
	}
	if !strings.Contains(lsOut, "sub_dir") || !strings.Contains(lsOut, "<DIR>") || !strings.Contains(lsOut, "file.txt") {
		t.Fatalf("unexpected ls output: %s", lsOut)
	}

	// 4. 测试 mdCmd.RunE
	mdOut, err := captureStdout(func() error {
		return mdCmd.RunE(mdCmd, []string{"mock-node:D:/new_folder"})
	})
	if err != nil {
		t.Fatalf("mdCmd.RunE failed: %v", err)
	}
	if !strings.Contains(mdOut, "[OK] Directory created on 'mock-node:D:/new_folder'") {
		t.Fatalf("unexpected md output: %s", mdOut)
	}

	// 5. 测试 killCmd.RunE (定向节点与无节点调用)
	killOut, err := captureStdout(func() error {
		return killCmd.RunE(killCmd, []string{"mock-node:job-mock1"})
	})
	if err != nil {
		t.Fatalf("killCmd.RunE with node:job_id failed: %v", err)
	}
	if !strings.Contains(killOut, "[OK] Job job-mock1 terminated") {
		t.Fatalf("unexpected kill output: %s", killOut)
	}

	killNode = "mock-node"
	killOutFlag, err := captureStdout(func() error {
		return killCmd.RunE(killCmd, []string{"job-mock1"})
	})
	killNode = ""
	if err != nil {
		t.Fatalf("killCmd.RunE with --node flag failed: %v", err)
	}
	if !strings.Contains(killOutFlag, "[OK] Job job-mock1 terminated") {
		t.Fatalf("unexpected kill output: %s", killOutFlag)
	}

	// 6. 测试 logsCmd.RunE (定向节点与无节点探测)
	logsLines = 10
	logsFollow = false
	logsOut, err := captureStdout(func() error {
		return logsCmd.RunE(logsCmd, []string{"mock-node:job-mock1"})
	})
	if err != nil {
		t.Fatalf("logsCmd.RunE with node:job_id failed: %v", err)
	}
	if !strings.Contains(logsOut, "log line 1") {
		t.Fatalf("unexpected logs output: %s", logsOut)
	}

	logsNode = "mock-node"
	logsOutFlag, err := captureStdout(func() error {
		return logsCmd.RunE(logsCmd, []string{"job-mock1"})
	})
	logsNode = ""
	if err != nil {
		t.Fatalf("logsCmd.RunE with --node flag failed: %v", err)
	}
	if !strings.Contains(logsOutFlag, "log line 1") {
		t.Fatalf("unexpected logs output: %s", logsOutFlag)
	}

	// 7. 测试 nodeCmd.RunE 及 nodeLsCmd.RunE (混合集群排版：同时验证在线与离线节点的 Address SSOT 展示)
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "offline-mock", Target: "127.0.0.1:59995"})
	nodesOut, err := captureStdout(func() error {
		return nodeCmd.RunE(nodeCmd, []string{})
	})
	if err != nil {
		t.Fatalf("nodeCmd.RunE failed: %v", err)
	}
	if !strings.Contains(nodesOut, "mock-node") || !strings.Contains(nodesOut, "ONLINE") || !strings.Contains(nodesOut, "12.8%") || !strings.Contains(nodesOut, u.Host) {
		t.Fatalf("unexpected nodes output for online node: %s", nodesOut)
	}
	if !strings.Contains(nodesOut, "offline-mock") || !strings.Contains(nodesOut, "OFFLINE") || !strings.Contains(nodesOut, "127.0.0.1:59995") {
		t.Fatalf("unexpected nodes output for offline node: %s", nodesOut)
	}

	nodeLsOut, err := captureStdout(func() error {
		return nodeLsCmd.RunE(nodeLsCmd, []string{})
	})
	if err != nil {
		t.Fatalf("nodeLsCmd.RunE failed: %v", err)
	}
	if !strings.Contains(nodeLsOut, "mock-node") || !strings.Contains(nodeLsOut, "ONLINE") || !strings.Contains(nodeLsOut, "12.8%") || !strings.Contains(nodeLsOut, u.Host) {
		t.Fatalf("unexpected node ls output: %s", nodeLsOut)
	}
	_ = cli.RemoveKnownNode("offline-mock")

	// 8. 测试 psCmd.RunE (多任务表格排版、耗时计算与超长截断)
	psOut, err := captureStdout(func() error {
		return psCmd.RunE(psCmd, []string{})
	})
	if err != nil {
		t.Fatalf("psCmd.RunE failed: %v", err)
	}
	if !strings.Contains(psOut, "job-mock1") || !strings.Contains(psOut, "named-job") {
		t.Fatalf("unexpected ps output for job-mock1: %s", psOut)
	}
	if !strings.Contains(psOut, "job-2") || !strings.Contains(psOut, "-") || !strings.Contains(psOut, "...") {
		t.Fatalf("unexpected ps output for job-2: %s", psOut)
	}
}

func TestCmd_NodeManagementCLI(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	// 1. 双参数添加 node add mynode 127.0.0.1:12345 --token abc
	nodeToken = "test-token-123"
	addOut, err := captureStdout(func() error {
		return nodeAddCmd.RunE(nodeAddCmd, []string{"node1", "127.0.0.1:12345"})
	})
	if err != nil {
		t.Fatalf("nodeAddCmd 2-arg failed: %v", err)
	}
	if !strings.Contains(addOut, "[OK] Added node 'node1'") {
		t.Fatalf("unexpected add output: %s", addOut)
	}

	// 2. 单参数添加 node add 127.0.0.1:18000
	nodeToken = "token-auto-name"
	addOut2, err := captureStdout(func() error {
		return nodeAddCmd.RunE(nodeAddCmd, []string{"127.0.0.1:18000"})
	})
	if err != nil {
		t.Fatalf("nodeAddCmd 1-arg failed: %v", err)
	}
	if !strings.Contains(addOut2, "127.0.0.1") {
		t.Fatalf("unexpected add output: %s", addOut2)
	}

	// 3. 移除存在的节点
	rmOut, err := captureStdout(func() error {
		return nodeRmCmd.RunE(nodeRmCmd, []string{"node1"})
	})
	if err != nil {
		t.Fatalf("nodeRmCmd failed: %v", err)
	}
	if !strings.Contains(rmOut, "[OK] Removed node 'node1'") {
		t.Fatalf("unexpected rm output: %s", rmOut)
	}

	// 4. 移除不存在的节点 (属于幂等操作，静默成功或打印 Removed)
	rmOut2, err := captureStdout(func() error {
		return nodeRmCmd.RunE(nodeRmCmd, []string{"nonexistent-node"})
	})
	if err != nil {
		t.Fatalf("expected nodeRmCmd on nonexistent node to be idempotent, got err: %v", err)
	}
	if !strings.Contains(rmOut2, "Removed node 'nonexistent-node'") {
		t.Fatalf("unexpected rm output for nonexistent: %s", rmOut2)
	}
}

func TestCmd_ShowCmd_CLI(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	tokenFile := filepath.Join(tempDir, ".cworker", "token")
	_ = os.MkdirAll(filepath.Dir(tokenFile), 0755)
	_ = os.WriteFile(tokenFile, []byte("mock-token-secret-999\n"), 0600)

	showOut, err := captureStdout(func() error {
		return showCmd.RunE(showCmd, []string{})
	})
	if err != nil {
		t.Fatalf("showCmd failed: %v", err)
	}
	if !strings.Contains(showOut, "mock-token-secret-999") || !strings.Contains(showOut, "cw node add") {
		t.Fatalf("expected showCmd output to contain token and node add cmd, got: %s", showOut)
	}
}

func TestCmd_DeployBinaryToUserDir(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	binDir, targetExe, err := deployBinaryToUserDir()
	if err != nil {
		t.Fatalf("deployBinaryToUserDir failed: %v", err)
	}
	if !strings.HasPrefix(binDir, tempDir) {
		t.Fatalf("expected binDir to be inside tempDir, got: %s", binDir)
	}
	if _, err := os.Stat(targetExe); err != nil {
		t.Fatalf("deployed binary does not exist at %s: %v", targetExe, err)
	}

	// 验证服务配置信息
	svcCfg := getServiceConfig(targetExe)
	if svcCfg.Name != "cworker" || !strings.Contains(svcCfg.DisplayName, "Carrot Worker") {
		t.Fatalf("unexpected service config: %+v", svcCfg)
	}
}

func TestCmd_CpCLI_EdgeCases(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	// 1. 本地源文件不存在
	nonexistentSrc := filepath.Join(tempDir, "does_not_exist.txt")
	err := cpCmd.RunE(cpCmd, []string{nonexistentSrc, "mock-node:D:/remote.txt"})
	if err == nil {
		t.Fatal("expected error when local source does not exist")
	}

	// 2. 拷贝本地文件夹到远端但未加 -r
	localDir := filepath.Join(tempDir, "local_folder")
	_ = os.MkdirAll(localDir, 0755)
	cpRecursive = false
	err = cpCmd.RunE(cpCmd, []string{localDir, "mock-node:D:/remote_dir"})
	if err == nil || !strings.Contains(err.Error(), "omitting directory") {
		t.Fatalf("expected omitting directory error, got: %v", err)
	}
}

func TestCmd_Ps_SortingAndTruncation(t *testing.T) {
	now := time.Now()
	jobs := []protocol.JobInfo{
		{ID: "job-1", Status: protocol.JobStatusCompleted, StartTime: now.Add(-10 * time.Minute)},
		{ID: "job-2", Status: protocol.JobStatusRunning, StartTime: now.Add(-2 * time.Minute)},
		{ID: "job-3", Status: protocol.JobStatusCompleted, StartTime: now.Add(-1 * time.Minute)},
		{ID: "job-4", Status: protocol.JobStatusRunning, StartTime: now.Add(-5 * time.Minute)},
	}

	sortJobsForDisplay(jobs)

	// RUNNING 状态必须排在前面
	if jobs[0].Status != protocol.JobStatusRunning || jobs[1].Status != protocol.JobStatusRunning {
		t.Fatalf("expected first two jobs to be RUNNING, got: %s, %s", jobs[0].Status, jobs[1].Status)
	}
	// 在 RUNNING 中，较新的 job-2 (2分钟前) 排在较旧的 job-4 (5分钟前) 前面
	if jobs[0].ID != "job-2" || jobs[1].ID != "job-4" {
		t.Fatalf("expected job-2 then job-4, got: %s, %s", jobs[0].ID, jobs[1].ID)
	}
	// 在已完成中，较新的 job-3 (1分钟前) 排在较旧的 job-1 (10分钟前) 前面
	if jobs[2].ID != "job-3" || jobs[3].ID != "job-1" {
		t.Fatalf("expected job-3 then job-1, got: %s, %s", jobs[2].ID, jobs[3].ID)
	}
}

func TestCmd_Clean_Validation(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	// 未传 --days 且未传 --all 时应报错
	cleanDays = 0
	cleanAll = false
	err := cleanCmd.RunE(cleanCmd, []string{})
	if err == nil {
		t.Fatal("expected error when neither --days nor --all is specified")
	}
	if !strings.Contains(err.Error(), "must specify either --days") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestCmd_Clean_ExecutionFlow(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	var cleanCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/clean" {
			cleanCalled.Store(true)
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.CleanJobsResponse{
				CleanedCount: 3,
				FreedBytes:   1024 * 1024,
			})
			return
		}
		rw.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "clean-node", Target: u.Host})

	// 1. 测试 -y 自动确认全量清理
	cleanCalled.Store(false)
	cleanAll = true
	cleanDays = 0
	cleanNode = "clean-node"
	cleanYes = true
	defer func() {
		cleanNode = ""
		cleanAll = false
		cleanDays = 0
		cleanYes = false
	}()

	out, err := captureStdout(func() error {
		return cleanCmd.RunE(cleanCmd, []string{})
	})
	if err != nil {
		t.Fatalf("clean -y failed: %v", err)
	}
	if !cleanCalled.Load() {
		t.Fatal("clean endpoint was not called")
	}
	if !strings.Contains(out, "Cleaned 3 finished jobs") {
		t.Fatalf("unexpected output: %s", out)
	}

	// 2. 测试交互式取消 (用户输入 n)
	cleanCalled.Store(false)
	cleanYes = false
	cleanAll = true

	oldStdin := os.Stdin
	rPipe, wPipe, _ := os.Pipe()
	os.Stdin = rPipe
	_, _ = wPipe.Write([]byte("n\n"))
	_ = wPipe.Close()

	outCancel, errCancel := captureStdout(func() error {
		return cleanCmd.RunE(cleanCmd, []string{})
	})
	os.Stdin = oldStdin

	if errCancel != nil {
		t.Fatalf("clean cancel failed: %v", errCancel)
	}
	if cleanCalled.Load() {
		t.Fatal("clean should NOT have been called when user entered n")
	}
	if !strings.Contains(outCancel, "Operation canceled") {
		t.Fatalf("expected 'Operation canceled', got: %s", outCancel)
	}
}

func TestCmd_Ps_ExecutionFlow(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	// 构造 25 个模拟任务
	var mockJobs []protocol.JobInfo
	for i := 0; i < 25; i++ {
		st := protocol.JobStatusCompleted
		if i == 0 {
			st = protocol.JobStatusRunning
		}
		mockJobs = append(mockJobs, protocol.JobInfo{
			ID:      fmt.Sprintf("job-%02d", i),
			Name:    fmt.Sprintf("task-%d", i),
			Status:  st,
			Node:    "ps-node",
			Command: "echo ok",
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/ps" {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(mockJobs)
			return
		}
		rw.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "ps-node", Target: u.Host})

	// 1. 默认查询 (limit 20) -> 触发省略提示
	psNode = "ps-node"
	psAll = false
	psLimit = 20
	defer func() {
		psNode = ""
		psAll = false
		psLimit = 20
	}()

	out, err := captureStdout(func() error {
		return psCmd.RunE(psCmd, []string{})
	})
	if err != nil {
		t.Fatalf("psCmd failed: %v", err)
	}
	if !strings.Contains(out, "omitted") {
		t.Fatalf("expected truncation note in output, got: %s", out)
	}
	if !strings.Contains(out, "RUNNING") {
		t.Fatalf("expected running job in output, got: %s", out)
	}

	// 2. 带 --all 查询 -> 不省略
	psAll = true
	outAll, err := captureStdout(func() error {
		return psCmd.RunE(psCmd, []string{})
	})
	if err != nil {
		t.Fatalf("ps --all failed: %v", err)
	}
	if strings.Contains(outAll, "omitted") {
		t.Fatalf("expected no truncation note with --all, got: %s", outAll)
	}
}

func TestCmd_Update_ExecutionFlow(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("USERPROFILE", tempDir)

	mockRelease := updater.ReleaseInfo{
		TagName: "v9.9.9",
		Body:    "Bug fixes and improvements",
		Assets: []updater.ReleaseAsset{
			{Name: "cw.exe", Size: 2 * 1024 * 1024, BrowserDownloadURL: "http://example.com/cw.exe"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "releases/latest") {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(mockRelease)
			return
		}
		rw.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// 1. 测试 --check 模式 (检测到有新版本但未触发下载)
	origVer := Version
	defer func() { Version = origVer }()

	updateCheck = true
	updateYes = false
	updateForce = false
	updateMirror = server.URL
	updateProxy = ""

	out, err := captureStdout(func() error {
		return updateCmd.RunE(updateCmd, []string{})
	})
	if err != nil {
		t.Fatalf("update --check failed: %v", err)
	}
	if !strings.Contains(out, "New release v9.9.9 is available") {
		t.Fatalf("expected new release notice, got: %s", out)
	}

	// 2. 测试版本已最新
	updateCheck = false
	Version = "9.9.9"
	outLatest, errLatest := captureStdout(func() error {
		return updateCmd.RunE(updateCmd, []string{})
	})
	if errLatest != nil {
		t.Fatalf("update when up to date failed: %v", errLatest)
	}
	if !strings.Contains(outLatest, "already up to date") {
		t.Fatalf("expected already up to date, got: %s", outLatest)
	}
}

func TestCmd_ExitErrorAndFormatBytes(t *testing.T) {
	// 1. ExitError
	err := &ExitError{Code: 42, Msg: "custom fatal error"}
	if err.ExitCode() != 42 {
		t.Fatalf("expected code 42, got: %d", err.ExitCode())
	}
	if err.Error() != "custom fatal error" {
		t.Fatalf("expected msg 'custom fatal error', got: %s", err.Error())
	}

	// 2. formatBytes across all scale boundaries
	cases := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{2048, "2.0 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{3 * 1024 * 1024 * 1024, "3.00 GB"},
	}
	for _, c := range cases {
		got := formatBytes(c.bytes)
		if got != c.expected {
			t.Errorf("formatBytes(%d) = %s; expected %s", c.bytes, got, c.expected)
		}
	}
}

func TestCmd_NodeListFormatting(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_nodelist_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("USERPROFILE", tempDir)

	// Mock 包含 4 类不同特性的节点服务
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		token := r.Header.Get("Authorization")

		switch token {
		case "Bearer tok-modern":
			_ = json.NewEncoder(rw).Encode(protocol.NodeInfo{
				Name:       "node-modern",
				Address:    "127.0.0.1:19001",
				Status:     protocol.NodeStatusOnline,
				ActiveJobs: 1,
				Metrics: protocol.NodeMetrics{
					CPUPercent: 11.42857,
					CPUCores:   28,
					MemFreeMB:  49152,
					MemTotalMB: 65536,
				},
			})
		case "Bearer tok-legacy":
			// 旧版本 Worker：未上报 CPUCores (默认为 0)
			_ = json.NewEncoder(rw).Encode(protocol.NodeInfo{
				Name:       "node-legacy",
				Address:    "127.0.0.1:19002",
				Status:     protocol.NodeStatusOnline,
				ActiveJobs: 0,
				Metrics: protocol.NodeMetrics{
					CPUPercent: 8.5,
					CPUCores:   0,
					MemFreeMB:  10240,
					MemTotalMB: 16384,
				},
			})
		case "Bearer tok-small":
			// 小内存节点 (< 1024MB)，单位应显示为 M
			_ = json.NewEncoder(rw).Encode(protocol.NodeInfo{
				Name:       "node-small",
				Address:    "127.0.0.1:19003",
				Status:     protocol.NodeStatusOnline,
				ActiveJobs: 0,
				Metrics: protocol.NodeMetrics{
					CPUPercent: 50.0,
					CPUCores:   2,
					MemFreeMB:  128,
					MemTotalMB: 512,
				},
			})
		case "Bearer tok-overflow":
			// 异常内存保护：Free > Total，确保非负归零且不 panic
			_ = json.NewEncoder(rw).Encode(protocol.NodeInfo{
				Name:       "node-overflow",
				Address:    "127.0.0.1:19004",
				Status:     protocol.NodeStatusOnline,
				ActiveJobs: 0,
				Metrics: protocol.NodeMetrics{
					CPUPercent: 0.0,
					CPUCores:   4,
					MemFreeMB:  4096,
					MemTotalMB: 2048,
				},
			})
		default:
			rw.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "node-modern", Target: u.Host, Token: "tok-modern"})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "node-legacy", Target: u.Host, Token: "tok-legacy"})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "node-small", Target: u.Host, Token: "tok-small"})
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "node-overflow", Target: u.Host, Token: "tok-overflow"})

	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = runNodeList()
	w.Close()
	os.Stdout = origStdout
	if err != nil {
		t.Fatalf("runNodeList failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	// 1. 表头验证：去除多余修饰
	if !strings.Contains(out, "CPU") || !strings.Contains(out, "MEM") {
		t.Fatalf("expected clean CPU and MEM headers, got:\n%s", out)
	}
	if strings.Contains(out, "FREE/TOTAL") {
		t.Fatalf("should not contain FREE/TOTAL, got:\n%s", out)
	}
	if strings.Contains(out, "CPU(%)") {
		t.Fatalf("should not contain CPU(%%), got:\n%s", out)
	}

	// 2. node-modern: 320% / 2800% 算力与 16.0G / 64.0G 内存
	if !strings.Contains(out, "320% / 2800%") {
		t.Fatalf("expected '320%% / 2800%%' for node-modern, got:\n%s", out)
	}
	if !strings.Contains(out, "16.0G / 64.0G") {
		t.Fatalf("expected '16.0G / 64.0G' for node-modern, got:\n%s", out)
	}

	// 3. node-legacy: 核心数为 0 安全降级为 8.5%，内存 6.0G / 16.0G
	if !strings.Contains(out, "8.5%") {
		t.Fatalf("expected '8.5%%' for legacy node, got:\n%s", out)
	}
	if !strings.Contains(out, "6.0G / 16.0G") {
		t.Fatalf("expected '6.0G / 16.0G' for legacy node, got:\n%s", out)
	}

	// 4. node-small: 100% / 200% 算力与 384M / 512M (<1024M 采用 M 单位)
	if !strings.Contains(out, "100% / 200%") {
		t.Fatalf("expected '100%% / 200%%' for small node, got:\n%s", out)
	}
	if !strings.Contains(out, "384M / 512M") {
		t.Fatalf("expected '384M / 512M' for small node, got:\n%s", out)
	}

	// 5. node-overflow: 0% / 400% 算力与 0.0G / 2.0G 非负安全防御
	if !strings.Contains(out, "0% / 400%") {
		t.Fatalf("expected '0%% / 400%%' for overflow node, got:\n%s", out)
	}
	if !strings.Contains(out, "0.0G / 2.0G") {
		t.Fatalf("expected '0.0G / 2.0G' for overflow node, got:\n%s", out)
	}
}

func TestCmd_Ps_WithJobsAndSorting(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_ps_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("USERPROFILE", tempDir)

	mockJobs := []protocol.JobInfo{
		{
			ID:        "job-live-1",
			Node:      "worker-ps",
			Name:      "task-live",
			Status:    protocol.JobStatusRunning,
			Command:   "ping 127.0.0.1 -n 10",
			StartTime: time.Now().Add(-10 * time.Second),
			Metrics: protocol.JobMetrics{
				CPUPercent: 15.5,
				MemoryMB:   128,
			},
		},
		{
			ID:        "job-stopped-2",
			Node:      "worker-ps",
			Name:      "task-stopped",
			Status:    protocol.JobStatusStopped,
			Command:   "very_long_command_that_exceeds_thirty_five_runes_limit_for_truncation_check",
			StartTime: time.Now().Add(-1 * time.Hour),
			ExitCode:  -1,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/ps" {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(mockJobs)
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "worker-ps", Target: u.Host, Token: "tok-ps"})

	// 1. 测试常规 ps 输出
	if err := psCmd.RunE(psCmd, []string{}); err != nil {
		t.Fatalf("psCmd failed: %v", err)
	}

	// 2. 测试指定节点 --node
	psNode = "worker-ps"
	defer func() { psNode = "" }()
	if err := psCmd.RunE(psCmd, []string{}); err != nil {
		t.Fatalf("psCmd with --node failed: %v", err)
	}

	// 3. 测试 sortJobsForDisplay
	unsorted := []protocol.JobInfo{
		{ID: "j1", Status: protocol.JobStatusCompleted, StartTime: time.Now().Add(-5 * time.Minute)},
		{ID: "j2", Status: protocol.JobStatusRunning, StartTime: time.Now().Add(-10 * time.Minute)},
		{ID: "j3", Status: protocol.JobStatusRunning, StartTime: time.Now().Add(-1 * time.Minute)},
	}
	sortJobsForDisplay(unsorted)
	if unsorted[0].ID != "j3" || unsorted[1].ID != "j2" || unsorted[2].ID != "j1" {
		t.Fatalf("sortJobsForDisplay order incorrect: %+v", unsorted)
	}
}

func TestCmd_Logs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_logs_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("USERPROFILE", tempDir)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/logs" {
			rw.Header().Set("Content-Type", "text/plain")
			_, _ = rw.Write([]byte("simulated job output line 1\nsimulated job output line 2\n"))
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "worker-logs", Target: u.Host, Token: "tok-logs"})

	if err := logsCmd.RunE(logsCmd, []string{"job-logs-123"}); err != nil {
		t.Fatalf("logsCmd failed: %v", err)
	}
}

func TestCmd_Kill(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_kill_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("USERPROFILE", tempDir)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/kill" {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.JobInfo{
				ID:       "job-kill-123",
				Status:   protocol.JobStatusStopped,
				ExitCode: 1,
			})
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "worker-kill", Target: u.Host, Token: "tok-kill"})

	if err := killCmd.RunE(killCmd, []string{"job-kill-123"}); err != nil {
		t.Fatalf("killCmd failed: %v", err)
	}
}

func TestCmd_Clean(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_clean_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("USERPROFILE", tempDir)

	// 1. 无参数应拦截校验报错
	if err := cleanCmd.RunE(cleanCmd, []string{}); err == nil {
		t.Fatal("expected error when running cleanCmd without --all or --days, got nil")
	}

	// 2. 带 mock worker 执行全量 clean
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/clean" {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.CleanJobsResponse{
				CleanedCount: 3,
				FreedBytes:   4096,
			})
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "worker-clean", Target: u.Host, Token: "tok-clean"})

	cleanAll = true
	cleanYes = true
	defer func() {
		cleanAll = false
		cleanYes = false
	}()

	if err := cleanCmd.RunE(cleanCmd, []string{}); err != nil {
		t.Fatalf("cleanCmd failed: %v", err)
	}
}

func TestCmd_Run(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_cmd_run_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("USERPROFILE", tempDir)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/run" {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(protocol.JobInfo{
				ID:     "job-run-123",
				Node:   "worker-run",
				Name:   "test-run",
				PID:    9998,
				Status: protocol.JobStatusRunning,
			})
			return
		}
		http.NotFound(rw, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{Name: "worker-run", Target: u.Host, Token: "tok-run"})

	runNodeName = "worker-run"
	runJobName = "test-run"
	defer func() {
		runNodeName = ""
		runJobName = ""
		runDir = ""
		runToken = ""
	}()

	if err := runCmd.RunE(runCmd, []string{"python", "train.py"}); err != nil {
		t.Fatalf("runCmd failed: %v", err)
	}
}





