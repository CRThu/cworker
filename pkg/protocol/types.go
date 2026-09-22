package protocol

import "time"

// 核心系统契约与协议常量 (SSOT 单一事实来源)
const (
	// Version 当前软件系统发布版本契约 (SSOT 单一事实来源)
	Version = "1.8.2"
	// BuildDate 构建与发布日期契约 (SSOT 单一事实来源)
	BuildDate = "2026-09-22"
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
	Version    string      `json:"version,omitempty"`    // cworker 程序版本，如 "1.6.1"
	OSVersion  string      `json:"os_version,omitempty"` // 宿主操作系统版本，如 "Windows 10 22H2" 或 "Windows 11 23H2"
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
	Name              string `json:"name"`
	Node              string `json:"node"`
	Command           string `json:"command"`
	Dir               string `json:"dir"`
	KillOnDisconnect  bool   `json:"kill_on_disconnect,omitempty"`
	CleanOnDisconnect bool   `json:"clean_on_disconnect,omitempty"`
}

// KillJobRequest 任务终止请求
type KillJobRequest struct {
	JobID string `json:"job_id"`
	Node  string `json:"node,omitempty"`
}

// CleanJobsRequest 任务与日志清理请求
type CleanJobsRequest struct {
	JobID string `json:"job_id,omitempty"` // 单任务精准清理 (与 Days/All 互斥优先)
	Days  int    `json:"days"`              // 清理早于多少天的终态任务；0 配合 All 使用
	All   bool   `json:"all"`               // 是否清空所有已终态任务
}

// CleanJobsResponse 任务与日志清理响应
type CleanJobsResponse struct {
	CleanedCount int   `json:"cleaned_count"` // 清理的任务数
	FreedBytes   int64 `json:"freed_bytes"`   // 释放的磁盘日志大小 (字节)
}

// 文本切片与安全防线常量 (SSOT 单一事实来源)
const (
	// DefaultTextSafetyThresholdBytes 文本输出默认安全阈值 (1MB)，超过自动保底末尾行数
	DefaultTextSafetyThresholdBytes int64 = 1024 * 1024
	// DefaultTextTailLines 超过安全阈值或未指定切片时的默认回溯行数
	DefaultTextTailLines = 100
)

// TextSliceOptions 统一文本行切片与安全读取选项
type TextSliceOptions struct {
	Tail      int    `json:"tail,omitempty"`  // 末尾 N 行
	Head      int    `json:"head,omitempty"`  // 开头 N 行
	LineRange string `json:"lines,omitempty"` // 指定行号区间 (如 "100:200", "50:", ":30")
	All       bool   `json:"all,omitempty"`       // 输出全量内容 (无截断)
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

// FsHashEventType 表示文件/目录哈希流式事件类型
type FsHashEventType string

const (
	FsHashEventInit     FsHashEventType = "init"     // 扫描完成，报告总文件数与总预估字节
	FsHashEventProgress FsHashEventType = "progress" // 正在计算大文件或心跳进度
	FsHashEventEntry    FsHashEventType = "entry"    // 单个文件哈希计算完毕
	FsHashEventDone     FsHashEventType = "done"     // 全部完成
	FsHashEventError    FsHashEventType = "error"    // 流式计算中途发生致命错误
)

// FsHashEvent 远端文件/目录哈希计算的 NDJSON 流式事件 (单向流 SSOT)
type FsHashEvent struct {
	Event       FsHashEventType `json:"event"`
	TotalFiles  int64           `json:"total_files,omitempty"`
	TotalBytes  int64           `json:"total_bytes,omitempty"`
	CurrentFile string          `json:"current_file,omitempty"`
	DoneBytes   int64           `json:"done_bytes,omitempty"`
	Entry       *FileInfo       `json:"entry,omitempty"`
	Error       string          `json:"error,omitempty"`
}

// FsRmEventType 表示文件/目录删除流式事件类型
type FsRmEventType string

const (
	FsRmEventProgress FsRmEventType = "progress" // 正在删除，报告已删除项数
	FsRmEventDone     FsRmEventType = "done"     // 删除完成，报告总删除项数
	FsRmEventError    FsRmEventType = "error"    // 删除中途出错
)

// FsRmEvent 远端文件/目录删除的 NDJSON 流式事件 (单向流 SSOT)
type FsRmEvent struct {
	Event        FsRmEventType `json:"event"`
	RemovedCount int64         `json:"removed_count,omitempty"`
	Error        string        `json:"error,omitempty"`
}


