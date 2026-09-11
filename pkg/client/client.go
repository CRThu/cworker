package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cworker/pkg/fsengine"
	"cworker/pkg/pathutil"
	"cworker/pkg/protocol"
	"nhooyr.io/websocket"
)

// Client 封装去中心化 CLI 与各 Worker 节点的动态 DNS 解析与直接鉴权交互
type Client struct {
	dataDir    string
	httpClient *http.Client
}

// NewClient 实例化客户端
func NewClient() *Client {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dataDir := filepath.Join(home, protocol.DefaultDataDirName)
	_ = os.MkdirAll(dataDir, 0755)

	// cworker 专用于局域网、本地环回与 Tailscale 点对点直连，必须显式隔离外部系统 HTTP 代理
	tr := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}

	return &Client{
		dataDir: dataDir,
		httpClient: &http.Client{
			Transport: tr,
			Timeout:   30 * time.Second,
		},
	}
}

// 账本文件管理 (Known Nodes Store)

func (c *Client) nodesFilePath() string {
	return filepath.Join(c.dataDir, protocol.LedgerFileName)
}

// LoadKnownNodes 读取本地记忆的已知节点账本
func (c *Client) LoadKnownNodes() (map[string]protocol.KnownNode, error) {
	nodes := make(map[string]protocol.KnownNode)
	data, err := os.ReadFile(c.nodesFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nodes, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nodes, nil
	}
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, fmt.Errorf("corrupted known nodes ledger: %w", err)
	}
	return nodes, nil
}

// SaveKnownNode 保存或更新单个节点到本地账本
func (c *Client) SaveKnownNode(node protocol.KnownNode) error {
	nodes, _ := c.LoadKnownNodes()
	if nodes == nil {
		nodes = make(map[string]protocol.KnownNode)
	}
	nodes[strings.ToLower(node.Name)] = node

	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.nodesFilePath(), data, 0600)
}

// RemoveKnownNode 从账本中移除节点
func (c *Client) RemoveKnownNode(name string) error {
	nodes, err := c.LoadKnownNodes()
	if err != nil {
		return err
	}
	delete(nodes, strings.ToLower(name))

	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.nodesFilePath(), data, 0600)
}

// ResolvedTarget 封装完成动态 DNS 解析后的目标 Worker
type ResolvedTarget struct {
	Name    string
	BaseURL string
	Token   string
}

// ResolveWorker 核心动态寻址：查账本 -> 动态 DNS/MagicDNS 解析 -> 获取最新物理 IP 与 Token
func (c *Client) ResolveWorker(targetName string, explicitToken string) (*ResolvedTarget, error) {
	nodes, _ := c.LoadKnownNodes()

	var targetStr string
	var token string
	var nodeName string

	// 1. 若 targetName 为空，默认优先本机
	if targetName == "" {
		hostname, _ := os.Hostname()
		if known, ok := nodes[strings.ToLower(hostname)]; ok {
			nodeName = known.Name
			targetStr = known.Target
			token = known.Token
		} else {
			// 本机默认回退
			nodeName = hostname
			targetStr = "127.0.0.1:" + protocol.DefaultPortStr
			// 尝试读本机 token
			if b, err := os.ReadFile(filepath.Join(c.dataDir, protocol.TokenFileName)); err == nil {
				token = strings.TrimSpace(string(b))
			}
		}
	} else {
		// 2. 查本地账本
		if known, ok := nodes[strings.ToLower(targetName)]; ok {
			nodeName = known.Name
			targetStr = known.Target
			token = known.Token
		} else {
			// 未在账本中的新目标：直接将其作为 targetStr
			nodeName = targetName
			targetStr = targetName
		}
	}

	if explicitToken != "" {
		token = explicitToken
	}

	// 3. 动态 DNS 解析 (host:port)
	baseURL, err := resolveTargetToURL(targetStr)
	if err != nil {
		return nil, fmt.Errorf("resolve worker target '%s' failed: %w", targetStr, err)
	}

	return &ResolvedTarget{
		Name:    nodeName,
		BaseURL: baseURL,
		Token:   token,
	}, nil
}

func resolveTargetToURL(target string) (string, error) {
	target = strings.TrimSpace(target)
	host := target
	port := protocol.DefaultPortStr

	if h, p, err := net.SplitHostPort(target); err == nil {
		host = h
		port = p
	}

	// 实时动态 DNS 解析 (兼容 Tailscale MagicDNS、系统 mDNS 与传统 DNS)
	ips, err := net.LookupHost(host)
	if err != nil || len(ips) == 0 {
		// 解析不通时尝试原样直连
		if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
			return fmt.Sprintf("http://[%s]:%s", host, port), nil
		}
		return fmt.Sprintf("http://%s:%s", host, port), nil
	}

	// 取首个有效物理 IP (优先 IPv4)
	chosenIP := ips[0]
	for _, ip := range ips {
		if strings.Contains(ip, ".") {
			chosenIP = ip
			break
		}
	}
	if strings.Contains(chosenIP, ":") && !strings.HasPrefix(chosenIP, "[") {
		return fmt.Sprintf("http://[%s]:%s", chosenIP, port), nil
	}
	return fmt.Sprintf("http://%s:%s", chosenIP, port), nil
}

