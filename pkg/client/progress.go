package client

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	terminalOverride         *bool
	defaultHeartbeatInterval = 5 * time.Second
)

// SetDefaultHeartbeatInterval 允许在测试与配置中动态调整全局默认心跳周期 (默认 5 秒)
func SetDefaultHeartbeatInterval(d time.Duration) {
	if d <= 0 {
		d = 5 * time.Second
	}
	defaultHeartbeatInterval = d
}

// GetDefaultHeartbeatInterval 获取全局默认心跳周期 (默认 5 秒)
func GetDefaultHeartbeatInterval() time.Duration {
	if defaultHeartbeatInterval <= 0 {
		return 5 * time.Second
	}
	return defaultHeartbeatInterval
}

// SetTerminalOverride 允许在测试与特殊环境中显式指定或重置 TTY 判定
func SetTerminalOverride(override *bool) {
	terminalOverride = override
}

// IsTerminal 判断当前 os.Stdout 是否为交互式终端 (TTY)
func IsTerminal() bool {
	if terminalOverride != nil {
		return *terminalOverride
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func isTerminal() bool {
	return IsTerminal()
}

// ProgressSnapshot 进度快照契约 (供 Web UI 与自动化监听流式消费)
type ProgressSnapshot struct {
	TotalFiles       int64    `json:"total_files"`
	CompletedFiles   int64    `json:"completed_files"`
	TotalBytes       int64    `json:"total_bytes"`
	TransferredBytes int64    `json:"transferred_bytes"`
	Percent          int      `json:"percent"`
	SpeedBytesSec    int64    `json:"speed_bytes_sec"`
	ActiveFiles      []string `json:"active_files,omitempty"`
}

// ProgressTracker 线程安全的流式进度追踪器 (TTY 环境平滑刷新，非 TTY/Agent 环境低频心跳定时汇报，支持回调)
type ProgressTracker struct {
	totalFiles        int64
	completedFiles    int64
	totalBytes        int64
	transferredBytes  int64
	startTime         time.Time
	lastRender        time.Time
	lastHeartbeat     time.Time
	heartbeatInterval time.Duration
	isTTY             bool
	out               io.Writer
	mu                sync.Mutex
	finishOnce        sync.Once
	activeFiles       map[string]struct{}
	onUpdate          func(ProgressSnapshot)
	label             string
}

// SetLabel 设置动作标签 (如 "Hashed"、"Transferred")
func (p *ProgressTracker) SetLabel(label string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.label = label
	p.mu.Unlock()
}

// SetHeartbeatInterval 设置非 TTY / Agent 环境下的低频心跳汇报周期 (默认 5 秒)
func (p *ProgressTracker) SetHeartbeatInterval(d time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.heartbeatInterval = d
	p.mu.Unlock()
}

// NewProgressTracker 创建并初始化传输进度追踪器
func NewProgressTracker(totalFiles, totalBytes int64) *ProgressTracker {
	if totalBytes < 0 {
		totalBytes = 0
	}
	if totalFiles < 0 {
		totalFiles = 0
	}
	now := time.Now()
	return &ProgressTracker{
		totalFiles:        totalFiles,
		totalBytes:        totalBytes,
		startTime:         now,
		lastRender:        now,
		lastHeartbeat:     now,
		heartbeatInterval: defaultHeartbeatInterval,
		isTTY:             isTerminal(),
		out:               os.Stdout,
		activeFiles:       make(map[string]struct{}),
		label:             "Transferred",
	}
}

// SetUpdateCallback 设置进度状态更新回调
func (p *ProgressTracker) SetUpdateCallback(cb func(ProgressSnapshot)) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.onUpdate = cb
	p.mu.Unlock()
}

// SetTotals 动态设置或校准待传输总文件数与总字节量 (在目录前置扫描完成后权威回填)
func (p *ProgressTracker) SetTotals(totalFiles, totalBytes int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if totalFiles >= 0 {
		p.totalFiles = totalFiles
	}
	if totalBytes >= 0 {
		atomic.StoreInt64(&p.totalBytes, totalBytes)
	}
	p.mu.Unlock()
	p.maybeRender(false)
}

// AddTotals 累加待处理/传输的总文件数与总字节量 (支持多源/双端并发场景原子累加)
func (p *ProgressTracker) AddTotals(totalFiles, totalBytes int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if totalFiles > 0 {
		p.totalFiles += totalFiles
	}
	if totalBytes > 0 {
		atomic.AddInt64(&p.totalBytes, totalBytes)
	}
	p.mu.Unlock()
	p.maybeRender(false)
}

// StartFile 标记某个相对路径文件开始传输 (加入活跃集合)
func (p *ProgressTracker) StartFile(name string) {
	if p == nil || name == "" {
		return
	}
	p.mu.Lock()
	if p.activeFiles == nil {
		p.activeFiles = make(map[string]struct{})
	}
	p.activeFiles[name] = struct{}{}
	p.mu.Unlock()
	p.maybeRender(false)
}

// EndFile 标记某个相对路径文件传输结束 (移出活跃集合)
func (p *ProgressTracker) EndFile(name string) {
	if p == nil || name == "" {
		return
	}
	p.mu.Lock()
	if p.activeFiles != nil {
		delete(p.activeFiles, name)
	}
	p.mu.Unlock()
	p.maybeRender(false)
}

// Snapshot 获取当前瞬时进度快照
func (p *ProgressTracker) Snapshot() ProgressSnapshot {
	if p == nil {
		return ProgressSnapshot{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	trans := atomic.LoadInt64(&p.transferredBytes)
	done := atomic.LoadInt64(&p.completedFiles)
	totalB := atomic.LoadInt64(&p.totalBytes)
	totalF := p.totalFiles

	// 防御性校准：若已知分母且实际完成数超出分母，动态抬升分母确保百分比守恒
	if totalF > 0 && done > totalF {
		totalF = done
	}
	if totalB > 0 && trans > totalB {
		totalB = trans
	}

	var percent float64
	if totalB > 0 {
		percent = float64(trans) / float64(totalB) * 100.0
	} else if totalF > 0 {
		percent = float64(done) / float64(totalF) * 100.0
	}
	if percent > 100.0 {
		percent = 100.0
	}
	if percent < 0.0 {
		percent = 0.0
	}

	now := time.Now()
	elapsed := now.Sub(p.startTime).Seconds()
	speedBytesSec := 0.0
	if elapsed > 0.05 {
		speedBytesSec = float64(trans) / elapsed
	}

	var active []string
	if len(p.activeFiles) > 0 {
		active = make([]string, 0, len(p.activeFiles))
		for f := range p.activeFiles {
			active = append(active, f)
		}
	}

	return ProgressSnapshot{
		TotalFiles:       totalF,
		CompletedFiles:   done,
		TotalBytes:       totalB,
		TransferredBytes: trans,
		Percent:          int(percent),
		SpeedBytesSec:    int64(speedBytesSec),
		ActiveFiles:      active,
	}
}

// AddBytes 原子累加已传输字节并按需刷新终端显示
func (p *ProgressTracker) AddBytes(n int64) {
	if p == nil || n <= 0 {
		return
	}
	atomic.AddInt64(&p.transferredBytes, n)
	p.maybeRender(false)
}

// AddFile 原子累加已完成文件数并按需刷新终端显示
func (p *ProgressTracker) AddFile() {
	if p == nil {
		return
	}
	atomic.AddInt64(&p.completedFiles, 1)
	p.maybeRender(false)
}

// SetOutput 允许显式重定向输出目标 (用于单元测试断言与日志管道)
func (p *ProgressTracker) SetOutput(w io.Writer) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if w == nil {
		p.out = os.Stdout
	} else {
		p.out = w
	}
}

func (p *ProgressTracker) getWriter() io.Writer {
	if p == nil {
		return os.Stdout
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.out != nil {
		return p.out
	}
	return os.Stdout
}

// SetTTY 允许显式覆盖 TTY 探测 (用于单测覆盖与非标准重定向环境)
func (p *ProgressTracker) SetTTY(isTTY bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.isTTY = isTTY
}

// SetTotalBytes 动态设置预期的总字节数 (例如在收到 X-File-Size 头后)
// 保护规则：若当前 Tracker 已规划为多文件 (TotalFiles > 1) 或已有预设的有效总大小 (> 0)，
// 则忽略单文件响应头的写入，防止并发子任务或单文件大小覆盖全局目录传输总大小。
func (p *ProgressTracker) SetTotalBytes(bytes int64) {
	if p == nil || bytes < 0 {
		return
	}
	p.mu.Lock()
	if p.totalFiles > 1 || atomic.LoadInt64(&p.totalBytes) > 0 {
		p.mu.Unlock()
		return
	}
	atomic.StoreInt64(&p.totalBytes, bytes)
	p.mu.Unlock()
	p.maybeRender(false)
}

func (p *ProgressTracker) TotalBytes() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.totalBytes)
}

func (p *ProgressTracker) TransferredBytes() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.transferredBytes)
}

