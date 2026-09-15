package protocol

import "time"

// 核心系统契约与协议常量 (SSOT 单一事实来源)
const (
	// Version 当前软件系统发布版本契约 (SSOT 单一事实来源)
	Version = "1.4.1"
	// BuildDate 构建与发布日期契约 (SSOT 单一事实来源)
	BuildDate = "2026-09-14"
	// DefaultPort Worker 默认监听端口
	DefaultPort = 19000
	// DefaultPortStr Worker 默认监听端口字符串形式
	DefaultPortStr = "19000"
	// DefaultUIPort Web UI 控制台默认监听端口
	DefaultUIPort = 19001
	// DefaultUIPortStr Web UI 控制台默认监听端口字符串形式
	DefaultUIPortStr = "19001"
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
	CPUCores   int     `json:"cpu_cores"`
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

// CleanJobsRequest 任务与日志清理请求
type CleanJobsRequest struct {
	Days int  `json:"days"` // 清理早于多少天的终态任务；0 配合 All 使用
	All  bool `json:"all"`  // 是否清空所有已终态任务
}

// CleanJobsResponse 任务与日志清理响应
type CleanJobsResponse struct {
	CleanedCount int   `json:"cleaned_count"` // 清理的任务数
	FreedBytes   int64 `json:"freed_bytes"`   // 释放的磁盘日志大小 (字节)
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
	SHA256  string    `json:"sha256,omitempty"`
}

// DiffStatus 表示两端比对的状态定义
type DiffStatus string

const (
	DiffStatusMatch    DiffStatus = "MATCH"
	DiffStatusModified DiffStatus = "MODIFIED"
	DiffStatusAdded    DiffStatus = "ADDED"
	DiffStatusDeleted  DiffStatus = "DELETED"
)

// DiffEntry 记录单个路径在源端与目标端之间的差异详情
type DiffEntry struct {
	Status  DiffStatus `json:"status"`
	Path    string     `json:"path"`
	SrcSize int64      `json:"src_size"`
	DstSize int64      `json:"dst_size"`
	SrcHash string     `json:"src_hash,omitempty"`
	DstHash string     `json:"dst_hash,omitempty"`
}

// DiffResult 汇总比对清单与统计摘要 (SSOT)
type DiffResult struct {
	Entries  []DiffEntry `json:"entries"`
	Matched  int         `json:"matched"`
	Modified int         `json:"modified"`
	Added    int         `json:"added"`
	Deleted  int         `json:"deleted"`
}