// 统一 HTTP 请求封装 (自动注入 Authorization Bearer Token)
func (c *Client) doRequest(rt *ResolvedTarget, method, path string, body io.Reader) (*http.Response, error) {
	return c.doRequestWithContext(context.Background(), rt, method, path, body)
}

func (c *Client) doRequestWithContext(ctx context.Context, rt *ResolvedTarget, method, path string, body io.Reader) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	u := rt.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}

	if rt.Token != "" {
		req.Header.Set("Authorization", "Bearer "+rt.Token)
	}
	if method == http.MethodPost || method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

// getEffectiveKnownNodes 获取已知节点账本。若本地账本为空，默认回退探查本机 (SSOT 单一事实来源)
func (c *Client) getEffectiveKnownNodes() (map[string]protocol.KnownNode, error) {
	nodes, err := c.LoadKnownNodes()
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		hostname, _ := os.Hostname()
		var localToken string
		if b, err := os.ReadFile(filepath.Join(c.dataDir, protocol.TokenFileName)); err == nil {
			localToken = strings.TrimSpace(string(b))
		}
		nodes = map[string]protocol.KnownNode{
			strings.ToLower(hostname): {
				Name:   hostname,
				Target: "127.0.0.1:" + protocol.DefaultPortStr,
				Token:  localToken,
			},
		}
	}
	return nodes, nil
}

// ListNodes 并发对账本中所有已知节点进行动态 DNS 解析与实时测活
func (c *Client) ListNodes() ([]protocol.NodeInfo, error) {
	nodes, err := c.getEffectiveKnownNodes()
	if err != nil {
		return nil, err
	}

	var mu sync.Mutex
	var list []protocol.NodeInfo
	var wg sync.WaitGroup

	for _, kn := range nodes {
		wg.Add(1)
		go func(node protocol.KnownNode) {
			defer wg.Done()
			rt, err := c.ResolveWorker(node.Name, node.Token)
			if err != nil {
				return
			}

			// 统一走 doRequestWithContext：复用 Proxy: nil 隔离系统代理，并设定 1500ms 探活超时
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()

			resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, "/api/v1/health", nil)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					var info protocol.NodeInfo
					if err := json.NewDecoder(resp.Body).Decode(&info); err == nil {
						mu.Lock()
						list = append(list, info)
						mu.Unlock()
						return
					}
				}
			}

			// 节点离线
			mu.Lock()
			list = append(list, protocol.NodeInfo{
				Name:    node.Name,
				Address: node.Target,
				Status:  protocol.NodeStatusOffline,
			})
			mu.Unlock()
		}(kn)
	}
	wg.Wait()

	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list, nil
}

// RunJob 派发任务到目标 Worker
func (c *Client) RunJob(req protocol.RunJobRequest, explicitToken string) (*protocol.JobInfo, error) {
	rt, err := c.ResolveWorker(req.Node, explicitToken)
	if err != nil {
		return nil, err
	}

	req.Node = rt.Name
	data, _ := json.Marshal(req)

	resp, err := c.doRequest(rt, http.MethodPost, "/api/v1/jobs/run", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("connect worker %s failed: %w", rt.BaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("run job failed (%d): %s", resp.StatusCode, string(body))
	}

	var info protocol.JobInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	// 契约闭环：若显式指定了 Token 且派发成功，自动记忆入账
	if explicitToken != "" && rt.Name != "" {
		nodes, _ := c.LoadKnownNodes()
		target := req.Node
		if known, ok := nodes[strings.ToLower(rt.Name)]; ok && known.Target != "" {
			target = known.Target
		} else {
			if _, _, err := net.SplitHostPort(target); err != nil && !strings.Contains(target, ":") {
				target = fmt.Sprintf("%s:%s", target, protocol.DefaultPortStr)
			}
		}
		_ = c.SaveKnownNode(protocol.KnownNode{
			Name:   rt.Name,
			Target: target,
			Token:  explicitToken,
		})
	}

	return &info, nil
}

// ListJobs 并发查询所有已知在线 Worker 的任务列表
func (c *Client) ListJobs() ([]protocol.JobInfo, error) {
	knownNodes, err := c.getEffectiveKnownNodes()
	if err != nil {
		return nil, err
	}

	var mu sync.Mutex
	var allJobs []protocol.JobInfo
	var wg sync.WaitGroup

	for _, kn := range knownNodes {
		wg.Add(1)
		go func(node protocol.KnownNode) {
			defer wg.Done()
			rt, err := c.ResolveWorker(node.Name, node.Token)
			if err != nil {
				return
			}

			resp, err := c.doRequest(rt, http.MethodGet, "/api/v1/jobs/ps", nil)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				var jobs []protocol.JobInfo
				if err := json.NewDecoder(resp.Body).Decode(&jobs); err == nil {
					mu.Lock()
					allJobs = append(allJobs, jobs...)
					mu.Unlock()
				}
			}
		}(kn)
	}
	wg.Wait()

	sort.Slice(allJobs, func(i, j int) bool {
		return allJobs[i].StartTime.After(allJobs[j].StartTime)
	})
	return allJobs, nil
}

