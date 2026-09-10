package worker

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cworker/pkg/pathutil"
	"cworker/pkg/process"
	"cworker/pkg/protocol"
	"nhooyr.io/websocket"
)

// Config 封装 Worker 启动配置
type Config struct {
	Name     string
	BindAddr string // 监听绑定地址，默认为 "0.0.0.0"；单测或仅本机时可设为 "127.0.0.1" 避免触发防火墙
	Port     int
	DataDir  string
	Token    string
}

// Worker 节点运行时结构
type Worker struct {
	cfg        Config
	mu         sync.RWMutex
	jobs       map[string]*process.ManagedJob
	server     *http.Server
	listenAddr string
	localIP    string
	lock       *process.SingleInstanceLock
}

// GenerateToken 使用 Ed25519 随机种子派生高强度不可预测的 64 位十六进制安全 Token
func GenerateToken() (string, error) {
	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ed25519 token failed: %w", err)
	}
	return hex.EncodeToString(privKey.Seed()), nil
}

// LoadOrCreateToken 读取现有 Token，若不存在则自动生成并以 0600 安全权限落盘
func LoadOrCreateToken(dataDir string) (string, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return "", fmt.Errorf("create data dir failed: %w", err)
	}
	tokenPath := filepath.Join(dataDir, protocol.TokenFileName)
	if content, err := os.ReadFile(tokenPath); err == nil {
		token := strings.TrimSpace(string(content))
		if token != "" {
			return token, nil
		}
	}

	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		return "", fmt.Errorf("save token file failed: %w", err)
	}
	slog.Info("generated ed25519 security token", "path", tokenPath)
	return token, nil
}

// RefreshToken 强制轮换生成新 Token 并以 0600 安全权限覆盖落盘
func RefreshToken(dataDir string) (string, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return "", fmt.Errorf("create data dir failed: %w", err)
	}
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	tokenPath := filepath.Join(dataDir, protocol.TokenFileName)
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		return "", fmt.Errorf("save refreshed token failed: %w", err)
	}
	slog.Info("refreshed ed25519 security token", "path", tokenPath)
	return token, nil
}

// NewWorker 初始化 Worker 节点
func NewWorker(cfg Config) (*Worker, error) {
	if cfg.Name == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "worker-node"
		}
		cfg.Name = hostname
	}

	if cfg.Port == 0 {
		cfg.Port = protocol.DefaultPort
	}

	if cfg.DataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		cfg.DataDir = filepath.Join(home, protocol.DefaultDataDirName)
	}

	if cfg.Token == "" {
		token, err := LoadOrCreateToken(cfg.DataDir)
		if err != nil {
			return nil, err
		}
		cfg.Token = token
	}

	localIP := getLocalIP()
	if cfg.BindAddr != "" && cfg.BindAddr != "0.0.0.0" {
		localIP = cfg.BindAddr
	}

	return &Worker{
		cfg:     cfg,
		jobs:    make(map[string]*process.ManagedJob),
		localIP: localIP,
	}, nil
}

func (w *Worker) Token() string { return w.cfg.Token }
func (w *Worker) Name() string  { return w.cfg.Name }
func (w *Worker) Port() int     { return w.cfg.Port }

// Start 启动 Worker 服务与单例互斥锁
func (w *Worker) Start(ctx context.Context) error {
	// 1. Win32 命名互斥体单例互斥检查 (严防多开争抢端口)
	lockName := fmt.Sprintf("cworker_instance_port_%d", w.cfg.Port)
	lock, err := process.AcquireInstanceLock(lockName)
	if err != nil {
		return fmt.Errorf("[FATAL] another cworker instance is already running on port %d: %w", w.cfg.Port, err)
	}
	w.lock = lock
	defer w.lock.Release()

	// 2. 路由正交化注册 (五件套独立职责端点)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", w.authMiddleware(w.handleHealth))
	mux.HandleFunc("/api/v1/jobs/run", w.authMiddleware(w.handleRunJob))
	mux.HandleFunc("/api/v1/jobs/kill", w.authMiddleware(w.handleKillJob))
	mux.HandleFunc("/api/v1/jobs/ps", w.authMiddleware(w.handleListJobs))
	mux.HandleFunc("/api/v1/jobs/logs", w.authMiddleware(w.handleGetLogs))
	mux.HandleFunc("/api/v1/jobs/stream", w.authMiddleware(w.handleStreamLogs))

	// 文件五件套正交端点
	mux.HandleFunc("/api/v1/fs/upload", w.authMiddleware(w.handleFsUpload))
	mux.HandleFunc("/api/v1/fs/download", w.authMiddleware(w.handleFsDownload))
	mux.HandleFunc("/api/v1/fs/ls", w.authMiddleware(w.handleFsList))
	mux.HandleFunc("/api/v1/fs/md", w.authMiddleware(w.handleFsMakeDir))
	mux.HandleFunc("/api/v1/fs/rm", w.authMiddleware(w.handleFsRemove))

	bindAddr := w.cfg.BindAddr
	if bindAddr == "" {
		bindAddr = "0.0.0.0"
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", bindAddr, w.cfg.Port))
	if err != nil {
		return fmt.Errorf("worker listen failed: %w", err)
	}
	w.listenAddr = listener.Addr().String()

	tcpAddr := listener.Addr().(*net.TCPAddr)
	w.cfg.Port = tcpAddr.Port

	w.server = &http.Server{Handler: mux}

	go func() {
		if err := w.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("worker http server error", "err", err)
		}
	}()

	slog.Info("worker service listening",
		"name", w.cfg.Name,
		"port", w.cfg.Port,
		"ip", w.localIP,
		"token_configured", w.cfg.Token != "",
	)

	<-ctx.Done()
	return w.server.Shutdown(context.Background())
}

