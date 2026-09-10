//go:build windows

package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cworker/pkg/protocol"
)

func TestManagedJob_StartAndComplete(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cworker_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	req := protocol.RunJobRequest{
		Name:    "echo-test",
		Command: "echo cworker_process_running_ok",
	}

	job, err := StartJob(req, "test-job-1", "local-node", tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}

	// 等待命令执行结束
	time.Sleep(300 * time.Millisecond)

	info := job.GetInfo()
	if info.Status != protocol.JobStatusCompleted {
		t.Errorf("job status = %s, want COMPLETED", info.Status)
	}
	if info.ExitCode != 0 {
		t.Errorf("job exit code = %d, want 0", info.ExitCode)
	}

	// 验证日志落盘
	logPath := filepath.Join(tempDir, "jobs", "test-job-1", "output.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read output.log: %v", err)
	}
	if !strings.Contains(string(content), "cworker_process_running_ok") {
		t.Errorf("log content does not contain expected output: %s", string(content))
	}
}

func TestManagedJob_Kill(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cworker_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 启动一个等待 20 秒的长任务
	req := protocol.RunJobRequest{
		Name:    "sleep-test",
		Command: "ping -n 20 127.0.0.1",
	}

	job, err := StartJob(req, "test-job-kill", "local-node", tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	info := job.GetInfo()
	if info.Status != protocol.JobStatusRunning {
		t.Fatalf("job status = %s, want RUNNING", info.Status)
	}

	// 执行 Kill
	if err := job.Kill(); err != nil {
		t.Fatalf("job.Kill() failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	infoAfter := job.GetInfo()
	if infoAfter.Status != protocol.JobStatusStopped {
		t.Errorf("job status after kill = %s, want STOPPED", infoAfter.Status)
	}
}

func TestManagedJob_Failed(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cworker_fail_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	req := protocol.RunJobRequest{
		Name:    "exit1-test",
		Command: "cmd.exe /c exit 1",
	}

	job, err := StartJob(req, "test-job-fail", "local-node", tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	info := job.GetInfo()
	if info.Status != protocol.JobStatusFailed {
		t.Errorf("job status = %s, want FAILED", info.Status)
	}
	if info.ExitCode != 1 {
		t.Errorf("job exit code = %d, want 1", info.ExitCode)
	}
}

func TestSystem_Metrics(t *testing.T) {
	freeMB, totalMB, err := GetSystemMemory()
	if err != nil {
		t.Fatalf("GetSystemMemory failed: %v", err)
	}
	if totalMB == 0 || freeMB > totalMB {
		t.Fatalf("invalid memory metrics: free=%d, total=%d", freeMB, totalMB)
	}

	// 连续两次采样 CPU 百分比
	cpu1 := GetSystemCPUPercent()
	time.Sleep(50 * time.Millisecond)
	cpu2 := GetSystemCPUPercent()
	if cpu1 < 0 || cpu1 > 100 || cpu2 < 0 || cpu2 > 100 {
		t.Fatalf("invalid cpu percent: cpu1=%f, cpu2=%f", cpu1, cpu2)
	}
}

func TestJobObject_Direct(t *testing.T) {
	jobObj, err := CreateJobObject("test_job_obj_direct")
	if err != nil {
		t.Fatalf("CreateJobObject failed: %v", err)
	}
	defer jobObj.Close()

	_, _, active, err := jobObj.QueryMetrics()
	if err != nil {
		t.Fatalf("QueryMetrics failed: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected 0 active processes, got %d", active)
	}

	// 幂等多次 Close
	if err := jobObj.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := jobObj.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestManagedJob_Broadcaster_And_WorkDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cworker_workdir_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	subDir := filepath.Join(tempDir, "sub_work_dir")
	_ = os.MkdirAll(subDir, 0755)

	req := protocol.RunJobRequest{
		Name:    "workdir-job",
		Command: "cmd.exe /c cd",
		Dir:     subDir,
	}

	job, err := StartJob(req, "test-job-workdir", "local-node", tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}

	// 验证 Broadcaster 获取
	bc := job.Broadcaster()
	if bc == nil {
		t.Fatal("expected non-nil broadcaster")
	}

	time.Sleep(300 * time.Millisecond)
	info := job.GetInfo()
	if info.Status != protocol.JobStatusCompleted {
		t.Errorf("job status = %s, want COMPLETED", info.Status)
	}
	if info.Dir != subDir {
		t.Errorf("job dir = %s, want %s", info.Dir, subDir)
	}
}