// KillJob 终止任务
func (c *Client) KillJob(jobID string) (*protocol.JobInfo, error) {
	knownNodes, err := c.getEffectiveKnownNodes()
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	var foundInfo *protocol.JobInfo
	var mu sync.Mutex

	for _, kn := range knownNodes {
		wg.Add(1)
		go func(node protocol.KnownNode) {
			defer wg.Done()
			rt, err := c.ResolveWorker(node.Name, node.Token)
			if err != nil {
				return
			}

			data, _ := json.Marshal(protocol.KillJobRequest{JobID: jobID})
			resp, err := c.doRequest(rt, http.MethodPost, "/api/v1/jobs/kill", bytes.NewReader(data))
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				var info protocol.JobInfo
				if err := json.NewDecoder(resp.Body).Decode(&info); err == nil {
					mu.Lock()
					foundInfo = &info
					mu.Unlock()
				}
			}
		}(kn)
	}
	wg.Wait()

	if foundInfo != nil {
		return foundInfo, nil
	}
	return nil, fmt.Errorf("job '%s' not found across known workers", jobID)
}

// GetLogs 获取日志
func (c *Client) GetLogs(jobID string, lines int) (string, error) {
	rt, err := c.findJobWorker(jobID)
	if err != nil {
		return "", err
	}

	path := fmt.Sprintf("/api/v1/jobs/logs?job_id=%s&lines=%d", url.QueryEscape(jobID), lines)
	resp, err := c.doRequest(rt, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get logs failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(resp.Body)
	return string(data), err
}

// StreamLogs 实时流式日志
func (c *Client) StreamLogs(ctx context.Context, jobID string, out io.Writer) error {
	rt, err := c.findJobWorker(jobID)
	if err != nil {
		return err
	}

	wsURL := strings.Replace(rt.BaseURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL = fmt.Sprintf("%s/api/v1/jobs/stream?job_id=%s", wsURL, url.QueryEscape(jobID))
	if rt.Token != "" {
		wsURL += fmt.Sprintf("&token=%s", url.QueryEscape(rt.Token))
	}

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket connect worker %s failed: %w", rt.BaseURL, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure || ctx.Err() != nil {
				return nil
			}
			return err
		}
		_, _ = out.Write(data)
	}
}

func (c *Client) findJobWorker(jobID string) (*ResolvedTarget, error) {
	knownNodes, err := c.getEffectiveKnownNodes()
	if err != nil {
		return nil, err
	}

	for _, kn := range knownNodes {
		rt, err := c.ResolveWorker(kn.Name, kn.Token)
		if err != nil {
			continue
		}
		resp, err := c.doRequest(rt, http.MethodGet, "/api/v1/jobs/ps", nil)
		if err != nil {
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var jobs []protocol.JobInfo
			err := json.NewDecoder(resp.Body).Decode(&jobs)
			resp.Body.Close()
			if err == nil {
				for _, j := range jobs {
					if j.ID == jobID {
						return rt, nil
					}
				}
			}
		} else {
			resp.Body.Close()
		}
	}
	return nil, fmt.Errorf("job '%s' not found across known workers", jobID)
}

// 文件五件套操作

// isSafeRelativePath 检验相对路径是否安全，严禁路径遍历攻击 (CWE-22 / Zip Slip)
func isSafeRelativePath(rel string) bool {
	if rel == "" || strings.ContainsRune(rel, 0) {
		return false
	}
	// 统一转换为正斜杠分析
	slash := filepath.ToSlash(rel)
	// 拒绝绝对路径 (/ 开头)
	if strings.HasPrefix(slash, "/") {
		return false
	}
	// 拒绝包含冒号以防御 Windows 盘符与 NTFS 备用数据流 (ADS) 攻击
	if strings.ContainsRune(slash, ':') {
		return false
	}
	// 使用系统标准库 Clean
	cleaned := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" {
		return false
	}
	// 严禁包含 .. 逃逸父级目录
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || strings.HasPrefix(slash, "../") {
		return false
	}
	return true
}

func (c *Client) UploadFile(node, remotePath string, r io.Reader) error {
	return c.UploadFileWithContext(context.Background(), node, remotePath, r)
}