// 统一鉴权中间件 (采用常数时间比较防御侧信道时序攻击)
func (w *Worker) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if w.cfg.Token != "" {
			authHeader := r.Header.Get("Authorization")
			expected := "Bearer " + w.cfg.Token
			queryToken := r.URL.Query().Get("token")

			matchHeader := subtle.ConstantTimeCompare([]byte(authHeader), []byte(expected)) == 1
			matchQuery := subtle.ConstantTimeCompare([]byte(queryToken), []byte(w.cfg.Token)) == 1

			if !matchHeader && !matchQuery {
				http.Error(rw, "unauthorized: invalid or missing bearer token", http.StatusUnauthorized)
				return
			}
		}
		next(rw, r)
	}
}

func (w *Worker) handleHealth(rw http.ResponseWriter, r *http.Request) {
	nodeInfo := w.collectNodeInfo()
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(nodeInfo)
}

func (w *Worker) collectNodeInfo() protocol.NodeInfo {
	w.mu.RLock()
	activeCount := 0
	for _, j := range w.jobs {
		if j.GetInfo().Status == protocol.JobStatusRunning {
			activeCount++
		}
	}
	w.mu.RUnlock()

	freeMB, totalMB, _ := process.GetSystemMemory()
	cpuPercent := process.GetSystemCPUPercent()

	return protocol.NodeInfo{
		Name:    w.cfg.Name,
		Address: fmt.Sprintf("%s:%d", w.localIP, w.cfg.Port),
		Status:  protocol.NodeStatusOnline,
		Metrics: protocol.NodeMetrics{
			CPUPercent: cpuPercent,
			MemFreeMB:  freeMB,
			MemTotalMB: totalMB,
		},
		ActiveJobs: activeCount,
		LastSeen:   time.Now(),
	}
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String()
				}
			}
		}
	}
	return "127.0.0.1"
}

// HTTP 任务相关处理器

func (w *Worker) handleRunJob(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req protocol.RunJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	jobID := "job-" + hex.EncodeToString(b)

	job, err := process.StartJob(req, jobID, w.cfg.Name, w.cfg.DataDir)
	if err != nil {
		http.Error(rw, "start job failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.mu.Lock()
	w.jobs[jobID] = job
	w.mu.Unlock()

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(job.GetInfo())
}

func (w *Worker) handleKillJob(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req protocol.KillJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, "bad request", http.StatusBadRequest)
		return
	}

	w.mu.RLock()
	job, exists := w.jobs[req.JobID]
	w.mu.RUnlock()

	if !exists {
		http.Error(rw, "job not found", http.StatusNotFound)
		return
	}

	_ = job.Kill()
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(job.GetInfo())
}

func (w *Worker) handleListJobs(rw http.ResponseWriter, r *http.Request) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	list := make([]protocol.JobInfo, 0, len(w.jobs))
	for _, j := range w.jobs {
		list = append(list, j.GetInfo())
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(list)
}

