package ui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/logstream"
	"cworker/pkg/pathutil"
	"cworker/pkg/protocol"
	"cworker/pkg/worker"
	"nhooyr.io/websocket"
)

//go:embed dist/*
var distFS embed.FS

// Config 封装控制台 Web 服务配置
type Config struct {
	BindAddr string // 监听绑定 IP，严格限制为 "127.0.0.1" 或 "localhost"
	Port     int    // 监听端口，默认 19001
	Version  string // 软件版本号
	Client   *client.Client
}

// Server 控制端本地聚合 HTTP 服务
type Server struct {
	cfg        Config
	cli        *client.Client
	httpServer *http.Server
	listener   net.Listener
	addr       string
	mu         sync.Mutex
}

// NewServer 实例化控制台服务
func NewServer(cfg Config) *Server {
	if cfg.BindAddr == "" {
		cfg.BindAddr = "127.0.0.1"
	}
	if cfg.Port == -1 {
		cfg.Port = 0 // 显式支持操作系统动态分配随机空闲端口 (ephemeral port)
	} else if cfg.Port == 0 {
		cfg.Port = protocol.DefaultUIPort
	}
	if cfg.Version == "" {
		cfg.Version = protocol.Version
	}
	if cfg.Client == nil {
		cfg.Client = client.NewClient()
	}

	return &Server{
		cfg: cfg,
		cli: cfg.Client,
	}
}

// Addr 获取服务实际监听地址
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// URL 获取浏览器可直接访问的完整 HTTP 地址
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s", s.Addr())
}

// Handler 构建所有静态资源与聚合 API 路由
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 1. 静态资源路由映射 (Vite 编译产物位于 dist/ 目录下)
	subFS, err := fs.Sub(distFS, "dist")
	if err == nil {
		fileServer := http.FileServer(http.FS(subFS))
		mux.Handle("/assets/", fileServer)
		mux.Handle("/favicon.svg", fileServer)
		mux.Handle("/icons.svg", fileServer)
	}

	// 2. 根路径与前端 SPA 入口 (非 /api/ 路径回退至 index.html)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		data, err := distFS.ReadFile("dist/index.html")
		if err != nil {
			http.Error(w, "index.html not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})

	// 3. 本地聚合 API 路由
	mux.HandleFunc("/api/ui/overview", s.handleOverview)
	mux.HandleFunc("/api/ui/nodes", s.handleNodes)
	mux.HandleFunc("/api/ui/jobs", s.handleJobs)
	mux.HandleFunc("/api/ui/jobs/run", s.handleRunJob)
	mux.HandleFunc("/api/ui/jobs/kill", s.handleKillJob)
	mux.HandleFunc("/api/ui/jobs/clean", s.handleCleanJobs)
	mux.HandleFunc("/api/ui/jobs/logs", s.handleGetJobLogs)
	mux.HandleFunc("/api/ui/jobs/stream", s.handleStreamLogs)
	mux.HandleFunc("/api/ui/fs/ls", s.handleFsList)
	mux.HandleFunc("/api/ui/fs/upload", s.handleFsUpload)
	mux.HandleFunc("/api/ui/fs/download", s.handleFsDownload)
	mux.HandleFunc("/api/ui/fs/transfer", s.handleFsTransfer)
	mux.HandleFunc("/api/ui/fs/roots", s.handleFsRoots)
	mux.HandleFunc("/api/ui/fs/mkdir", s.handleFsMkdir)
	mux.HandleFunc("/api/ui/fs/rm", s.handleFsRemove)

	return mux
}

// Start 启动本地 HTTP 服务
func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.cfg.BindAddr, s.cfg.Port))
	if err != nil {
		return fmt.Errorf("listen on %s:%d failed: %w", s.cfg.BindAddr, s.cfg.Port, err)
	}

	s.mu.Lock()
	s.listener = listener
	s.addr = listener.Addr().String()
	s.httpServer = &http.Server{
		Handler: s.Handler(),
	}
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		return s.Close()
	case err := <-errCh:
		return err
	}
}

// Close 平滑优雅关闭服务
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// ==================== 聚合 API 处理器 ====================