func (c *Client) UploadFileWithContext(ctx context.Context, node, remotePath string, r io.Reader) error {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return err
	}

	// 真正的单遍 I/O：使用 io.TeeReader 边读边向网卡发、边在内存流式算 Hash，零预读
	clientHasher := sha256.New()
	teeReader := io.TeeReader(r, clientHasher)

	path := fmt.Sprintf("/api/v1/fs/upload?path=%s", url.QueryEscape(remotePath))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, rt.BaseURL+path, teeReader)
	if err != nil {
		return err
	}
	if rt.Token != "" {
		req.Header.Set("Authorization", "Bearer "+rt.Token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed (%d): %s", resp.StatusCode, string(body))
	}

	// 传输完成，在末尾两端核对 Hash：协议契约强制要求两端 SHA-256 必须严格存在且 100% 匹配
	clientHash := hex.EncodeToString(clientHasher.Sum(nil))
	serverHash := strings.TrimSpace(resp.Header.Get("X-File-SHA256"))
	if serverHash == "" {
		_ = c.Delete(node, remotePath, false)
		return fmt.Errorf("upload failed: worker %s did not return X-File-SHA256 header (remote file deleted)", node)
	}

	if !strings.EqualFold(clientHash, serverHash) {
		// 两端 Hash 不一致，立即触发远端脏数据物理回滚
		_ = c.Delete(node, remotePath, false)
		return fmt.Errorf("sha256 checksum mismatch: client=%s, worker=%s (remote file deleted)", clientHash, serverHash)
	}

	return nil
}

func (c *Client) DownloadFile(node, remotePath string, w io.Writer, trackers ...*ProgressTracker) error {
	return c.DownloadFileWithContext(context.Background(), node, remotePath, w, trackers...)
}

func (c *Client) DownloadFileWithContext(ctx context.Context, node, remotePath string, w io.Writer, trackers ...*ProgressTracker) error {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/fs/download?path=%s", url.QueryEscape(remotePath))
	resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed (%d): %s", resp.StatusCode, string(body))
	}

	var tracker *ProgressTracker
	if len(trackers) > 0 && trackers[0] != nil {
		tracker = trackers[0]
		if szStr := resp.Header.Get("X-File-Size"); szStr != "" {
			if sz, err := strconv.ParseInt(szStr, 10, 64); err == nil && sz > 0 {
				tracker.SetTotalBytes(sz)
			}
		}
	}

	clientHasher := sha256.New()
	var finalWriter io.Writer = w
	if tracker != nil {
		finalWriter = NewCountingWriter(w, tracker)
	}
	destWriter := io.MultiWriter(finalWriter, clientHasher)

	if _, err := io.Copy(destWriter, resp.Body); err != nil {
		return err
	}

	// 读取期望 Hash：Trailer 必须在 Body 全部读取至 EOF 后从 resp.Header 或 resp.Trailer 提取
	expectedHash := strings.TrimSpace(resp.Header.Get("X-File-SHA256"))
	if expectedHash == "" && resp.Trailer != nil {
		expectedHash = strings.TrimSpace(resp.Trailer.Get("X-File-SHA256"))
	}

	if expectedHash == "" {
		return fmt.Errorf("download failed: worker %s did not return X-File-SHA256 checksum", node)
	}

	computedHash := hex.EncodeToString(clientHasher.Sum(nil))
	if !strings.EqualFold(expectedHash, computedHash) {
		return fmt.Errorf("sha256 checksum mismatch: expected %s, got %s", expectedHash, computedHash)
	}
	return nil
}

func (c *Client) ListDir(node, remotePath string, recursive ...bool) ([]protocol.FileInfo, error) {
	return c.ListDirWithContext(context.Background(), node, remotePath, recursive...)
}

func (c *Client) ListDirWithContext(ctx context.Context, node, remotePath string, recursive ...bool) ([]protocol.FileInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return nil, err
	}

	isRec := len(recursive) > 0 && recursive[0]
	path := fmt.Sprintf("/api/v1/fs/ls?path=%s&recursive=%t", url.QueryEscape(remotePath), isRec)
	resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list dir failed (%d): %s", resp.StatusCode, string(body))
	}

	var list []protocol.FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *Client) MakeDir(node, remotePath string) error {
	return c.MakeDirWithContext(context.Background(), node, remotePath)
}