// isValidJobID 校验任务 ID 是否合法，阻断路径遍历与特殊字符注入
func isValidJobID(jobID string) bool {
	if jobID == "" || len(jobID) > 128 {
		return false
	}
	for _, ch := range jobID {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

// tailFile 高性能读取文件末尾指定行数，内存严格有界，杜绝超大日志引发 OOM
func tailFile(file *os.File, n int) ([]byte, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := stat.Size()
	if size == 0 {
		return []byte{}, nil
	}

	if size <= 64*1024 {
		content, err := io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		return extractLastNLines(content, n), nil
	}

	chunkSize := int64(64 * 1024)
	offset := size
	var tailData []byte
	newlines := 0

	for offset > 0 {
		readSize := chunkSize
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		buf := make([]byte, readSize)
		if _, err := file.ReadAt(buf, offset); err != nil && err != io.EOF {
			return nil, err
		}

		tailData = append(buf, tailData...)
		newlines += bytes.Count(buf, []byte("\n"))
		if newlines >= n+1 || len(tailData) >= 2*1024*1024 {
			break
		}
	}

	return extractLastNLines(tailData, n), nil
}

func extractLastNLines(content []byte, n int) []byte {
	text := string(content)
	hasTrailingNewline := strings.HasSuffix(text, "\n")
	if hasTrailingNewline {
		text = strings.TrimSuffix(text, "\n")
		text = strings.TrimSuffix(text, "\r")
	}
	allLines := strings.Split(text, "\n")
	if len(allLines) > n {
		allLines = allLines[len(allLines)-n:]
	}
	result := strings.Join(allLines, "\n")
	if hasTrailingNewline {
		result += "\n"
	}
	return []byte(result)
}

func (w *Worker) handleGetLogs(rw http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	if !isValidJobID(jobID) {
		http.Error(rw, "invalid or missing job_id", http.StatusBadRequest)
		return
	}

	logPath := filepath.Join(w.cfg.DataDir, "jobs", jobID, "output.log")
	file, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(rw, "log not found", http.StatusNotFound)
			return
		}
		http.Error(rw, "read log failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	linesStr := r.URL.Query().Get("lines")
	if linesStr != "" {
		if n, err := strconv.Atoi(linesStr); err == nil && n > 0 {
			content, err := tailFile(file, n)
			if err != nil {
				http.Error(rw, "tail log failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = rw.Write(content)
			return
		}
	}

	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.Copy(rw, file)
}

func (w *Worker) handleStreamLogs(rw http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	if !isValidJobID(jobID) {
		http.Error(rw, "invalid or missing job_id", http.StatusBadRequest)
		return
	}

	w.mu.RLock()
	job, exists := w.jobs[jobID]
	w.mu.RUnlock()

	if !exists {
		http.Error(rw, "job not found", http.StatusNotFound)
		return
	}

	conn, err := websocket.Accept(rw, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	logPath := filepath.Join(w.cfg.DataDir, "jobs", jobID, "output.log")
	if f, err := os.Open(logPath); err == nil {
		if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
			var hist []byte
			if fi.Size() > 64*1024 {
				hist, _ = tailFile(f, 200)
			} else {
				hist, _ = io.ReadAll(f)
			}
			if len(hist) > 0 {
				_ = conn.Write(r.Context(), websocket.MessageText, hist)
			}
		}
		_ = f.Close()
	}

	subCh, unsubscribe := job.Broadcaster().Subscribe()
	defer unsubscribe()

	for {
		select {
		case <-r.Context().Done():
			return
		case chunk, ok := <-subCh:
			if !ok {
				return
			}
			err := conn.Write(r.Context(), websocket.MessageText, chunk)
			if err != nil {
				return
			}
		}
	}
}

// 远端文件五件套正交独立处理器

func (w *Worker) handleFsUpload(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawPath := r.URL.Query().Get("path")
	cleanPath, err := pathutil.NormalizeLocalPath(rawPath)
	if err != nil {
		http.Error(rw, "invalid path: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 自动递归创建父级目录
	parentDir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		http.Error(rw, "create parent dir failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if fi, err := os.Stat(cleanPath); err == nil && fi.IsDir() {
		http.Error(rw, "destination path is an existing directory", http.StatusBadRequest)
		return
	}

	// 写入同目录下的临时文件，待流式传输完成且校验通过后再原子替换目标文件，防止传输中断破坏目标原文件
	tmpPath := filepath.Join(parentDir, fmt.Sprintf(".%s.cwupload-%d", filepath.Base(cleanPath), time.Now().UnixNano()))
	destFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		http.Error(rw, "create temp file failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	committed := false
	defer func() {
		_ = destFile.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	destWriter := io.MultiWriter(destFile, hasher)

	if _, err := io.Copy(destWriter, r.Body); err != nil {
		http.Error(rw, "write file failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := destFile.Close(); err != nil {
		http.Error(rw, "flush dest file failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	computedHash := hex.EncodeToString(hasher.Sum(nil))
	expectedHash := strings.TrimSpace(r.Header.Get("X-File-SHA256"))
	if expectedHash != "" && !strings.EqualFold(expectedHash, computedHash) {
		http.Error(rw, fmt.Sprintf("sha256 mismatch: expected %s, got %s", expectedHash, computedHash), http.StatusBadRequest)
		return
	}

	// 原子替换目标文件
	if err := os.Rename(tmpPath, cleanPath); err != nil {
		_ = os.Remove(cleanPath)
		if err2 := os.Rename(tmpPath, cleanPath); err2 != nil {
			http.Error(rw, "commit dest file failed: "+err2.Error(), http.StatusInternalServerError)
			return
		}
	}
	committed = true

	rw.Header().Set("X-File-SHA256", computedHash)
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write([]byte("OK"))
}

func (w *Worker) handleFsDownload(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawPath := r.URL.Query().Get("path")
	cleanPath, err := pathutil.NormalizeLocalPath(rawPath)
	if err != nil {
		http.Error(rw, "invalid path: "+err.Error(), http.StatusBadRequest)
		return
	}

	f, err := os.Open(cleanPath)
	if err != nil {
		http.Error(rw, "open file failed: "+err.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		http.Error(rw, "stat file failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if fi.IsDir() {
		http.Error(rw, "path is a directory, use recursive copy (-r)", http.StatusBadRequest)
		return
	}

	// 提前发送文件大小供客户端进度条初始化，保留 chunked transfer 与 trailer 校验
	rw.Header().Set("X-File-Size", fmt.Sprintf("%d", fi.Size()))
	rw.Header().Set("Trailer", "X-File-SHA256")
	rw.Header().Set("Content-Type", "application/octet-stream")

	hasher := sha256.New()
	mw := io.MultiWriter(rw, hasher)
	if _, err := io.Copy(mw, f); err != nil {
		return
	}

	fileHash := hex.EncodeToString(hasher.Sum(nil))
	rw.Header().Set("X-File-SHA256", fileHash)
}

func (w *Worker) handleFsList(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawPath := r.URL.Query().Get("path")
	cleanPath, err := pathutil.NormalizeLocalPath(rawPath)
	if err != nil {
		http.Error(rw, "invalid path: "+err.Error(), http.StatusBadRequest)
		return
	}

	fi, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(rw, "path not found", http.StatusNotFound)
			return
		}
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if !fi.IsDir() {
		http.Error(rw, "path is a file, not a directory", http.StatusBadRequest)
		return
	}

	recursive := r.URL.Query().Get("recursive") == "true"
	var list []protocol.FileInfo

	if recursive {
		err = filepath.WalkDir(cleanPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == cleanPath {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			// 过滤符号链接避免循环递归
			if info.Mode()&os.ModeSymlink != 0 {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			rel, err := filepath.Rel(cleanPath, path)
			if err != nil {
				return err
			}

			list = append(list, protocol.FileInfo{
				Name:    d.Name(),
				Path:    filepath.ToSlash(rel),
				IsDir:   d.IsDir(),
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
			return nil
		})
		if err != nil {
			http.Error(rw, "walk dir failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		entries, err := os.ReadDir(cleanPath)
		if err != nil {
			http.Error(rw, "readdir failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		for _, e := range entries {
			info, _ := e.Info()
			size := int64(0)
			modTime := time.Now()
			if info != nil {
				size = info.Size()
				modTime = info.ModTime()
			}
			list = append(list, protocol.FileInfo{
				Name:    e.Name(),
				Path:    e.Name(),
				IsDir:   e.IsDir(),
				Size:    size,
				ModTime: modTime,
			})
		}
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(list)
}

func (w *Worker) handleFsMakeDir(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawPath := r.URL.Query().Get("path")
	cleanPath, err := pathutil.NormalizeLocalPath(rawPath)
	if err != nil {
		http.Error(rw, "invalid path: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(cleanPath, 0755); err != nil {
		http.Error(rw, "mkdir failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write([]byte("CREATED"))
}

func (w *Worker) handleFsRemove(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawPath := r.URL.Query().Get("path")
	cleanPath, err := pathutil.NormalizeLocalPath(rawPath)
	if err != nil {
		http.Error(rw, "invalid path: "+err.Error(), http.StatusBadRequest)
		return
	}

	recursive := r.URL.Query().Get("recursive") == "true"
	fi, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(rw, "path not found", http.StatusNotFound)
			return
		}
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	if fi.IsDir() {
		if !recursive {
			entries, _ := os.ReadDir(cleanPath)
			if len(entries) > 0 {
				http.Error(rw, "path is a non-empty directory, requires recursive flag (-r)", http.StatusBadRequest)
				return
			}
			_ = os.Remove(cleanPath)
		} else {
			if err := os.RemoveAll(cleanPath); err != nil {
				http.Error(rw, "remove dir failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	} else {
		if err := os.Remove(cleanPath); err != nil {
			http.Error(rw, "remove file failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write([]byte("DELETED"))
}