func (p *ProgressTracker) CompletedFiles() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.completedFiles)
}

func (p *ProgressTracker) TotalFiles() int64 {
	if p == nil {
		return 0
	}
	return p.totalFiles
}

// FormatSize 格式化字节大小为人类可读格式 (B / KB / MB / GB)
func FormatSize(bytes int64) string {
	if bytes < 0 {
		bytes = 0
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// FormatSpeed 格式化传输速率 (B/s / KB/s / MB/s / GB/s)
func FormatSpeed(bytesPerSec float64) string {
	if bytesPerSec < 0 {
		bytesPerSec = 0
	}
	const (
		kb = 1024.0
		mb = 1024.0 * kb
		gb = 1024.0 * mb
	)
	switch {
	case bytesPerSec >= gb:
		return fmt.Sprintf("%.1f GB/s", bytesPerSec/gb)
	case bytesPerSec >= mb:
		return fmt.Sprintf("%.1f MB/s", bytesPerSec/mb)
	case bytesPerSec >= kb:
		return fmt.Sprintf("%.1f KB/s", bytesPerSec/kb)
	default:
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
}

// FormatCount 将整数格式化为带千位分隔符的字符串 (例如 15200 -> "15,200")
func FormatCount(n int64) string {
	in := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(in, "-") {
		sign = "-"
		in = in[1:]
	}
	var out strings.Builder
	l := len(in)
	for i, c := range in {
		if i > 0 && (l-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	return sign + out.String()
}

func (p *ProgressTracker) maybeRender(force bool) {
	if p == nil {
		return
	}

	p.mu.Lock()
	now := time.Now()
	// 节流：每 100ms 最多渲染一次 (非 TTY 且设定了更小心跳周期时对齐心跳周期)
	throttle := 100 * time.Millisecond
	if !p.isTTY && p.heartbeatInterval > 0 && p.heartbeatInterval < throttle {
		throttle = p.heartbeatInterval
	}
	if !force && now.Sub(p.lastRender) < throttle {
		p.mu.Unlock()
		return
	}
	p.lastRender = now

	trans := atomic.LoadInt64(&p.transferredBytes)
	done := atomic.LoadInt64(&p.completedFiles)
	totalB := atomic.LoadInt64(&p.totalBytes)
	totalFiles := p.totalFiles
	if totalFiles > 0 && done > totalFiles {
		totalFiles = done
	}
	if totalB > 0 && trans > totalB {
		totalB = trans
	}

	var percent float64
	if totalB > 0 {
		percent = float64(trans) / float64(totalB) * 100.0
	} else if totalFiles > 0 {
		percent = float64(done) / float64(totalFiles) * 100.0
	}
	if percent > 100.0 {
		percent = 100.0
	}
	if percent < 0.0 {
		percent = 0.0
	}

	// 计算瞬时速率 (字节/秒)
	elapsed := now.Sub(p.startTime).Seconds()
	speedBytesSec := 0.0
	if elapsed > 0.05 {
		speedBytesSec = float64(trans) / elapsed
	}

	var active []string
	if len(p.activeFiles) > 0 {
		active = make([]string, 0, len(p.activeFiles))
		for f := range p.activeFiles {
			active = append(active, f)
		}
	}

	updateCb := p.onUpdate
	isTTY := p.isTTY
	out := p.out
	action := p.label
	if action == "" {
		action = "Progress"
	}

	shouldHeartbeat := false
	if !isTTY {
		interval := p.heartbeatInterval
		if interval <= 0 {
			interval = 5 * time.Second
		}
		if now.Sub(p.lastHeartbeat) >= interval {
			p.lastHeartbeat = now
			shouldHeartbeat = true
		}
	}
	p.mu.Unlock()

	if updateCb != nil {
		updateCb(ProgressSnapshot{
			TotalFiles:       totalFiles,
			CompletedFiles:   done,
			TotalBytes:       totalB,
			TransferredBytes: trans,
			Percent:          int(percent),
			SpeedBytesSec:    int64(speedBytesSec),
			ActiveFiles:      active,
		})
	}

	if !isTTY {
		if shouldHeartbeat {
			if out == nil {
				out = os.Stdout
			}
			fmt.Fprintf(out, "[cworker] %s: %5.1f%% (%s/%s, %d/%d files, %s)\n",
				action, percent, FormatSize(trans), FormatSize(totalB), done, totalFiles, FormatSpeed(speedBytesSec))
		}
		return
	}

	// 构造 20 格进度条
	barWidth := 20
	filled := int(percent / 100.0 * float64(barWidth))
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("=", filled)
	if filled < barWidth {
		bar += ">" + strings.Repeat(" ", barWidth-filled-1)
	}

	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "\r[%s] %5.1f%% (%s/%s, %d/%d files, %s)  ",
		bar, percent, FormatSize(trans), FormatSize(totalB), done, totalFiles, FormatSpeed(speedBytesSec))
}

// Finish 完成传输并换行输出统计结果
func (p *ProgressTracker) Finish() {
	if p == nil {
		return
	}

	p.finishOnce.Do(func() {
		p.mu.Lock()
		elapsed := time.Since(p.startTime)
		trans := atomic.LoadInt64(&p.transferredBytes)
		done := atomic.LoadInt64(&p.completedFiles)
		totalB := atomic.LoadInt64(&p.totalBytes)
		totalFiles := p.totalFiles
		if totalFiles == 0 && done > 0 {
			totalFiles = done
		} else if totalFiles > 0 && done > totalFiles {
			totalFiles = done
		}
		if totalB == 0 && trans > 0 {
			totalB = trans
		} else if totalB > 0 && trans > totalB {
			totalB = trans
		}
		updateCb := p.onUpdate

		speedBytesSec := 0.0
		if elapsed.Seconds() > 0.02 {
			speedBytesSec = float64(trans) / elapsed.Seconds()
		}
		p.mu.Unlock()

		if updateCb != nil {
			updateCb(ProgressSnapshot{
				TotalFiles:       totalFiles,
				CompletedFiles:   done,
				TotalBytes:       totalB,
				TransferredBytes: trans,
				Percent:          100,
				SpeedBytesSec:    int64(speedBytesSec),
				ActiveFiles:      nil,
			})
		}

		action := p.label
		if action == "" {
			action = "Transferred"
		}
		out := p.getWriter()
		if p.isTTY {
			bar := strings.Repeat("=", 20)
			fmt.Fprintf(out, "\r[%s] 100.0%% (%s, %d/%d files, %s) in %s\n",
				bar, FormatSize(trans), done, totalFiles, FormatSpeed(speedBytesSec), elapsed.Truncate(10*time.Millisecond))
		} else {
			fmt.Fprintf(out, "[cworker] %s %d/%d files (%s) in %s (%s)\n",
				action, done, totalFiles, FormatSize(trans), elapsed.Truncate(10*time.Millisecond), FormatSpeed(speedBytesSec))
		}
	})
}

// CountingReader 包装 io.Reader，边读边更新 ProgressTracker
type CountingReader struct {
	reader  io.Reader
	tracker *ProgressTracker
}

// NewCountingReader 包装一个 io.Reader 自动接入进度统计
func NewCountingReader(r io.Reader, tracker *ProgressTracker) *CountingReader {
	return &CountingReader{
		reader:  r,
		tracker: tracker,
	}
}

func (cr *CountingReader) Read(p []byte) (int, error) {
	n, err := cr.reader.Read(p)
	if n > 0 && cr.tracker != nil {
		cr.tracker.AddBytes(int64(n))
	}
	return n, err
}

// CountingWriter 包装 io.Writer，边写边更新 ProgressTracker
type CountingWriter struct {
	writer  io.Writer
	tracker *ProgressTracker
}

// NewCountingWriter 包装一个 io.Writer 自动接入进度统计
func NewCountingWriter(w io.Writer, tracker *ProgressTracker) *CountingWriter {
	return &CountingWriter{
		writer:  w,
		tracker: tracker,
	}
}

func (cw *CountingWriter) Write(p []byte) (int, error) {
	n, err := cw.writer.Write(p)
	if n > 0 && cw.tracker != nil {
		cw.tracker.AddBytes(int64(n))
	}
	return n, err
}