func (c *Client) MakeDirWithContext(ctx context.Context, node, remotePath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/fs/md?path=%s", url.QueryEscape(remotePath))
	resp, err := c.doRequestWithContext(ctx, rt, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mkdir failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *Client) Delete(node, remotePath string, recursive bool) error {
	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/fs/rm?path=%s&recursive=%t", url.QueryEscape(remotePath), recursive)
	resp, err := c.doRequest(rt, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *Client) RelayCopy(srcNode, srcPath, dstNode, dstPath string, trackers ...*ProgressTracker) error {
	return c.RelayCopyWithContext(context.Background(), srcNode, srcPath, dstNode, dstPath, trackers...)
}

func (c *Client) RelayCopyWithContext(ctx context.Context, srcNode, srcPath, dstNode, dstPath string, trackers ...*ProgressTracker) error {
	if ctx == nil {
		ctx = context.Background()
	}
	srcTarget, err := c.ResolveWorker(srcNode, "")
	if err != nil {
		return err
	}
	dstTarget, err := c.ResolveWorker(dstNode, "")
	if err != nil {
		return err
	}

	var tracker *ProgressTracker
	if len(trackers) > 0 && trackers[0] != nil {
		tracker = trackers[0]
	}

	// 1. 发起源端读取流
	srcPathURL := fmt.Sprintf("/api/v1/fs/download?path=%s", url.QueryEscape(srcPath))
	srcResp, err := c.doRequestWithContext(ctx, srcTarget, http.MethodGet, srcPathURL, nil)
	if err != nil {
		return fmt.Errorf("connect src worker %s failed: %w", srcTarget.BaseURL, err)
	}
	defer srcResp.Body.Close()

	if srcResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(srcResp.Body)
		return fmt.Errorf("src worker returned %d: %s", srcResp.StatusCode, string(body))
	}

	if tracker != nil {
		if szStr := srcResp.Header.Get("X-File-Size"); szStr != "" {
			if sz, err := strconv.ParseInt(szStr, 10, 64); err == nil && sz > 0 {
				tracker.SetTotalBytes(sz)
			}
		}
	}

	// 2. 边从源端拉流，边在 CLI 内存计算中继 Hash，边向目的端直灌 (单遍流式，零磁盘中转)
	cliHasher := sha256.New()
	var bodyReader io.Reader = srcResp.Body
	if tracker != nil {
		bodyReader = NewCountingReader(srcResp.Body, tracker)
	}
	teeReader := io.TeeReader(bodyReader, cliHasher)

	dstPathURL := fmt.Sprintf("/api/v1/fs/upload?path=%s", url.QueryEscape(dstPath))
	dstReq, err := http.NewRequestWithContext(ctx, http.MethodPut, dstTarget.BaseURL+dstPathURL, teeReader)
	if err != nil {
		return err
	}
	if dstTarget.Token != "" {
		dstReq.Header.Set("Authorization", "Bearer "+dstTarget.Token)
	}

	dstResp, err := c.httpClient.Do(dstReq)
	if err != nil {
		return fmt.Errorf("connect dst worker %s failed: %w", dstTarget.BaseURL, err)
	}
	defer dstResp.Body.Close()

	if dstResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(dstResp.Body)
		return fmt.Errorf("dst worker returned %d: %s", dstResp.StatusCode, string(body))
	}

	// 3. 传输结束，三方 Hash 在末尾全面核对！三方必须全部存在且 100% 严格一致
	cliHash := hex.EncodeToString(cliHasher.Sum(nil))

	dstHash := strings.TrimSpace(dstResp.Header.Get("X-File-SHA256"))
	if dstHash == "" {
		_ = c.Delete(dstNode, dstPath, false)
		return fmt.Errorf("relay copy failed: dst worker %s did not return X-File-SHA256 checksum (dst file rolled back)", dstNode)
	}
	if !strings.EqualFold(cliHash, dstHash) {
		_ = c.Delete(dstNode, dstPath, false)
		return fmt.Errorf("relay copy sha256 mismatch: cli=%s, dst=%s (dst file rolled back)", cliHash, dstHash)
	}

	srcHash := strings.TrimSpace(srcResp.Header.Get("X-File-SHA256"))
	if srcHash == "" && srcResp.Trailer != nil {
		srcHash = strings.TrimSpace(srcResp.Trailer.Get("X-File-SHA256"))
	}
	if srcHash == "" {
		_ = c.Delete(dstNode, dstPath, false)
		return fmt.Errorf("relay copy failed: src worker %s did not return X-File-SHA256 checksum (dst file rolled back)", srcNode)
	}
	if !strings.EqualFold(cliHash, srcHash) {
		_ = c.Delete(dstNode, dstPath, false)
		return fmt.Errorf("relay copy sha256 mismatch: src=%s, cli=%s (dst file rolled back)", srcHash, cliHash)
	}

	return nil
}

