package client

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// isTerminal 判断当前 os.Stdout 是否为交互式终端 (TTY)
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
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

// ProgressTracker 线程安全的流式进度追踪器 (TTY 环境平滑刷新，非 TTY/Agent 环境静默，支持回调)
type ProgressTracker struct {
	totalFiles       int64
	completedFiles   int64
	totalBytes       int64
	transferredBytes int64
	startTime        time.Time
	lastRender       time.Time
	isTTY            bool
	out              io.Writer
	mu               sync.Mutex
	finishOnce       sync.Once
	activeFiles      map[string]struct{}
	onUpdate         func(ProgressSnapshot)
}

// NewProgressTracker 创建并初始化传输进度追踪器
func NewProgressTracker(totalFiles, totalBytes int64) *ProgressTracker {
	if totalBytes < 0 {
		totalBytes = 0
	}
	if totalFiles < 0 {
		totalFiles = 0
	}
	return &ProgressTracker{
		totalFiles:  totalFiles,
		totalBytes:  totalBytes,
		startTime:   time.Now(),
		lastRender:  time.Now(),
		isTTY:       isTerminal(),
		out:         os.Stdout,
		activeFiles: make(map[string]struct{}),
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
		p.totalBytes = totalBytes
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

	var percent float64
	if totalB > 0 {
		percent = float64(trans) / float64(totalB) * 100.0
		if percent > 100.0 {
			percent = 100.0
		}
	} else if p.totalFiles > 0 {
		percent = float64(done) / float64(p.totalFiles) * 100.0
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
		TotalFiles:       p.totalFiles,
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
func (p *ProgressTracker) SetTotalBytes(bytes int64) {
	if p == nil || bytes < 0 {
		return
	}
	atomic.StoreInt64(&p.totalBytes, bytes)
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

func (p *ProgressTracker) maybeRender(force bool) {
	if p == nil {
		return
	}

	p.mu.Lock()
	now := time.Now()
	// 节流：每 100ms 最多渲染一次，避免刷屏卡顿终端与过度分发
	if !force && now.Sub(p.lastRender) < 100*time.Millisecond {
		p.mu.Unlock()
		return
	}
	p.lastRender = now

	trans := atomic.LoadInt64(&p.transferredBytes)
	done := atomic.LoadInt64(&p.completedFiles)
	totalB := atomic.LoadInt64(&p.totalBytes)

	var percent float64
	if totalB > 0 {
		percent = float64(trans) / float64(totalB) * 100.0
		if percent > 100.0 {
			percent = 100.0
		}
	} else if p.totalFiles > 0 {
		percent = float64(done) / float64(p.totalFiles) * 100.0
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
	totalFiles := p.totalFiles
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

		out := p.getWriter()
		if p.isTTY {
			bar := strings.Repeat("=", 20)
			fmt.Fprintf(out, "\r[%s] 100.0%% (%s, %d/%d files, %s) in %s\n",
				bar, FormatSize(trans), done, totalFiles, FormatSpeed(speedBytesSec), elapsed.Truncate(10*time.Millisecond))
		} else {
			fmt.Fprintf(out, "[cworker] Transferred %d/%d files (%s) in %s (%s)\n",
				done, totalFiles, FormatSize(trans), elapsed.Truncate(10*time.Millisecond), FormatSpeed(speedBytesSec))
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

