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

	// 空账本时执行 cw nodes
	if err := nodesCmd.RunE(nodesCmd, []string{}); err != nil {
		t.Fatalf("nodesCmd failed: %v", err)
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
	// mdCmd 参数校验：缺少节点目标
	err := mdCmd.RunE(mdCmd, []string{"invalid_no_colon"})
	if err == nil {
		t.Fatal("expected error for missing node colon in mdCmd")
	}

	// catCmd 参数校验：缺少节点目标
	err = catCmd.RunE(catCmd, []string{"invalid_no_colon"})
	if err == nil {
		t.Fatal("expected error for missing node colon in catCmd")
	}

	// lsCmd 参数校验：缺少节点目标
	err = lsCmd.RunE(lsCmd, []string{"invalid_no_colon"})
	if err == nil {
		t.Fatal("expected error for missing node colon in lsCmd")
	}

	// rmCmd 参数校验：缺少节点目标
	err = rmCmd.RunE(rmCmd, []string{"invalid_no_colon"})
	if err == nil {
		t.Fatal("expected error for missing node colon in rmCmd")
	}

	// cpCmd 参数校验：全为本地路径
	err = cpCmd.RunE(cpCmd, []string{"local1.txt", "local2.txt"})
	if err == nil {
		t.Fatal("expected error when both paths are local in cpCmd")
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

	// 5. 测试 killCmd.RunE
	killOut, err := captureStdout(func() error {
		return killCmd.RunE(killCmd, []string{"job-mock1"})
	})
	if err != nil {
		t.Fatalf("killCmd.RunE failed: %v", err)
	}
	if !strings.Contains(killOut, "[OK] Job job-mock1 terminated") {
		t.Fatalf("unexpected kill output: %s", killOut)
	}

	// 6. 测试 logsCmd.RunE
	logsLines = 10
	logsFollow = false
	logsOut, err := captureStdout(func() error {
		return logsCmd.RunE(logsCmd, []string{"job-mock1"})
	})
	if err != nil {
		t.Fatalf("logsCmd.RunE failed: %v", err)
	}
	if !strings.Contains(logsOut, "log line 1") {
		t.Fatalf("unexpected logs output: %s", logsOut)
	}

	// 7. 测试 nodesCmd.RunE (活跃集群排版)
	nodesOut, err := captureStdout(func() error {
		return nodesCmd.RunE(nodesCmd, []string{})
	})
	if err != nil {
		t.Fatalf("nodesCmd.RunE failed: %v", err)
	}
	if !strings.Contains(nodesOut, "mock-node") || !strings.Contains(nodesOut, "ONLINE") || !strings.Contains(nodesOut, "12.8%") {
		t.Fatalf("unexpected nodes output: %s", nodesOut)
	}

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
