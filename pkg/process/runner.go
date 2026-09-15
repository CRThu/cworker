package process

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"cworker/pkg/logstream"
	"cworker/pkg/protocol"
	"golang.org/x/sys/windows"
)

// JobMetaFileName 统一任务元数据持久化文件名 (SSOT)
const JobMetaFileName = "job.json"

// SaveJobMeta 将 JobInfo 原子持久化至指定任务目录下的 job.json
func SaveJobMeta(jobDir string, info protocol.JobInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal job meta failed: %w", err)
	}

	tmpFile := filepath.Join(jobDir, JobMetaFileName+".tmp")
	targetFile := filepath.Join(jobDir, JobMetaFileName)

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("write tmp job meta failed: %w", err)
	}

	// 兼容 Windows 下原子重命名替换
	if err := os.Rename(tmpFile, targetFile); err != nil {
		_ = os.Remove(targetFile)
		if err := os.Rename(tmpFile, targetFile); err != nil {
			return fmt.Errorf("rename job meta failed: %w", err)
		}
	}
	return nil
}

// LoadJobMeta 从任务目录读取 job.json
func LoadJobMeta(jobDir string) (*protocol.JobInfo, error) {
	targetFile := filepath.Join(jobDir, JobMetaFileName)
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return nil, err
	}
	var info protocol.JobInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("unmarshal job meta failed: %w", err)
	}
	return &info, nil
}

// ManagedJob 统一维护单个被管任务的全部运行时状态与物理资源句柄
type ManagedJob struct {
	mu          sync.Mutex
	info        protocol.JobInfo
	jobDir      string
	cmd         *exec.Cmd
	jobObj      *JobObject
	logFile     *os.File
	broadcaster *logstream.Broadcaster
	done        chan struct{}

	lastCpuMs time.Duration
	lastTime  time.Time
}

// NewHistoricJob 为冷启动恢复的历史任务构建只读托管句柄
func NewHistoricJob(info protocol.JobInfo, jobDir string) *ManagedJob {
	done := make(chan struct{})
	close(done)
	return &ManagedJob{
		info:   info,
		jobDir: jobDir,
		done:   done,
	}
}

// IsLive 检查当前任务是否为活动中/正在被当前 Worker 管辖的物理进程句柄
func (j *ManagedJob) IsLive() bool {
	if j == nil {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cmd != nil && j.jobObj != nil && j.info.Status == protocol.JobStatusRunning
}

// StartJob 在 Worker 本地启动命令，关联 Job Object 并建立多路日志重定向
func StartJob(req protocol.RunJobRequest, jobID string, nodeName string, logRoot string) (*ManagedJob, error) {
	// 1. 规范化日志存储目录: ~/.cworker/jobs/<job_id>/output.log
	jobLogDir := filepath.Join(logRoot, "jobs", jobID)
	if err := os.MkdirAll(jobLogDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create job log dir: %w", err)
	}
	logFilePath := filepath.Join(jobLogDir, "output.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open output.log: %w", err)
	}

	broadcaster := logstream.NewBroadcaster()
	multiWriter := io.MultiWriter(logFile, broadcaster)

	// 2. 创建专用 Win32 作业对象
	jobName := fmt.Sprintf("cworker-job-%s", jobID)
	jobObj, err := CreateJobObject(jobName)
	if err != nil {
		logFile.Close()
		broadcaster.Close()
		return nil, fmt.Errorf("failed to create job object: %w", err)
	}

	// 3. 构建并启动 Windows 子进程 (通过 cmd.exe /c 保持原生环境支持)
	cmd := exec.Command("cmd.exe", "/c", req.Command)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter

	if err := cmd.Start(); err != nil {
		jobObj.Close()
		logFile.Close()
		broadcaster.Close()
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	// 4. 将主进程句柄加入 Job Object，由内核自动托管其后续所有子孙进程
	hProcess, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		_ = jobObj.AssignProcess(hProcess)
		_ = windows.CloseHandle(hProcess)
	}

	job := &ManagedJob{
		info: protocol.JobInfo{
			ID:        jobID,
			Name:      req.Name,
			Node:      nodeName,
			Command:   req.Command,
			Dir:       cmd.Dir,
			Status:    protocol.JobStatusRunning,
			PID:       cmd.Process.Pid,
			StartTime: time.Now(),
		},
		jobDir:      jobLogDir,
		cmd:         cmd,
		jobObj:      jobObj,
		logFile:     logFile,
		broadcaster: broadcaster,
		done:        make(chan struct{}),
		lastTime:    time.Now(),
	}

	// 原子落盘初始元数据 (RUNNING)
	_ = SaveJobMeta(jobLogDir, job.info)

	// 异步监听进程生命周期退出
	go job.waitExit()

	return job, nil
}