// OverviewData 概览聚合结构
type OverviewData struct {
	Version             string              `json:"version"`
	BuildDate           string              `json:"build_date"`
	NodesCount          int                 `json:"nodes_count"`
	OnlineCount         int                 `json:"online_count"`
	ActiveJobs          int                 `json:"active_jobs"`
	AvgCPU              float64             `json:"avg_cpu"`
	TotalCPUCores       int                 `json:"total_cpu_cores"`
	TotalUsedCPUPercent float64             `json:"total_used_cpu_percent"`
	TotalFreeMemMB      uint64              `json:"total_free_mem_mb"`
	TotalMemMB          uint64              `json:"total_mem_mb"`
	Nodes               []protocol.NodeInfo `json:"nodes"`
	LocalCard           LocalCardInfo       `json:"local_card"`
}

// LocalCardInfo 本机名片结构
type LocalCardInfo struct {
	Name  string `json:"name"`
	IP    string `json:"ip"`
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodes, _ := s.cli.ListNodes()
	onlineCount := 0
	activeJobs := 0
	var sumCPU float64
	var totalCPUCores int
	var totalUsedCPU float64
	var totalFreeMem uint64
	var totalMem uint64

	for _, n := range nodes {
		if n.Status == protocol.NodeStatusOnline {
			onlineCount++
			activeJobs += n.ActiveJobs
			sumCPU += n.Metrics.CPUPercent
			totalFreeMem += n.Metrics.MemFreeMB
			totalMem += n.Metrics.MemTotalMB
			if n.Metrics.CPUCores > 0 {
				totalCPUCores += n.Metrics.CPUCores
				totalUsedCPU += math.Round(n.Metrics.CPUPercent * float64(n.Metrics.CPUCores))
			}
		}
	}

	var avgCPU float64
	if onlineCount > 0 {
		avgCPU = sumCPU / float64(onlineCount)
	}

	// 提取本机名片信息
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, protocol.DefaultDataDirName)
	token, _ := worker.LoadOrCreateToken(dataDir)
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "local-node"
	}

	resp := OverviewData{
		Version:             s.cfg.Version,
		BuildDate:           protocol.BuildDate,
		NodesCount:          len(nodes),
		OnlineCount:         onlineCount,
		ActiveJobs:          activeJobs,
		AvgCPU:              avgCPU,
		TotalCPUCores:       totalCPUCores,
		TotalUsedCPUPercent: totalUsedCPU,
		TotalFreeMemMB:      totalFreeMem,
		TotalMemMB:          totalMem,
		Nodes:               nodes,
		LocalCard: LocalCardInfo{
			Name:  hostname,
			IP:    "127.0.0.1",
			Port:  protocol.DefaultPort,
			Token: token,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleNodes 节点矩阵 (增删查改)
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		nodes, err := s.cli.ListNodes()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		knownMap, _ := s.cli.LoadKnownNodes()
		homeDir, _ := os.UserHomeDir()
		dataDir := filepath.Join(homeDir, protocol.DefaultDataDirName)
		localToken, _ := worker.LoadOrCreateToken(dataDir)
		hostname, _ := os.Hostname()

		var knownList []protocol.KnownNode
		for _, k := range knownMap {
			if k.Token == "" && localToken != "" {
				if (hostname != "" && strings.EqualFold(k.Name, hostname)) ||
					strings.HasPrefix(k.Target, "127.0.0.1") ||
					strings.HasPrefix(k.Target, "localhost") {
					k.Token = localToken
				}
			}
			knownList = append(knownList, k)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"nodes": nodes,
			"known": knownList,
		})

	case http.MethodPost:
		var req protocol.KnownNode
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.Target == "" {
			http.Error(w, "name and target are required", http.StatusBadRequest)
			return
		}
		if _, _, err := net.SplitHostPort(req.Target); err != nil {
			req.Target = fmt.Sprintf("%s:%s", req.Target, protocol.DefaultPortStr)
		}

		if err := s.cli.SaveKnownNode(req); err != nil {
			http.Error(w, "save node failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "query param 'name' is required", http.StatusBadRequest)
			return
		}
		if err := s.cli.RemoveKnownNode(name); err != nil {
			http.Error(w, "remove node failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleJobs 任务列表查询与多维过滤
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node := r.URL.Query().Get("node")
	statusFilter := protocol.JobStatus(r.URL.Query().Get("status"))
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))

	jobs, err := s.cli.ListJobs(node)
	if err != nil {
		http.Error(w, "list jobs failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 按 JobID 去重 (防止同一物理机在账本中存在多个网络入口时重复回包)
	seen := make(map[string]bool)
	var uniqueJobs []protocol.JobInfo
	for _, j := range jobs {
		if !seen[j.ID] {
			seen[j.ID] = true
			uniqueJobs = append(uniqueJobs, j)
		}
	}
	jobs = uniqueJobs

	// 排序保障：RUNNING 优先置顶，其余按启动时间倒序排
	sort.SliceStable(jobs, func(i, j int) bool {
		iRunning := jobs[i].Status == protocol.JobStatusRunning
		jRunning := jobs[j].Status == protocol.JobStatusRunning
		if iRunning && !jRunning {
			return true
		}
		if !iRunning && jRunning {
			return false
		}
		return jobs[i].StartTime.After(jobs[j].StartTime)
	})

	// 过滤项
	var filtered []protocol.JobInfo
	for _, j := range jobs {
		if statusFilter != "" && j.Status != statusFilter {
			continue
		}
		if search != "" {
			matchID := strings.Contains(strings.ToLower(j.ID), search)
			matchName := strings.Contains(strings.ToLower(j.Name), search)
			matchCmd := strings.Contains(strings.ToLower(j.Command), search)
			if !matchID && !matchName && !matchCmd {
				continue
			}
		}
		filtered = append(filtered, j)
	}

	if filtered == nil {
		filtered = []protocol.JobInfo{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(filtered)
}

// handleRunJob 派发新任务
func (s *Server) handleRunJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		protocol.RunJobRequest
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		http.Error(w, "command cannot be empty", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		req.Name = generateRandomJobName()
	}

	info, err := s.cli.RunJob(req.RunJobRequest, req.Token)
	if err != nil {
		http.Error(w, "dispatch job failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

var (
	adjectives = []string{"swift", "brave", "sharp", "calm", "rapid", "silent", "bold", "vivid", "bright", "agile", "sturdy", "noble"}
	nouns      = []string{"falcon", "badger", "otter", "crane", "lynx", "tiger", "eagle", "cedar", "beacon", "orbit", "stream", "matrix"}
)

func generateRandomJobName() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	adj := adjectives[int(b[0])%len(adjectives)]
	noun := nouns[int(b[1])%len(nouns)]
	return fmt.Sprintf("%s-%s-%03d", adj, noun, int(b[0])%900+100)
}

// handleKillJob 终止指定任务
func (s *Server) handleKillJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req protocol.KillJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.JobID == "" {
		http.Error(w, "job_id is required", http.StatusBadRequest)
		return
	}

	var info *protocol.JobInfo
	var err error
	if req.Node != "" {
		info, err = s.cli.KillJobNode(req.Node, req.JobID)
	} else {
		info, err = s.cli.KillJob(req.JobID)
	}
	if err != nil {
		http.Error(w, "kill job failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// handleCleanJobs 清理已完成任务
func (s *Server) handleCleanJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Node  string   `json:"node"`
		Nodes []string `json:"nodes"`
		Days  int      `json:"days"`
		All   bool     `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	allResults := make(map[string]protocol.CleanJobsResponse)
	if len(req.Nodes) > 0 {
		for _, n := range req.Nodes {
			res, err := s.cli.CleanJobs(n, req.Days, req.All)
			if err == nil {
				for k, v := range res {
					allResults[k] = v
				}
			}
		}
	} else {
		res, err := s.cli.CleanJobs(req.Node, req.Days, req.All)
		if err != nil {
			http.Error(w, "clean jobs failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		allResults = res
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(allResults)
}

// wsWriter 将字节流实时转换为安全合法的 UTF-8 WebSocket Text 消息帧写入浏览器连接
type wsWriter struct {
	conn *websocket.Conn
	ctx  context.Context
	n    *int64
}

func (w *wsWriter) Write(p []byte) (int, error) {
	safe := logstream.EnsureUTF8(p)
	err := w.conn.Write(w.ctx, websocket.MessageText, safe)
	if err != nil {
		return 0, err
	}
	if w.n != nil {
		*w.n += int64(len(p))
	}
	return len(p), nil
}

// handleStreamLogs 实时流式日志桥接 (直通目标 Worker，支持定向加速与历史回退)
func (s *Server) handleStreamLogs(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	node := r.URL.Query().Get("node")
	if jobID == "" {
		http.Error(w, "query param 'job_id' is required", http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		slog.Error("websocket accept failed", "err", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "finished")

	var written int64
	writer := &wsWriter{
		conn: conn,
		ctx:  r.Context(),
		n:    &written,
	}

	// 桥接 Client.StreamLogsNode 推流
	_ = s.cli.StreamLogsNode(r.Context(), node, jobID, writer)

	// 若流式广播未发送任何字节 (例如已完成任务或 Worker 刚重启)，自动回退直接读取已落盘历史输出
	if written == 0 {
		if logs, err := s.cli.GetLogsNode(node, jobID, 500); err == nil && len(logs) > 0 {
			_ = conn.Write(r.Context(), websocket.MessageText, logstream.EnsureUTF8([]byte(logs)))
		}
	}
}

// handleGetJobLogs 获取指定任务的完整或截断日志 (REST 接口，用于离线查看与回退加载)
func (s *Server) handleGetJobLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	jobID := r.URL.Query().Get("job_id")
	node := r.URL.Query().Get("node")
	if jobID == "" {
		http.Error(w, "query param 'job_id' is required", http.StatusBadRequest)
		return
	}

	lines := 500
	if l := r.URL.Query().Get("lines"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			lines = n
		}
	}

	output, err := s.cli.GetLogsNode(node, jobID, lines)
	if err != nil {
		http.Error(w, "get logs failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(logstream.EnsureUTF8([]byte(output)))
}

// handleFsList 目录列表
func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node := r.URL.Query().Get("node")
	targetPath := r.URL.Query().Get("path")
	if targetPath == "" {
		targetPath = "."
	}

	var files []protocol.FileInfo
	var err error

	if node == "" {
		files, err = client.ListLocalDir(targetPath)
	} else {
		files, err = s.cli.ListDir(node, targetPath)
	}

	if err != nil {
		http.Error(w, "list directory failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if files == nil {
		files = []protocol.FileInfo{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(files)
}

// handleFsUpload 文件上传
func (s *Server) handleFsUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node := r.URL.Query().Get("node")
	targetPath := r.URL.Query().Get("path")
	if targetPath == "" {
		http.Error(w, "query param 'path' is required", http.StatusBadRequest)
		return
	}

	if node == "" {
		// 本地上传落盘
		cleanPath, err := pathutil.NormalizeLocalPath(targetPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = os.MkdirAll(filepath.Dir(cleanPath), 0755)
		f, err := os.Create(cleanPath)
		if err != nil {
			http.Error(w, "create local file failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()
		if _, err := io.Copy(f, r.Body); err != nil {
			http.Error(w, "write file failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		// 远端节点上传
		if err := s.cli.UploadFileWithContext(r.Context(), node, targetPath, r.Body); err != nil {
			http.Error(w, "remote upload failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleFsDownload 文件下载
func (s *Server) handleFsDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node := r.URL.Query().Get("node")
	targetPath := r.URL.Query().Get("path")
	if targetPath == "" {
		http.Error(w, "query param 'path' is required", http.StatusBadRequest)
		return
	}

	filename := pathutil.SafeBaseName(targetPath)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	if node == "" {
		cleanPath, err := pathutil.NormalizeLocalPath(targetPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.ServeFile(w, r, cleanPath)
		return
	}

	// 远端文件流式直通响应
	if err := s.cli.DownloadFileWithContext(r.Context(), node, targetPath, w); err != nil {
		http.Error(w, "download remote file failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleFsTransfer 跨机/本地中继文件传输 (支持 application/x-ndjson 实时流式进度反馈与传统 JSON 响应)
func (s *Server) handleFsTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SrcNode     string `json:"src_node"`
		SrcPath     string `json:"src_path"`
		DstNode     string `json:"dst_node"`
		DstPath     string `json:"dst_path"`
		Recursive   bool   `json:"recursive"`
		Concurrency int    `json:"concurrency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 8
	}

	isStream := r.URL.Query().Get("stream") == "true" || strings.Contains(r.Header.Get("Accept"), "application/x-ndjson")
	flusher, isFlusher := w.(http.Flusher)

	var sendMu sync.Mutex
	sendStreamFrame := func(frame map[string]any) {
		if !isStream {
			return
		}
		sendMu.Lock()
		defer sendMu.Unlock()
		data, _ := json.Marshal(frame)
		_, _ = w.Write(append(data, '\n'))
		if isFlusher {
			flusher.Flush()
		}
	}

	if isStream {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		if isFlusher {
			flusher.Flush()
		}
	}

	handleTransferError := func(statusCode int, msg string) {
		if isStream {
			sendStreamFrame(map[string]any{
				"type":  "error",
				"error": msg,
			})
		} else {
			http.Error(w, msg, statusCode)
		}
	}

	ctx := r.Context()

	tracker := client.NewProgressTracker(0, 0)
	tracker.SetTTY(false)
	tracker.SetUpdateCallback(func(snap client.ProgressSnapshot) {
		sendStreamFrame(map[string]any{
			"type":              "progress",
			"percent":           snap.Percent,
			"total_files":       snap.TotalFiles,
			"completed_files":   snap.CompletedFiles,
			"total_bytes":       snap.TotalBytes,
			"transferred_bytes": snap.TransferredBytes,
			"speed_bps":         snap.SpeedBytesSec,
			"active_files":      snap.ActiveFiles,
		})
	})

	opts := client.TransferOptions{
		SrcNode:     req.SrcNode,
		SrcPath:     req.SrcPath,
		DstNode:     req.DstNode,
		DstPath:     req.DstPath,
		Recursive:   req.Recursive,
		Concurrency: req.Concurrency,
	}

	if err := s.cli.Transfer(ctx, opts, tracker); err != nil {
		var dirErr *client.ErrDirectoryWithoutRecursive
		if errors.As(err, &dirErr) || errors.Is(err, os.ErrNotExist) || errors.Is(err, pathutil.ErrEmptyPath) {
			handleTransferError(http.StatusBadRequest, err.Error())
			return
		}
		handleTransferError(http.StatusInternalServerError, err.Error())
		return
	}

	if isStream {
		sendStreamFrame(map[string]any{
			"type":    "done",
			"percent": 100,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleFsRoots 获取可用根目录/盘符列表 (支持本地与指定远端 Worker)
func (s *Server) handleFsRoots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node := r.URL.Query().Get("node")
	roots, err := s.cli.GetRootsWithContext(r.Context(), node)
	if err != nil {
		http.Error(w, "get roots failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(roots)
}

// handleFsMkdir 新建目录
func (s *Server) handleFsMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Node string `json:"node"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	var err error
	if req.Node == "" {
		cleanPath, nErr := pathutil.NormalizeLocalPath(req.Path)
		if nErr != nil {
			http.Error(w, nErr.Error(), http.StatusBadRequest)
			return
		}
		err = client.MakeLocalDir(cleanPath)
	} else {
		err = s.cli.MakeDirWithContext(r.Context(), req.Node, req.Path)
	}

	if err != nil {
		http.Error(w, "create directory failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleFsRemove 删除文件或目录
func (s *Server) handleFsRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Node      string `json:"node"`
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	var err error
	if req.Node == "" {
		cleanPath, nErr := pathutil.NormalizeLocalPath(req.Path)
		if nErr != nil {
			http.Error(w, nErr.Error(), http.StatusBadRequest)
			return
		}
		err = client.DeleteLocal(cleanPath, req.Recursive)
	} else {
		err = s.cli.Delete(req.Node, req.Path, req.Recursive)
	}

	if err != nil {
		http.Error(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
