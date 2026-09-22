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

func TestManagedJob_MetaPersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cworker_meta_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jobDir := filepath.Join(tempDir, "jobs", "test-job-meta")
	_ = os.MkdirAll(jobDir, 0755)

	// 1. 测试直接调用 SaveJobMeta 与 LoadJobMeta
	initial := protocol.JobInfo{
		ID:       "test-job-meta",
		Name:     "meta-test",
		Command:  "echo hello",
		Status:   protocol.JobStatusRunning,
		PID:      9999,
		ExitCode: 0,
	}
	if err := SaveJobMeta(jobDir, initial); err != nil {
		t.Fatalf("SaveJobMeta failed: %v", err)
	}

	loaded, err := LoadJobMeta(jobDir)
	if err != nil {
		t.Fatalf("LoadJobMeta failed: %v", err)
	}
	if loaded.ID != initial.ID || loaded.Status != protocol.JobStatusRunning || loaded.PID != 9999 {
		t.Fatalf("loaded meta mismatch: %+v", loaded)
	}

	// 2. 测试真实 StartJob 自动生成与完成更新
	req := protocol.RunJobRequest{
		Name:    "real-meta-job",
		Command: "echo real_meta_done",
	}
	job, err := StartJob(req, "test-job-real-meta", "node-1", tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}
	defer job.WaitExit(2 * time.Second)

	realJobDir := filepath.Join(tempDir, "jobs", "test-job-real-meta")
	// 启动后应立即可读取到 job.json
	metaAfterStart, err := LoadJobMeta(realJobDir)
	if err != nil {
		t.Fatalf("job.json not generated on StartJob: %v", err)
	}
	if metaAfterStart.ID != "test-job-real-meta" {
		t.Fatalf("unexpected job id in job.json: %s", metaAfterStart.ID)
	}

	// 等待任务完成
	time.Sleep(300 * time.Millisecond)
	metaAfterExit, err := LoadJobMeta(realJobDir)
	if err != nil {
		t.Fatalf("failed to read job.json after exit: %v", err)
	}
	if metaAfterExit.Status != protocol.JobStatusCompleted {
		t.Errorf("job.json status after exit = %s, want COMPLETED", metaAfterExit.Status)
	}
	if metaAfterExit.EndTime == nil {
		t.Error("job.json EndTime is nil after exit")
	}
	if metaAfterExit.ExitCode != 0 {
		t.Errorf("job.json exit code = %d, want 0", metaAfterExit.ExitCode)
	}
}

func TestManagedJob_HistoricJob(t *testing.T) {
	info := protocol.JobInfo{
		ID:       "hist-job-1",
		Name:     "historic-task",
		Status:   protocol.JobStatusCompleted,
		ExitCode: 0,
	}
	hist := NewHistoricJob(info, "D:\\fake\\path")

	if hist.IsLive() {
		t.Error("expected historical job IsLive() to be false")
	}
	if !hist.WaitExit(10 * time.Millisecond) {
		t.Error("expected WaitExit on historical job to return immediately")
	}
	if hist.Broadcaster() != nil {
		t.Error("expected nil broadcaster on historical job")
	}

	// 尝试 Kill 历史任务应报错防御
	if err := hist.Kill(); err == nil {
		t.Error("expected error when killing historical job, got nil")
	}

	gotInfo := hist.GetInfo()
	if gotInfo.ID != info.ID || gotInfo.Status != protocol.JobStatusCompleted {
		t.Errorf("GetInfo mismatch: %+v", gotInfo)
	}
}

func TestManagedJob_KillOnDisconnect_Accessors(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 测试显式开启标记
	req := protocol.RunJobRequest{
		Name:              "ephemeral-job",
		Command:           "cmd.exe /c echo test",
		KillOnDisconnect:  true,
		CleanOnDisconnect: true,
	}
	job, err := StartJob(req, "test-job-eph", "local", tempDir)
	if err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}
	defer job.Kill()

	if !job.KillOnDisconnect() {
		t.Errorf("expected KillOnDisconnect() to be true")
	}
	if !job.CleanOnDisconnect() {
		t.Errorf("expected CleanOnDisconnect() to be true")
	}

	// 2. 测试默认关闭标记
	reqDefault := protocol.RunJobRequest{
		Name:    "normal-job",
		Command: "cmd.exe /c echo normal",
	}
	jobDefault, err := StartJob(reqDefault, "test-job-def", "local", tempDir)
	if err != nil {
		t.Fatalf("StartJob default failed: %v", err)
	}
	defer jobDefault.Kill()

	if jobDefault.KillOnDisconnect() {
		t.Errorf("expected default KillOnDisconnect() to be false")
	}
	if jobDefault.CleanOnDisconnect() {
		t.Errorf("expected default CleanOnDisconnect() to be false")
	}

	// 3. 边界测试：nil 指针安全防御
	var nilJob *ManagedJob
	if nilJob.KillOnDisconnect() != false {
		t.Errorf("nil job KillOnDisconnect() should return false")
	}
	if nilJob.CleanOnDisconnect() != false {
		t.Errorf("nil job CleanOnDisconnect() should return false")
	}
}