func (j *ManagedJob) waitExit() {
	defer close(j.done)
	err := j.cmd.Wait()

	j.mu.Lock()
	now := time.Now()
	j.info.EndTime = &now

	if j.info.Status == protocol.JobStatusRunning {
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				j.info.ExitCode = exitErr.ExitCode()
			} else {
				j.info.ExitCode = -1
			}
			j.info.Status = protocol.JobStatusFailed
		} else {
			j.info.ExitCode = 0
			j.info.Status = protocol.JobStatusCompleted
		}
	}

	// 持久化最终退出状态
	if j.jobDir != "" {
		_ = SaveJobMeta(j.jobDir, j.info)
	}
	j.mu.Unlock()

	// 释放资源
	if j.jobObj != nil {
		_ = j.jobObj.Close()
	}
	if j.logFile != nil {
		_ = j.logFile.Close()
	}
	if j.broadcaster != nil {
		j.broadcaster.Close()
	}
}

// WaitExit 等待任务完全退出并安全释放所有底层句柄 (包括进程树、日志文件锁与 Win32 句柄)
func (j *ManagedJob) WaitExit(timeout time.Duration) bool {
	if j == nil || j.done == nil {
		return true
	}
	select {
	case <-j.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Kill 触发 Win32 Job Object 终结，由 Windows 内核一网打尽全部子孙进程
func (j *ManagedJob) Kill() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.cmd == nil || j.jobObj == nil {
		return errors.New("cannot kill non-live or historical job")
	}

	if j.info.Status != protocol.JobStatusRunning {
		return nil
	}

	j.info.Status = protocol.JobStatusStopped
	now := time.Now()
	j.info.EndTime = &now

	if j.jobDir != "" {
		_ = SaveJobMeta(j.jobDir, j.info)
	}

	if j.jobObj != nil {
		_ = j.jobObj.Terminate(1)
	} else if j.cmd != nil && j.cmd.Process != nil {
		_ = j.cmd.Process.Kill()
	}

	return nil
}

// GetInfo 返回当前最新任务状态与度量指标
func (j *ManagedJob) GetInfo() protocol.JobInfo {
	j.mu.Lock()
	defer j.mu.Unlock()

	info := j.info
	if info.Status == protocol.JobStatusRunning && j.jobObj != nil {
		cpuMs, memMB, _, err := j.jobObj.QueryMetrics()
		if err == nil {
			info.Metrics.MemoryMB = memMB
			// 粗略计算 CPU 百分比
			now := time.Now()
			elapsed := now.Sub(j.lastTime)
			if elapsed > 0 {
				deltaCpu := time.Duration(cpuMs)*time.Millisecond - j.lastCpuMs
				percent := float64(deltaCpu) / float64(elapsed) * 100
				if percent < 0 {
					percent = 0
				}
				info.Metrics.CPUPercent = percent
			}
			j.lastCpuMs = time.Duration(cpuMs) * time.Millisecond
			j.lastTime = now
		}
	}

	return info
}

// Broadcaster 返回日志广播器，供 WebSocket 连接实时订阅输出
func (j *ManagedJob) Broadcaster() *logstream.Broadcaster {
	return j.broadcaster
}
