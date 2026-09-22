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
	dataDir      string
	httpClient   *http.Client // RPC client: 30s 默认超时，防控制面请求死锁挂死
	streamClient *http.Client // Stream client: Timeout 0，无全局硬超时，由 Context 精确控制流式与大文件生命周期
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
		streamClient: &http.Client{
			Transport: tr,
			Timeout:   0, // 无全局硬编码超时，生命周期完全受 context.Context 约束
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
			// 遍历账本按 Name 或 Target 模糊匹配
			found := false
			for _, kn := range nodes {
				if strings.EqualFold(kn.Name, targetName) || strings.EqualFold(kn.Target, targetName) {
					nodeName = kn.Name
					targetStr = kn.Target
					token = kn.Token
					found = true
					break
				}
			}
			if !found {
				// 未在账本中的新目标：直接将其作为 targetStr
				nodeName = targetName
				targetStr = targetName
			}
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

// 统一流式 HTTP 请求封装 (使用 streamClient，无 30s 硬超时截断，生命周期完全由 Context 精准控制)
func (c *Client) doStreamRequestWithContext(ctx context.Context, rt *ResolvedTarget, method, path string, body io.Reader) (*http.Response, error) {
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

	return c.streamClient.Do(req)
}

// ListNodes 并发对账本中所有已知节点进行动态 DNS 解析与实时测活
func (c *Client) ListNodes() ([]protocol.NodeInfo, error) {
	return c.ListNodesWithContext(context.Background())
}

// ListNodesWithContext 带 Context 控制的并发节点测活
func (c *Client) ListNodesWithContext(ctx context.Context) ([]protocol.NodeInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	nodes, err := c.LoadKnownNodes()
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
			select {
			case <-ctx.Done():
				return
			default:
			}

			rt, err := c.ResolveWorker(node.Name, node.Token)
			if err != nil {
				mu.Lock()
				list = append(list, protocol.NodeInfo{
					Name:    node.Name,
					Address: node.Target,
					Status:  protocol.NodeStatusOffline,
				})
				mu.Unlock()
				return
			}

			// 统一走 doRequestWithContext：复用 Proxy: nil 隔离系统代理，设定 3000ms 探活超时以覆盖 Tailscale/异地冷启动打洞 (受父 ctx 约束)
			probeCtx, cancel := context.WithTimeout(ctx, 3000*time.Millisecond)
			defer cancel()

			resp, err := c.doRequestWithContext(probeCtx, rt, http.MethodGet, "/api/v1/health", nil)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					var info protocol.NodeInfo
					if err := json.NewDecoder(resp.Body).Decode(&info); err == nil {
						if node.Name != "" {
							info.Name = node.Name
						}
						info.Address = strings.TrimPrefix(strings.TrimPrefix(rt.BaseURL, "http://"), "https://")
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

	if err := ctx.Err(); err != nil && len(list) == 0 {
		return nil, err
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list, nil
}

// RunJob 派发任务到目标 Worker
func (c *Client) RunJob(req protocol.RunJobRequest, explicitToken string) (*protocol.JobInfo, error) {
	return c.RunJobWithContext(context.Background(), req, explicitToken)
}

// RunJobWithContext 带 Context 支持的任务派发
func (c *Client) RunJobWithContext(ctx context.Context, req protocol.RunJobRequest, explicitToken string) (*protocol.JobInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.ResolveWorker(req.Node, explicitToken)
	if err != nil {
		return nil, err
	}

	req.Node = rt.Name
	data, _ := json.Marshal(req)

	resp, err := c.doRequestWithContext(ctx, rt, http.MethodPost, "/api/v1/jobs/run", bytes.NewReader(data))
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
	if rt.Name != "" {
		info.Node = rt.Name
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

// ListJobs 查询指定节点或全集群所有已知在线 Worker 的任务列表 (targetNode 可选)
func (c *Client) ListJobs(targetNode ...string) ([]protocol.JobInfo, error) {
	return c.ListJobsWithContext(context.Background(), targetNode...)
}

// ListJobsWithContext 带 Context 支持的任务列表查询
func (c *Client) ListJobsWithContext(ctx context.Context, targetNode ...string) ([]protocol.JobInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	node := ""
	if len(targetNode) > 0 {
		node = targetNode[0]
	}

	if node != "" {
		rt, err := c.ResolveWorker(node, "")
		if err != nil {
			return nil, err
		}
		resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, "/api/v1/jobs/ps", nil)
		if err != nil {
			return nil, fmt.Errorf("list jobs from node '%s' failed: %w", node, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("list jobs from node '%s' failed (%d): %s", node, resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var jobs []protocol.JobInfo
		if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
			return nil, err
		}
		for i := range jobs {
			if rt.Name != "" {
				jobs[i].Node = rt.Name
			}
		}
		sort.Slice(jobs, func(i, j int) bool {
			return jobs[i].StartTime.After(jobs[j].StartTime)
		})
		return jobs, nil
	}

	knownNodes, err := c.LoadKnownNodes()
	if err != nil {
		return nil, err
	}

	var mu sync.Mutex
	var allJobs []protocol.JobInfo
	var wg sync.WaitGroup

	for _, kn := range knownNodes {
		wg.Add(1)
		go func(kn protocol.KnownNode) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			default:
			}

			rt, err := c.ResolveWorker(kn.Name, kn.Token)
			if err != nil {
				return
			}

			resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, "/api/v1/jobs/ps", nil)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				var jobs []protocol.JobInfo
				if err := json.NewDecoder(resp.Body).Decode(&jobs); err == nil {
					for i := range jobs {
						if kn.Name != "" {
							jobs[i].Node = kn.Name
						}
					}
					mu.Lock()
					allJobs = append(allJobs, jobs...)
					mu.Unlock()
				}
			}
		}(kn)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil && len(allJobs) == 0 {
		return nil, err
	}

	sort.Slice(allJobs, func(i, j int) bool {
		return allJobs[i].StartTime.After(allJobs[j].StartTime)
	})
	return allJobs, nil
}

// KillJobNode 定向终止指定节点上的任务 (单点直达，避免全集群广播开销)
func (c *Client) KillJobNode(node string, jobID string) (*protocol.JobInfo, error) {
	return c.KillJobNodeWithContext(context.Background(), node, jobID)
}

// KillJobNodeWithContext 带 Context 支持的定向任务终止
func (c *Client) KillJobNodeWithContext(ctx context.Context, node string, jobID string) (*protocol.JobInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.resolveTargetForJobWithContext(ctx, node, jobID)
	if err != nil {
		return nil, err
	}

	data, _ := json.Marshal(protocol.KillJobRequest{JobID: jobID})
	resp, err := c.doRequestWithContext(ctx, rt, http.MethodPost, "/api/v1/jobs/kill", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("kill job on node '%s' failed: %w", node, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kill job on node '%s' failed (%d): %s", node, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var info protocol.JobInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	if rt.Name != "" {
		info.Node = rt.Name
	}
	return &info, nil
}

// KillJob 终止任务 (未指定节点时全集群广播探测)
func (c *Client) KillJob(jobID string) (*protocol.JobInfo, error) {
	return c.KillJobWithContext(context.Background(), jobID)
}

// KillJobWithContext 带 Context 支持的全集群广播任务终止
func (c *Client) KillJobWithContext(ctx context.Context, jobID string) (*protocol.JobInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	knownNodes, err := c.LoadKnownNodes()
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
			select {
			case <-ctx.Done():
				return
			default:
			}

			rt, err := c.ResolveWorker(node.Name, node.Token)
			if err != nil {
				return
			}

			data, _ := json.Marshal(protocol.KillJobRequest{JobID: jobID})
			resp, err := c.doRequestWithContext(ctx, rt, http.MethodPost, "/api/v1/jobs/kill", bytes.NewReader(data))
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				var info protocol.JobInfo
				if err := json.NewDecoder(resp.Body).Decode(&info); err == nil {
					if node.Name != "" {
						info.Node = node.Name
					}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("job '%s' not found across known workers", jobID)
}

// CleanJobs 向指定节点或全集群在线节点下发任务与日志清理指令
func (c *Client) CleanJobs(targetNode string, days int, all bool) (map[string]protocol.CleanJobsResponse, error) {
	return c.CleanJobsWithContext(context.Background(), targetNode, days, all)
}

// CleanJobsWithContext 带 Context 支持的任务清理
func (c *Client) CleanJobsWithContext(ctx context.Context, targetNode string, days int, all bool) (map[string]protocol.CleanJobsResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	reqData, err := json.Marshal(protocol.CleanJobsRequest{
		Days: days,
		All:  all,
	})
	if err != nil {
		return nil, err
	}

	results := make(map[string]protocol.CleanJobsResponse)
	var mu sync.Mutex

	if targetNode != "" {
		rt, err := c.ResolveWorker(targetNode, "")
		if err != nil {
			return nil, err
		}
		resp, err := c.doRequestWithContext(ctx, rt, http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(reqData))
		if err != nil {
			return nil, fmt.Errorf("clean jobs on node %s failed: %w", targetNode, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("clean jobs on node %s failed (%d): %s", targetNode, resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var cleanResp protocol.CleanJobsResponse
		if err := json.NewDecoder(resp.Body).Decode(&cleanResp); err != nil {
			return nil, err
		}
		results[rt.Name] = cleanResp
		return results, nil
	}

	// 全集群并发清理
	knownNodes, err := c.LoadKnownNodes()
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	for _, kn := range knownNodes {
		wg.Add(1)
		go func(node protocol.KnownNode) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			default:
			}

			rt, err := c.ResolveWorker(node.Name, node.Token)
			if err != nil {
				return
			}
			resp, err := c.doRequestWithContext(ctx, rt, http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(reqData))
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				var cleanResp protocol.CleanJobsResponse
				if err := json.NewDecoder(resp.Body).Decode(&cleanResp); err == nil {
					mu.Lock()
					results[rt.Name] = cleanResp
					mu.Unlock()
				}
			}
		}(kn)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil && len(results) == 0 {
		return nil, err
	}

	return results, nil
}

// CleanJobWithContext 精准清理单个已终态任务及其磁盘日志目录
func (c *Client) CleanJobWithContext(ctx context.Context, targetNode string, jobID string) (*protocol.CleanJobsResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.resolveTargetForJobWithContext(ctx, targetNode, jobID)
	if err != nil {
		return nil, err
	}

	reqData, err := json.Marshal(protocol.CleanJobsRequest{
		JobID: jobID,
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequestWithContext(ctx, rt, http.MethodPost, "/api/v1/jobs/clean", bytes.NewReader(reqData))
	if err != nil {
		return nil, fmt.Errorf("clean job '%s' on node '%s' failed: %w", jobID, rt.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("clean job '%s' on node '%s' failed (%d): %s", jobID, rt.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var cleanResp protocol.CleanJobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&cleanResp); err != nil {
		return nil, err
	}
	return &cleanResp, nil
}

// GetJobInfoWithContext 定向获取指定任务的最新实时状态与退出码
func (c *Client) GetJobInfoWithContext(ctx context.Context, targetNode string, jobID string) (*protocol.JobInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.resolveTargetForJobWithContext(ctx, targetNode, jobID)
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, "/api/v1/jobs/ps", nil)
	if err != nil {
		return nil, fmt.Errorf("query jobs on node '%s' failed: %w", rt.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query jobs on node '%s' failed (%d): %s", rt.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var jobs []protocol.JobInfo
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, err
	}

	for _, j := range jobs {
		if j.ID == jobID {
			if j.Node == "" {
				j.Node = rt.Name
			}
			return &j, nil
		}
	}

	return nil, fmt.Errorf("job '%s' not found on node '%s'", jobID, rt.Name)
}

func (c *Client) resolveTargetForJob(node string, jobID string) (*ResolvedTarget, error) {
	return c.resolveTargetForJobWithContext(context.Background(), node, jobID)
}

func (c *Client) resolveTargetForJobWithContext(ctx context.Context, node string, jobID string) (*ResolvedTarget, error) {
	if node != "" {
		knownNodes, err := c.LoadKnownNodes()
		if err == nil {
			if kn, ok := knownNodes[strings.ToLower(node)]; ok {
				return c.ResolveWorker(kn.Name, kn.Token)
			}
			for _, kn := range knownNodes {
				if strings.EqualFold(kn.Name, node) || strings.EqualFold(kn.Target, node) {
					return c.ResolveWorker(kn.Name, kn.Token)
				}
			}
		}
		// 若确实未在账本中，但输入的是显式 IP:端口 或 hostname:端口，允许直连
		if strings.Contains(node, ":") {
			return c.ResolveWorker(node, "")
		}
		return nil, fmt.Errorf("node '%s' not found in known nodes ledger", node)
	}
	return c.findJobWorkerWithContext(ctx, jobID)
}

// GetLogs 获取日志 (自动探测节点)
func (c *Client) GetLogs(jobID string, lines int) (string, error) {
	return c.GetLogsWithContext(context.Background(), jobID, lines)
}

// GetLogsWithContext 带 Context 支持的自动探测日志查询
func (c *Client) GetLogsWithContext(ctx context.Context, jobID string, lines int) (string, error) {
	return c.GetLogsNodeWithContext(ctx, "", jobID, lines)
}

// GetLogsNode 获取指定节点上的日志
func (c *Client) GetLogsNode(node string, jobID string, lines int) (string, error) {
	return c.GetLogsNodeWithContext(context.Background(), node, jobID, lines)
}

// GetLogsWithOptionsWithContext 带切片选项与 Context 支持的日志查询 (支持 head/tail/range/all)
func (c *Client) GetLogsWithOptionsWithContext(ctx context.Context, node string, jobID string, opts protocol.TextSliceOptions) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := c.resolveTargetForJobWithContext(ctx, node, jobID)
	if err != nil {
		return "", err
	}

	queryParams := url.Values{}
	queryParams.Set("job_id", jobID)
	if opts.All {
		queryParams.Set("all", "true")
	}
	if opts.Tail > 0 {
		queryParams.Set("tail", strconv.Itoa(opts.Tail))
	}
	if opts.Head > 0 {
		queryParams.Set("head", strconv.Itoa(opts.Head))
	}
	if opts.LineRange != "" {
		queryParams.Set("range", opts.LineRange)
	}

	path := fmt.Sprintf("/api/v1/jobs/logs?%s", queryParams.Encode())
	resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, path, nil)
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

// GetLogsNodeWithContext 带 Context 支持的指定节点日志查询
func (c *Client) GetLogsNodeWithContext(ctx context.Context, node string, jobID string, lines int) (string, error) {
	opts := protocol.TextSliceOptions{}
	if lines > 0 {
		opts.Tail = lines
	}
	return c.GetLogsWithOptionsWithContext(ctx, node, jobID, opts)
}

// StreamLogs 实时流式日志 (自动探测节点)
func (c *Client) StreamLogs(ctx context.Context, jobID string, out io.Writer) error {
	return c.StreamLogsNode(ctx, "", jobID, out)
}

// StreamLogsNode 实时流式日志 (指定或探测节点)
func (c *Client) StreamLogsNode(ctx context.Context, node string, jobID string, out io.Writer) error {
	rt, err := c.resolveTargetForJobWithContext(ctx, node, jobID)
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

	// 解除 nhooyr.io/websocket 默认 32KB (32768 字节) 单帧读取保护限制，防止大历史积压或长终端输出熔断
	conn.SetReadLimit(-1)

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
	return c.findJobWorkerWithContext(context.Background(), jobID)
}

// findJobWorkerWithContext 并发探测账本节点，带 Fast-path 极速熔断与 Context 生命周期保护
func (c *Client) findJobWorkerWithContext(ctx context.Context, jobID string) (*ResolvedTarget, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	knownNodes, err := c.LoadKnownNodes()
	if err != nil {
		return nil, err
	}
	if len(knownNodes) == 0 {
		return nil, fmt.Errorf("no known nodes configured in ledger")
	}

	probeCtx, cancelProbe := context.WithCancel(ctx)
	defer cancelProbe()

	var result *ResolvedTarget
	var once sync.Once

	// 阶段一：并发探测内存中活跃或保留的任务 (/api/v1/jobs/ps)
	var wg sync.WaitGroup
	for _, kn := range knownNodes {
		wg.Add(1)
		go func(node protocol.KnownNode) {
			defer wg.Done()
			select {
			case <-probeCtx.Done():
				return
			default:
			}

			rt, err := c.ResolveWorker(node.Name, node.Token)
			if err != nil {
				return
			}
			resp, err := c.doRequestWithContext(probeCtx, rt, http.MethodGet, "/api/v1/jobs/ps", nil)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				var jobs []protocol.JobInfo
				if err := json.NewDecoder(resp.Body).Decode(&jobs); err == nil {
					for _, j := range jobs {
						if j.ID == jobID {
							once.Do(func() {
								result = rt
								cancelProbe() // Fast-path: 立即熔断其余正在进行的探测！
							})
							return
						}
					}
				}
			}
		}(kn)
	}
	wg.Wait()

	if result != nil {
		return result, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 阶段二：若内存中未找到，并发探测磁盘历史日志 (/api/v1/jobs/logs?job_id=...&lines=1)
	probeCtx2, cancelProbe2 := context.WithCancel(ctx)
	defer cancelProbe2()

	var once2 sync.Once
	for _, kn := range knownNodes {
		wg.Add(1)
		go func(node protocol.KnownNode) {
			defer wg.Done()
			select {
			case <-probeCtx2.Done():
				return
			default:
			}

			rt, err := c.ResolveWorker(node.Name, node.Token)
			if err != nil {
				return
			}
			path := fmt.Sprintf("/api/v1/jobs/logs?job_id=%s&lines=1", url.QueryEscape(jobID))
			resp, err := c.doRequestWithContext(probeCtx2, rt, http.MethodGet, path, nil)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				once2.Do(func() {
					result = rt
					cancelProbe2() // Fast-path 熔断！
				})
			}
		}(kn)
	}
	wg.Wait()

	if result != nil {
		return result, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
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

	resp, err := c.streamClient.Do(req)
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
	resp, err := c.doStreamRequestWithContext(ctx, rt, http.MethodGet, path, nil)
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

// DownloadToLocalFile 将远端文件流式下载并原子落盘至本地物理文件 (委托 fsengine.SaveStreamWithValidator 统一维护物理落盘与校验)
func (c *Client) DownloadToLocalFile(ctx context.Context, node, remotePath, localFilePath string, tracker *ProgressTracker) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cleanLocal, err := pathutil.NormalizeLocalPath(localFilePath)
	if err != nil {
		return err
	}

	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/fs/download?path=%s", url.QueryEscape(remotePath))
	resp, err := c.doStreamRequestWithContext(ctx, rt, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if tracker != nil {
		if szStr := resp.Header.Get("X-File-Size"); szStr != "" {
			if sz, err := strconv.ParseInt(szStr, 10, 64); err == nil && sz > 0 {
				tracker.SetTotalBytes(sz)
			}
		}
	}

	var r io.Reader = resp.Body
	if tracker != nil {
		r = NewCountingReader(resp.Body, tracker)
	}

	_, err = fsengine.SaveStreamWithValidator(cleanLocal, r, func(computedHash string) error {
		expectedHash := strings.TrimSpace(resp.Header.Get("X-File-SHA256"))
		if expectedHash == "" && resp.Trailer != nil {
			expectedHash = strings.TrimSpace(resp.Trailer.Get("X-File-SHA256"))
		}
		if expectedHash == "" {
			return fmt.Errorf("download failed: worker %s did not return X-File-SHA256 checksum", node)
		}
		if !strings.EqualFold(expectedHash, computedHash) {
			return fmt.Errorf("sha256 checksum mismatch: expected %s, got %s", expectedHash, computedHash)
		}
		return nil
	})
	return err
}

// GetRoots 获取指定节点（或本地）可用盘符列表
func (c *Client) GetRoots(node string) ([]string, error) {
	return c.GetRootsWithContext(context.Background(), node)
}

// GetRootsWithContext 获取指定节点（或本地）可用盘符列表
func (c *Client) GetRootsWithContext(ctx context.Context, node string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if node == "" {
		return pathutil.GetAvailableDrives(), nil
	}

	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, "/api/v1/fs/roots", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get roots failed (%d): %s", resp.StatusCode, string(body))
	}

	var roots []string
	if err := json.NewDecoder(resp.Body).Decode(&roots); err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		roots = []string{"C:/"}
	}
	return roots, nil
}

func (c *Client) ListDir(node, remotePath string, recursive ...bool) ([]protocol.FileInfo, error) {
	return c.ListDirWithContext(context.Background(), node, remotePath, recursive...)
}

func (c *Client) ListDirWithContext(ctx context.Context, node, remotePath string, recursive ...bool) ([]protocol.FileInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	isRec := len(recursive) > 0 && recursive[0]
	if node == "" {
		cleanLocal, err := pathutil.NormalizeLocalPath(remotePath)
		if err != nil {
			return nil, err
		}
		return fsengine.ListDir(cleanLocal, isRec)
	}

	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return nil, err
	}

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
	if node == "" {
		cleanLocal, err := pathutil.NormalizeLocalPath(remotePath)
		if err != nil {
			return err
		}
		return fsengine.MakeDir(cleanLocal)
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
	return c.DeleteWithContext(context.Background(), node, remotePath, recursive)
}

func (c *Client) DeleteWithContext(ctx context.Context, node, remotePath string, recursive bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if node == "" {
		cleanLocal, err := pathutil.NormalizeLocalPath(remotePath)
		if err != nil {
			return err
		}
		return fsengine.Remove(cleanLocal, recursive)
	}

	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/fs/rm?path=%s&recursive=%t", url.QueryEscape(remotePath), recursive)
	resp, err := c.doRequestWithContext(ctx, rt, http.MethodPost, path, nil)
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

	// 1. 发起源端读取流 (使用 streamClient，无 30s 截断)
	srcPathURL := fmt.Sprintf("/api/v1/fs/download?path=%s", url.QueryEscape(srcPath))
	srcResp, err := c.doStreamRequestWithContext(ctx, srcTarget, http.MethodGet, srcPathURL, nil)
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

	// 2. 边从源端拉流，边在 CLI 内存计算中继 Hash，边向目的端直灌 (单遍流式，零磁盘中转，使用 streamClient)
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

	dstResp, err := c.streamClient.Do(dstReq)
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

// Cat 打印或输出远端节点或本地文件的内容 (统一本地与远端，默认对超 1MB 内容截取末尾 100 行)
func (c *Client) Cat(ctx context.Context, node, path string, w io.Writer) error {
	return c.CatWithSlice(ctx, node, path, protocol.TextSliceOptions{}, w)
}

// CatWithSlice 带切片选项与 1MB 智能防线的文本输出 (统一本地与远端)
func (c *Client) CatWithSlice(ctx context.Context, node, path string, opts protocol.TextSliceOptions, w io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if node == "" {
		cleanLocal, err := pathutil.NormalizeLocalPath(path)
		if err != nil {
			return err
		}
		f, err := os.Open(cleanLocal)
		if err != nil {
			return err
		}
		defer f.Close()

		truncated, err := fsengine.SliceFile(f, opts, w)
		if err != nil {
			return err
		}
		if truncated {
			fi, _ := f.Stat()
			sizeMB := float64(fi.Size()) / (1024 * 1024)
			_, _ = fmt.Fprintf(w, "\n[NOTICE] File size (%.2f MB) exceeds 1 MB limit. Truncated to last 100 lines. Use -n, --head, -L, or --all to override.\n", sizeMB)
		}
		return nil
	}

	// 远端节点：向 Worker 请求 /api/v1/fs/cat
	rt, err := c.ResolveWorker(node, "")
	if err != nil {
		return err
	}

	queryParams := url.Values{}
	queryParams.Set("path", path)
	if opts.All {
		queryParams.Set("all", "true")
	}
	if opts.Tail > 0 {
		queryParams.Set("tail", strconv.Itoa(opts.Tail))
	}
	if opts.Head > 0 {
		queryParams.Set("head", strconv.Itoa(opts.Head))
	}
	if opts.LineRange != "" {
		queryParams.Set("range", opts.LineRange)
	}

	catURL := fmt.Sprintf("/api/v1/fs/cat?%s", queryParams.Encode())
	resp, err := c.doRequestWithContext(ctx, rt, http.MethodGet, catURL, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		_, err := io.Copy(w, resp.Body)
		return err
	}

	// 防御性优雅降级：若远端 Worker 是未升级老版本 (返回 404 Not Found)，回退到全量下载并在客户端安全切片
	if resp.StatusCode == http.StatusNotFound {
		var tmpBuf bytes.Buffer
		if dErr := c.DownloadFileWithContext(ctx, node, path, &tmpBuf); dErr == nil {
			tmpFile, tErr := os.CreateTemp("", "cworker-cat-fallback-*.tmp")
			if tErr == nil {
				defer os.Remove(tmpFile.Name())
				defer tmpFile.Close()
				_, _ = tmpFile.Write(tmpBuf.Bytes())
				truncated, sErr := fsengine.SliceFile(tmpFile, opts, w)
				if sErr == nil && truncated {
					sizeMB := float64(tmpBuf.Len()) / (1024 * 1024)
					_, _ = fmt.Fprintf(w, "\n[NOTICE] File size (%.2f MB) exceeds 1 MB limit. Truncated to last 100 lines. Use -n, --head, -L, or --all to override.\n", sizeMB)
				}
				return sErr
			}
		}
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("cat remote file failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// Hash 获取远端节点或本地路径的文件/目录 SHA-256 清单 (统一本地与远端)
func (c *Client) Hash(ctx context.Context, node, path string, recursive bool) ([]protocol.FileInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if node == "" {
		cleanLocal, err := pathutil.NormalizeLocalPath(path)
		if err != nil {
			return nil, err
		}
		return HashLocalPath(cleanLocal, recursive)
	}
	return c.HashRemotePath(ctx, node, path, recursive)
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
	} else {
		tracker.SetTotals(int64(len(files)), totalBytes)
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

			if tracker != nil {
				tracker.StartFile(entry.relPath)
				defer tracker.EndFile(entry.relPath)
			}

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
	} else {
		tracker.SetTotals(int64(len(files)), totalBytes)
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

			if tracker != nil {
				tracker.StartFile(file.relPath)
				defer tracker.EndFile(file.relPath)
			}

			if err := c.DownloadToLocalFile(ctx, node, file.remotePath, localFilePath, tracker); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("download '%s' failed: %w", file.relPath, err)
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
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// RelayCopyDir 将远端节点目录通过本机内存中继复制至另一个远端节点目录
func (c *Client) RelayCopyDir(ctx context.Context, srcNode, srcBaseDir, dstNode, dstBaseDir string, concurrency int, tracker *ProgressTracker) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	entries, err := c.ListDirWithContext(ctx, srcNode, srcBaseDir, true)
	if err != nil {
		return fmt.Errorf("list remote source dir failed: %w", err)
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
	} else {
		tracker.SetTotals(int64(len(files)), totalBytes)
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

			if tracker != nil {
				tracker.StartFile(item.srcPath)
				defer tracker.EndFile(item.srcPath)
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