// UploadDir 递归并发上传本地文件夹到远端节点 (两阶段目录治理 + 单遍流式 Hash + 自洽进度条)
func (c *Client) UploadDir(ctx context.Context, node, remoteBaseDir, localBaseDir string, concurrency int, tracker *ProgressTracker) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanLocal, err := pathutil.NormalizeLocalPath(localBaseDir)
	if err != nil {
		return err
	}

	type fileEntry struct {
		relPath string
		size    int64
	}
	var dirs []string
	var files []fileEntry
	var totalBytes int64

	// 阶段一：扫描本地目录，前置过滤软连接并统计待传输文件元数据
	err = filepath.Walk(cleanLocal, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == cleanLocal {
			return nil
		}

		// 忽略目录软链接以防死循环
		if info.Mode()&os.ModeSymlink != 0 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(cleanLocal, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		if info.IsDir() {
			dirs = append(dirs, relSlash)
		} else {
			files = append(files, fileEntry{
				relPath: relSlash,
				size:    info.Size(),
			})
			totalBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan local dir failed: %w", err)
	}

	if tracker == nil {
		tracker = NewProgressTracker(int64(len(files)), totalBytes)
	}

	// 阶段二：远端先建立完整的子目录骨架 (保证空目录 100% 守恒)
	if err := ctx.Err(); err != nil {
		return err
	}
	_ = c.MakeDirWithContext(ctx, node, remoteBaseDir)
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		remoteSub := pathutil.JoinRemotePath(remoteBaseDir, d)
		if err := c.MakeDirWithContext(ctx, node, remoteSub); err != nil {
			return fmt.Errorf("create remote dir '%s' failed: %w", remoteSub, err)
		}
	}

	// 阶段三：有界并发池推流传输文件
	if concurrency <= 0 {
		concurrency = 8
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var firstErr error
	var errMu sync.Mutex

concurrencyLoop:
	for _, fe := range files {
		select {
		case <-ctx.Done():
			break concurrencyLoop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(entry fileEntry) {
			defer func() {
				<-sem
				wg.Done()
			}()

			select {
			case <-ctx.Done():
				return
			default:
			}

			localFilePath := filepath.Join(cleanLocal, filepath.FromSlash(entry.relPath))
			remoteFilePath := pathutil.JoinRemotePath(remoteBaseDir, entry.relPath)

			f, err := os.Open(localFilePath)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("open local file failed: %w", err)
					cancel()
				}
				errMu.Unlock()
				return
			}

			var r io.Reader = f
			if tracker != nil {
				r = NewCountingReader(f, tracker)
			}

			uploadErr := c.UploadFileWithContext(ctx, node, remoteFilePath, r)
			_ = f.Close()

			if uploadErr != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("upload '%s' failed: %w", entry.relPath, uploadErr)
					cancel()
				}
				errMu.Unlock()
				return
			}

			if tracker != nil {
				tracker.AddFile()
			}
		}(fe)
	}

	wg.Wait()
	if tracker != nil {
		tracker.Finish()
	}
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// DownloadDir 递归并发下载远端目录到本地 (支持进度展示、空目录保留与路径遍历严密防御)
func (c *Client) DownloadDir(ctx context.Context, node, remoteBaseDir, localBaseDir string, concurrency int, tracker *ProgressTracker) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanLocal, err := pathutil.NormalizeLocalPath(localBaseDir)
	if err != nil {
		return err
	}

	// 阶段一：单次请求递归获取远端目录树
	entries, err := c.ListDirWithContext(ctx, node, remoteBaseDir, true)
	if err != nil {
		return fmt.Errorf("list remote dir failed: %w", err)
	}

	type remoteFile struct {
		remotePath string
		relPath    string
		size       int64
	}
	var dirs []string
	var files []remoteFile
	var totalBytes int64

	for _, e := range entries {
		rel := strings.TrimLeft(filepath.ToSlash(e.Path), "/")
		if rel == "" {
			continue
		}

		// 严密防御 CWE-22 路径遍历与 Zip Slip 风险
		if !isSafeRelativePath(rel) {
			return fmt.Errorf("security: illegal path traversal in remote entry: %q", e.Path)
		}

		if e.IsDir {
			dirs = append(dirs, rel)
		} else {
			files = append(files, remoteFile{
				remotePath: pathutil.JoinRemotePath(remoteBaseDir, rel),
				relPath:    rel,
				size:       e.Size,
			})
			totalBytes += e.Size
		}
	}

	if tracker == nil {
		tracker = NewProgressTracker(int64(len(files)), totalBytes)
	}

	// 阶段二：本地先创建全部子目录骨架 (保证空目录存在且路径绝不逃逸)
	_ = os.MkdirAll(cleanLocal, 0755)
	for _, d := range dirs {
		localSub := filepath.Join(cleanLocal, filepath.FromSlash(d))
		relCheck, err := filepath.Rel(cleanLocal, localSub)
		if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
			return fmt.Errorf("security: path escapes destination directory: %q", d)
		}
		if err := os.MkdirAll(localSub, 0755); err != nil {
			return fmt.Errorf("create local dir '%s' failed: %w", localSub, err)
		}
	}

	// 阶段三：并发下载文件
	if concurrency <= 0 {
		concurrency = 8
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var firstErr error
	var errMu sync.Mutex

concurrencyLoop:
	for _, rf := range files {
		select {
		case <-ctx.Done():
			break concurrencyLoop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(file remoteFile) {
			defer func() {
				<-sem
				wg.Done()
			}()

			select {
			case <-ctx.Done():
				return
			default:
			}

			localFilePath := filepath.Join(cleanLocal, filepath.FromSlash(file.relPath))
			relCheck, err := filepath.Rel(cleanLocal, localFilePath)
			if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("security: path escapes destination directory: %q", file.relPath)
					cancel()
				}
				errMu.Unlock()
				return
			}
			localDir := filepath.Dir(localFilePath)
			_ = os.MkdirAll(localDir, 0755)

			tmpLocal := filepath.Join(localDir, fmt.Sprintf(".%s.cwtemp-%d", filepath.Base(localFilePath), time.Now().UnixNano()))
			f, err := os.OpenFile(tmpLocal, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("create local temp file failed: %w", err)
					cancel()
				}
				errMu.Unlock()
				return
			}
			committed := false
			defer func() {
				_ = f.Close()
				if !committed {
					_ = os.Remove(tmpLocal)
				}
			}()

			var w io.Writer = f
			if tracker != nil {
				w = NewCountingWriter(f, tracker)
			}

			if err := c.DownloadFileWithContext(ctx, node, file.remotePath, w); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("download '%s' failed: %w", file.relPath, err)
					cancel()
				}
				errMu.Unlock()
				return
			}
			if err := f.Close(); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("close local file '%s' failed: %w", file.relPath, err)
					cancel()
				}
				errMu.Unlock()
				return
			}

			if err := os.Rename(tmpLocal, localFilePath); err != nil {
				_ = os.Remove(localFilePath)
				if err2 := os.Rename(tmpLocal, localFilePath); err2 != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("commit local file '%s' failed: %w", file.relPath, err2)
						cancel()
					}
					errMu.Unlock()
					return
				}
			}
			committed = true

			if tracker != nil {
				tracker.AddFile()
			}
		}(rf)
	}

	wg.Wait()
	if tracker != nil {
		tracker.Finish()
	}
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// RelayCopyDir 跨机递归中继拷贝目录 (内存管道直连对穿，零磁盘中转与路径安全校验)
func (c *Client) RelayCopyDir(ctx context.Context, srcNode, srcBaseDir, dstNode, dstBaseDir string, concurrency int, tracker *ProgressTracker) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := c.ListDirWithContext(ctx, srcNode, srcBaseDir, true)
	if err != nil {
		return fmt.Errorf("list src dir failed: %w", err)
	}

	type relayFile struct {
		srcPath string
		dstPath string
		size    int64
	}
	var dirs []string
	var files []relayFile
	var totalBytes int64

	for _, e := range entries {
		rel := strings.TrimLeft(filepath.ToSlash(e.Path), "/")
		if rel == "" {
			continue
		}

		// 严密防御跨节点路径注入
		if !isSafeRelativePath(rel) {
			return fmt.Errorf("security: illegal path traversal in remote entry: %q", e.Path)
		}

		if e.IsDir {
			dirs = append(dirs, rel)
		} else {
			files = append(files, relayFile{
				srcPath: pathutil.JoinRemotePath(srcBaseDir, rel),
				dstPath: pathutil.JoinRemotePath(dstBaseDir, rel),
				size:    e.Size,
			})
			totalBytes += e.Size
		}
	}

	if tracker == nil {
		tracker = NewProgressTracker(int64(len(files)), totalBytes)
	}

	// 目的端预建目录
	if err := ctx.Err(); err != nil {
		return err
	}
	_ = c.MakeDirWithContext(ctx, dstNode, dstBaseDir)
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		dstSub := pathutil.JoinRemotePath(dstBaseDir, d)
		if err := c.MakeDirWithContext(ctx, dstNode, dstSub); err != nil {
			return fmt.Errorf("create dst dir '%s' failed: %w", dstSub, err)
		}
	}

	if concurrency <= 0 {
		concurrency = 8
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var firstErr error
	var errMu sync.Mutex

concurrencyLoop:
	for _, rf := range files {
		select {
		case <-ctx.Done():
			break concurrencyLoop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(item relayFile) {
			defer func() {
				<-sem
				wg.Done()
			}()

			select {
			case <-ctx.Done():
				return
			default:
			}

			if err := c.RelayCopyWithContext(ctx, srcNode, item.srcPath, dstNode, item.dstPath, tracker); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("relay copy '%s' failed: %w", item.srcPath, err)
					cancel()
				}
				errMu.Unlock()
				return
			}

			if tracker != nil {
				tracker.AddFile()
			}
		}(rf)
	}

	wg.Wait()
	if tracker != nil {
		tracker.Finish()
	}
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// LocalCopyFile 本地文件安全原子拷贝 (流式拷贝，同时计算 SHA-256 并原子写入)
func (c *Client) LocalCopyFile(srcPath, dstPath string, tracker *ProgressTracker) error {
	var l fsengine.ProgressListener
	if tracker != nil {
		l = tracker
	}
	return fsengine.CopyFile(srcPath, dstPath, l)
}

