package protocol

import "time"

// 核心系统契约与协议常量 (SSOT 单一事实来源)
const (
	// DefaultPort Worker 默认监听端口
	DefaultPort = 19000
	// DefaultPortStr Worker 默认监听端口字符串形式
	DefaultPortStr = "19000"
	// DefaultDataDirName 默认用户存储目录名称
	DefaultDataDirName = ".cworker"
	// TokenFileName 默认 Token 文件名
	TokenFileName = "token"
	// LedgerFileName 默认已知节点账本文件名
	LedgerFileName = "nodes.json"
)

// JobStatus 表示任务生命周期状态
type JobStatus string

const (
	JobStatusRunning   JobStatus = "RUNNING"
	JobStatusCompleted JobStatus = "COMPLETED"
	JobStatusFailed    JobStatus = "FAILED"
	JobStatusStopped   JobStatus = "STOPPED"
)

// NodeStatus 表示工作节点的探活状态
type NodeStatus string

const (
	NodeStatusOnline  NodeStatus = "ONLINE"
	NodeStatusOffline NodeStatus = "OFFLINE"
)

// NodeMetrics 封装节点的系统级实时硬件负载
type NodeMetrics struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemFreeMB  uint64  `json:"mem_free_mb"`
	MemTotalMB uint64  `json:"mem_total_mb"`
}

// NodeInfo 描述集群中单个 Worker 节点的完整元数据
type NodeInfo struct {
	Name       string      `json:"name"`
	Address    string      `json:"address"` // 形如 192.168.1.102:19000
	Status     NodeStatus  `json:"status"`
	Metrics    NodeMetrics `json:"metrics"`
	ActiveJobs int         `json:"active_jobs"`
	LastSeen   time.Time   `json:"last_seen"`
}

// JobMetrics 封装特定任务（包含整棵子进程树）的资源消耗
type JobMetrics struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemoryMB   uint64  `json:"memory_mb"`
}

// JobInfo 描述单个具体任务的权威状态定义
type JobInfo struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Node      string     `json:"node"`
	Command   string     `json:"command"`
	Dir       string     `json:"dir"`
	Status    JobStatus  `json:"status"`
	PID       int        `json:"pid"`
	Metrics   JobMetrics `json:"metrics"`
	StartTime time.Time  `json:"start_time"`
	EndTime   *time.Time `json:"end_time,omitempty"`
	ExitCode  int        `json:"exit_code"`
}

// RunJobRequest 任务创建与派发请求
type RunJobRequest struct {
	Name    string `json:"name"`
	Node    string `json:"node"`
	Command string `json:"command"`
	Dir     string `json:"dir"`
}

// KillJobRequest 任务终止请求
type KillJobRequest struct {
	JobID string `json:"job_id"`
}

// KnownNode 客户端本地记忆账本中的节点条目 (Target 包含 host[:port] 与可选的认证 Token)
type KnownNode struct {
	Name   string `json:"name"`
	Target string `json:"target"` // 形如 "desktop-4090:19000" 或 "192.168.1.105"
	Token  string `json:"token,omitempty"`
}

// FileInfo 远端文件与目录详情
type FileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}