// LocalCopyDir 本地目录递归并发拷贝
func (c *Client) LocalCopyDir(ctx context.Context, srcBaseDir, dstBaseDir string, concurrency int, tracker *ProgressTracker) error {
	var l fsengine.ProgressListener
	if tracker != nil {
		l = tracker
	}
	err := fsengine.CopyDir(ctx, srcBaseDir, dstBaseDir, concurrency, l)
	if tracker != nil {
		tracker.Finish()
	}
	return err
}

// HashRemotePath 获取远端路径的文件/目录 SHA-256 清单
func (c *Client) HashRemotePath(ctx context.Context, node, remotePath string, recursive bool) ([]protocol.FileInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/api/v1/fs/hash?path=%s&recursive=%t", url.QueryEscape(remotePath), recursive)
	resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hash remote path failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var list []protocol.FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

// HashLocalPath 获取本地路径的文件/目录 SHA-256 清单 (与远端 handleFsHash 契约保持一致)
func HashLocalPath(localPath string, recursive bool) ([]protocol.FileInfo, error) {
	return fsengine.Hash(localPath, recursive)
}

// CompareFileInfos 对比源端与目标端的文件清单并生成 Diff 汇总结果
func CompareFileInfos(srcFiles, dstFiles []protocol.FileInfo) *protocol.DiffResult {
	srcMap := make(map[string]protocol.FileInfo, len(srcFiles))
	dstMap := make(map[string]protocol.FileInfo, len(dstFiles))
	allPathsMap := make(map[string]struct{}, len(srcFiles)+len(dstFiles))

	for _, sf := range srcFiles {
		p := filepath.ToSlash(sf.Path)
		srcMap[p] = sf
		allPathsMap[p] = struct{}{}
	}
	for _, df := range dstFiles {
		p := filepath.ToSlash(df.Path)
		dstMap[p] = df
		allPathsMap[p] = struct{}{}
	}

	allPaths := make([]string, 0, len(allPathsMap))
	for p := range allPathsMap {
		allPaths = append(allPaths, p)
	}
	sort.Strings(allPaths)

	res := &protocol.DiffResult{
		Entries: make([]protocol.DiffEntry, 0, len(allPaths)),
	}

	for _, p := range allPaths {
		sf, inSrc := srcMap[p]
		df, inDst := dstMap[p]

		if inSrc && inDst {
			if sf.Size != df.Size {
				res.Modified++
				res.Entries = append(res.Entries, protocol.DiffEntry{
					Status:  protocol.DiffStatusModified,
					Path:    p,
					SrcSize: sf.Size,
					DstSize: df.Size,
					SrcHash: sf.SHA256,
					DstHash: df.SHA256,
				})
			} else if sf.SHA256 != "" && df.SHA256 != "" && strings.EqualFold(sf.SHA256, df.SHA256) {
				res.Matched++
				res.Entries = append(res.Entries, protocol.DiffEntry{
					Status:  protocol.DiffStatusMatch,
					Path:    p,
					SrcSize: sf.Size,
					DstSize: df.Size,
					SrcHash: sf.SHA256,
					DstHash: df.SHA256,
				})
			} else {
				res.Modified++
				res.Entries = append(res.Entries, protocol.DiffEntry{
					Status:  protocol.DiffStatusModified,
					Path:    p,
					SrcSize: sf.Size,
					DstSize: df.Size,
					SrcHash: sf.SHA256,
					DstHash: df.SHA256,
				})
			}
		} else if inSrc && !inDst {
			res.Added++
			res.Entries = append(res.Entries, protocol.DiffEntry{
				Status:  protocol.DiffStatusAdded,
				Path:    p,
				SrcSize: sf.Size,
				DstSize: 0,
				SrcHash: sf.SHA256,
				DstHash: "",
			})
		} else if !inSrc && inDst {
			res.Deleted++
			res.Entries = append(res.Entries, protocol.DiffEntry{
				Status:  protocol.DiffStatusDeleted,
				Path:    p,
				SrcSize: 0,
				DstSize: df.Size,
				SrcHash: "",
				DstHash: df.SHA256,
			})
		}
	}

	return res
}

// CompareSingleFile 比对两个单文件并返回差异结果
func CompareSingleFile(srcFile, dstFile protocol.FileInfo) *protocol.DiffResult {
	status := protocol.DiffStatusModified
	matched := 0
	modified := 0

	if srcFile.Size == dstFile.Size && strings.EqualFold(srcFile.SHA256, dstFile.SHA256) {
		status = protocol.DiffStatusMatch
		matched = 1
	} else {
		modified = 1
	}

	return &protocol.DiffResult{
		Entries: []protocol.DiffEntry{
			{
				Status:  status,
				Path:    srcFile.Name,
				SrcSize: srcFile.Size,
				DstSize: dstFile.Size,
				SrcHash: srcFile.SHA256,
				DstHash: dstFile.SHA256,
			},
		},
		Matched:  matched,
		Modified: modified,
	}
}

// ListLocalDir 列出本地目录中的条目 (与远端 handleFsList 保持契约一致)
func ListLocalDir(localPath string) ([]protocol.FileInfo, error) {
	return fsengine.ListDir(localPath, false)
}

// DeleteLocal 删除本地文件或目录 (与远端 handleFsRemove 保持契约一致)
func DeleteLocal(localPath string, recursive bool) error {
	return fsengine.Remove(localPath, recursive)
}

// MakeLocalDir 在本地递归创建目录 (与远端 handleFsMakeDir 保持契约一致)
func MakeLocalDir(localPath string) error {
	return fsengine.MakeDir(localPath)
}

