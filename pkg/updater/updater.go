package updater

import (
	"archive/zip"
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
	"runtime"
	"strconv"
	"strings"
	"time"

	"cworker/pkg/netutil"
	"cworker/pkg/protocol"
	"github.com/kardianos/service"
	"golang.org/x/sys/windows/registry"
)

const (
	// DefaultRepo 默认 GitHub 官方发布仓库 (SSOT)
	DefaultRepo = "crthu/cworker"
)

// ReleaseAsset 描述 GitHub Release 资产条目
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// ReleaseInfo 描述 GitHub Release 权威元数据
type ReleaseInfo struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	PublishedAt string         `json:"published_at"`
	Assets      []ReleaseAsset `json:"assets"`
}

// Updater 封装 GitHub Releases 自升级器
type Updater struct {
	Repo       string
	CurrentVer string
	Proxy      string
	NoProxy    bool
	Mirror     string
	Force      bool
	httpClient *http.Client
}

// NewUpdater 实例化自升级器
func NewUpdater(currentVer string, proxy string, noProxy bool, mirror string, force bool) (*Updater, error) {
	u := &Updater{
		Repo:       DefaultRepo,
		CurrentVer: currentVer,
		Proxy:      proxy,
		NoProxy:    noProxy,
		Mirror:     mirror,
		Force:      force,
	}

	proxyFunc, err := netutil.ResolveProxyFunc(netutil.ProxyConfig{
		Proxy:   proxy,
		NoProxy: noProxy,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid proxy configuration: %w", err)
	}

	tr := &http.Transport{
		Proxy: proxyFunc,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	u.httpClient = &http.Client{
		Transport: tr,
		Timeout:   10 * time.Minute, // 支持大包下载超时
	}
	return u, nil
}

// DetectProxy 级联探测代理（兼容旧接口与测试，统一基于 netutil）
func DetectProxy(cliProxy string) (*url.URL, error) {
	if cliProxy != "" {
		return netutil.ParseProxyURL(cliProxy)
	}
	for _, envKey := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"} {
		if val := os.Getenv(envKey); val != "" {
			if u, err := url.Parse(val); err == nil {
				return u, nil
			}
		}
	}

	if u := detectWindowsRegistryProxy(); u != nil {
		return u, nil
	}
	return nil, nil
}

func detectWindowsRegistryProxy() *url.URL {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()

	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enable != 1 {
		return nil
	}

	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil || server == "" {
		return nil
	}

	target := server
	if strings.Contains(target, ";") {
		parts := strings.Split(target, ";")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(strings.ToLower(p), "https=") {
				target = strings.TrimPrefix(p, "https=")
				break
			} else if strings.HasPrefix(strings.ToLower(p), "http=") {
				target = strings.TrimPrefix(p, "http=")
			}
		}
	}

	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, "socks5://") {
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil
	}
	return u
}

// ExtractTagFromLocation 从 HTTP 重定向 Location 头中精准提取版本 Tag
// 支持各类形如 /releases/download/<tag>/cw.exe 或 /releases/tag/<tag> 的绝对/相对路径及镜像反代 URL
func ExtractTagFromLocation(loc string) (string, error) {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return "", fmt.Errorf("empty redirect location header")
	}

	// 模式 1: .../releases/download/<tag>/...
	const dlPattern = "/releases/download/"
	if idx := strings.Index(loc, dlPattern); idx != -1 {
		remainder := loc[idx+len(dlPattern):]
		parts := strings.Split(remainder, "/")
		if len(parts) > 0 {
			tag := strings.TrimSpace(parts[0])
			tag = strings.Split(tag, "?")[0]
			tag = strings.Split(tag, "#")[0]
			if tag != "" {
				return tag, nil
			}
		}
	}

	// 模式 2: .../releases/tag/<tag>
	const tagPattern = "/releases/tag/"
	if idx := strings.Index(loc, tagPattern); idx != -1 {
		remainder := loc[idx+len(tagPattern):]
		parts := strings.Split(remainder, "/")
		if len(parts) > 0 {
			tag := strings.TrimSpace(parts[0])
			tag = strings.Split(tag, "?")[0]
			tag = strings.Split(tag, "#")[0]
			if tag != "" {
				return tag, nil
			}
		}
	}

	return "", fmt.Errorf("could not extract version tag from location: %s", loc)
}

// FetchLatestRelease 统一通过 releases/latest 探针获取最新版本与资产（免 API 限制、镜像原生兼容）
func (u *Updater) FetchLatestRelease(ctx context.Context) (*ReleaseInfo, error) {
	// 构造 latest 目标探针 URL (以 cw.exe 为锚点)
	probeURL := fmt.Sprintf("https://github.com/%s/releases/latest/download/cw.exe", u.Repo)
	if u.Mirror != "" {
		mirror := strings.TrimRight(u.Mirror, "/") + "/"
		probeURL = mirror + probeURL
	}

	// 创建不跟随重定向的探针专用 HTTP Client
	probeTransport := u.httpClient.Transport
	probeClient := &http.Client{
		Transport: probeTransport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 优先以 HEAD 请求探测（开销极低），若服务器不支持则平滑降级为 GET
	rel, err := u.probeReleaseInfo(ctx, probeClient, http.MethodHead, probeURL)
	if err != nil {
		rel, err = u.probeReleaseInfo(ctx, probeClient, http.MethodGet, probeURL)
	}

	// 若 302 探针成功提取到版本与资产
	if err == nil && rel != nil {
		// 若为直连且无 mirror 且无 Release Notes，尝试轻量获取 Release Notes（失败则静默忽略，绝不阻塞升级主链路）
		if u.Mirror == "" && rel.Body == "" {
			u.tryEnrichReleaseNotes(ctx, rel)
		}
		return rel, nil
	}

	// 若探针失败，尝试直接从 API 获取作为兜底（支持针对本地纯 Mock Server 测试）
	apiRel, apiErr := u.fetchFromAPI(ctx)
	if apiErr == nil && apiRel != nil {
		return apiRel, nil
	}

	if err != nil {
		return nil, fmt.Errorf("probe latest release failed: %w", err)
	}
	return nil, fmt.Errorf("failed to determine latest release")
}

func (u *Updater) probeReleaseInfo(ctx context.Context, client *http.Client, method, probeURL string) (*ReleaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, method, probeURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cworker-updater/"+u.CurrentVer)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 截获重定向响应并提取 Tag
	if resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusSeeOther ||
		resp.StatusCode == http.StatusTemporaryRedirect ||
		resp.StatusCode == http.StatusPermanentRedirect {
		loc := resp.Header.Get("Location")
		tag, err := ExtractTagFromLocation(loc)
		if err != nil {
			return nil, err
		}
		return u.buildReleaseInfoFromTag(tag), nil
	}

	// 若直接返回 200 OK 且包含完整 JSON，兼容直接返回 ReleaseInfo 的测试服务
	if resp.StatusCode == http.StatusOK {
		var rel ReleaseInfo
		if err := json.NewDecoder(resp.Body).Decode(&rel); err == nil && rel.TagName != "" {
			if len(rel.Assets) == 0 {
				rel.Assets = u.buildReleaseInfoFromTag(rel.TagName).Assets
			}
			return &rel, nil
		}
	}

	return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
}

func (u *Updater) buildReleaseInfoFromTag(tag string) *ReleaseInfo {
	downloadBase := fmt.Sprintf("https://github.com/%s/releases/download/%s", u.Repo, tag)
	return &ReleaseInfo{
		TagName: tag,
		Name:    tag,
		Assets: []ReleaseAsset{
			{
				Name:               "cw.exe",
				BrowserDownloadURL: fmt.Sprintf("%s/cw.exe", downloadBase),
			},
			{
				Name:               "cw.exe.sha256",
				BrowserDownloadURL: fmt.Sprintf("%s/cw.exe.sha256", downloadBase),
			},
		},
	}
}

func (u *Updater) tryEnrichReleaseNotes(ctx context.Context, rel *ReleaseInfo) {
	enrichCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	apiRel, err := u.fetchFromAPI(enrichCtx)
	if err == nil && apiRel != nil {
		if apiRel.Body != "" {
			rel.Body = apiRel.Body
		}
		for _, a := range apiRel.Assets {
			for i := range rel.Assets {
				if strings.EqualFold(rel.Assets[i].Name, a.Name) && a.Size > 0 {
					rel.Assets[i].Size = a.Size
				}
			}
		}
	}
}

func (u *Updater) fetchFromAPI(ctx context.Context) (*ReleaseInfo, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", u.Repo)
	if u.Mirror != "" && !strings.HasPrefix(apiURL, strings.TrimRight(u.Mirror, "/")) {
		mirror := strings.TrimRight(u.Mirror, "/") + "/"
		apiURL = mirror + apiURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "cworker-updater/"+u.CurrentVer)

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release json failed: %w", err)
	}
	return &rel, nil
}

// CompareSemVer 比对版本号：若 latest > current 返回 1，latest < current 返回 -1，相等返回 0
func CompareSemVer(latest, current string) int {
	cleanL := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(latest), "v"), "V")
	cleanC := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(current), "v"), "V")

	partsL := strings.Split(cleanL, ".")
	partsC := strings.Split(cleanC, ".")

	maxLen := len(partsL)
	if len(partsC) > maxLen {
		maxLen = len(partsC)
	}

	for i := 0; i < maxLen; i++ {
		var nL, nC int
		if i < len(partsL) {
			nL, _ = strconv.Atoi(partsL[i])
		}
		if i < len(partsC) {
			nC, _ = strconv.Atoi(partsC[i])
		}
		if nL > nC {
			return 1
		}
		if nL < nC {
			return -1
		}
	}
	return 0
}

// MatchAsset 根据系统架构在 Assets 列表中精准匹配二进制资产
func MatchAsset(assets []ReleaseAsset) (*ReleaseAsset, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	patterns := []string{
		"cw.exe",
		fmt.Sprintf("cw-%s-%s.exe", goos, goarch),
		fmt.Sprintf("cw_%s_%s.exe", goos, goarch),
		fmt.Sprintf("cworker-%s-%s.exe", goos, goarch),
		fmt.Sprintf("cworker_%s_%s.exe", goos, goarch),
		fmt.Sprintf("cw-%s-%s.zip", goos, goarch),
		fmt.Sprintf("cw_%s_%s.zip", goos, goarch),
	}

	for _, p := range patterns {
		for _, a := range assets {
			if strings.EqualFold(a.Name, p) {
				return &a, nil
			}
		}
	}
	return nil, fmt.Errorf("no matching binary asset found for %s/%s in release", goos, goarch)
}

// CheckRunningJobs 检查本机受控服务是否有 RUNNING 活跃任务；若有且未传递 force 则报错拦截
func (u *Updater) CheckRunningJobs(ctx context.Context) error {
	if u.Force {
		return nil
	}

	// 尝试向本机 127.0.0.1:19000 发送探活请求
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", protocol.DefaultPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return nil
	}

	// 读取本地 token (若存在)
	home, _ := os.UserHomeDir()
	tokenPath := filepath.Join(home, protocol.DefaultDataDirName, protocol.TokenFileName)
	if tok, err := os.ReadFile(tokenPath); err == nil {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	}

	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		// Worker 服务可能未运行，放行更新
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var info protocol.NodeInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err == nil {
			if info.ActiveJobs > 0 {
				return fmt.Errorf("cannot update: %d active jobs are currently running on this node. Aborting update to prevent job termination. Use 'cw update --force' to terminate running jobs and update anyway", info.ActiveJobs)
			}
		}
	}
	return nil
}

// DownloadAsset 下载目标资产并计算单遍 SHA-256，返回下载文件的实际数据
func (u *Updater) DownloadAsset(ctx context.Context, asset *ReleaseAsset, onProgress func(downloaded, total int64)) ([]byte, string, error) {
	downloadURL := asset.BrowserDownloadURL
	if u.Mirror != "" && !strings.HasPrefix(downloadURL, strings.TrimRight(u.Mirror, "/")) {
		mirror := strings.TrimRight(u.Mirror, "/") + "/"
		downloadURL = mirror + downloadURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "cworker-updater/"+u.CurrentVer)

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download asset failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength
	var buf bytes.Buffer
	hasher := sha256.New()
	multiWriter := io.MultiWriter(&buf, hasher)

	bufReader := make([]byte, 32*1024)
	var downloaded int64

	for {
		n, readErr := resp.Body.Read(bufReader)
		if n > 0 {
			if _, wErr := multiWriter.Write(bufReader[:n]); wErr != nil {
				return nil, "", wErr
			}
			downloaded += int64(n)
			if onProgress != nil {
				onProgress(downloaded, total)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, "", readErr
		}
	}

	rawBytes := buf.Bytes()
	sum := hex.EncodeToString(hasher.Sum(nil))

	// 若为 zip 包，解压提取单一可执行文件
	if strings.HasSuffix(strings.ToLower(asset.Name), ".zip") {
		exeBytes, err := extractExeFromZip(rawBytes)
		if err != nil {
			return nil, "", fmt.Errorf("extract zip asset failed: %w", err)
		}
		return exeBytes, sum, nil
	}

	if len(rawBytes) < 500*1024 {
		return nil, "", fmt.Errorf("downloaded binary is unusually small (%d bytes), possibly corrupted or rate limited", len(rawBytes))
	}

	return rawBytes, sum, nil
}

// VerifyChecksum 从 Release 资产列表中自动寻找 checksums.txt 或 *.sha256 文件核验 SHA-256
func (u *Updater) VerifyChecksum(ctx context.Context, assets []ReleaseAsset, assetName string, actualSHA string) error {
	var checksumAsset *ReleaseAsset
	for _, a := range assets {
		lower := strings.ToLower(a.Name)
		if lower == "checksums.txt" || lower == "sha256sums" || lower == "sha256sums.txt" || lower == strings.ToLower(assetName)+".sha256" {
			checksumAsset = &a
			break
		}
	}

	if checksumAsset == nil {
		return nil // 无校验文件，静默放行
	}

	content, err := u.downloadText(ctx, checksumAsset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("download checksum file '%s' failed: %w", checksumAsset.Name, err)
	}

	lines := strings.Split(content, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		// 支持标准格式: <hash>  <filename> 或 <hash> *<filename>
		parts := strings.Fields(l)
		if len(parts) >= 2 {
			hash := strings.ToLower(parts[0])
			filename := strings.TrimPrefix(parts[1], "*")
			if strings.EqualFold(filepath.Base(filename), assetName) {
				if hash != strings.ToLower(actualSHA) {
					return fmt.Errorf("SHA-256 mismatch for %s: expected %s, calculated %s", assetName, hash, actualSHA)
				}
				return nil
			}
		} else if len(parts) == 1 && strings.EqualFold(checksumAsset.Name, assetName+".sha256") {
			if strings.ToLower(parts[0]) != strings.ToLower(actualSHA) {
				return fmt.Errorf("SHA-256 mismatch for %s: expected %s, calculated %s", assetName, parts[0], actualSHA)
			}
			return nil
		}
	}

	return nil
}

func (u *Updater) downloadText(ctx context.Context, downloadURL string) (string, error) {
	if u.Mirror != "" {
		mirror := strings.TrimRight(u.Mirror, "/") + "/"
		downloadURL = mirror + downloadURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cworker-updater/"+u.CurrentVer)
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func extractExeFromZip(zipData []byte) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if strings.EqualFold(filepath.Base(f.Name), "cw.exe") || strings.HasSuffix(strings.ToLower(f.Name), ".exe") {
			// 限制单个可执行文件解压上限为 100MB，物理防范恶意 Zip Bomb
			if f.UncompressedSize64 > 100*1024*1024 {
				return nil, fmt.Errorf("executable inside zip exceeds maximum allowed size (100MB)")
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 100*1024*1024))
		}
	}
	return nil, fmt.Errorf("no executable found inside zip archive")
}

// GetTargetExePaths 获取需要升级的目标路径（优先系统服务部署路径与当前运行文件）
func GetTargetExePaths() (deployedExe string, currentExe string) {
	home, _ := os.UserHomeDir()
	deployedExe = filepath.Join(home, protocol.DefaultDataDirName, "bin", "cw.exe")

	cur, err := os.Executable()
	if err == nil {
		currentExe = cur
	}
	return deployedExe, currentExe
}

// ApplyAtomicReplace 在 Windows 平台执行无锁原子替换 (Rename-Replace 模式)
func ApplyAtomicReplace(targetExe string, newBytes []byte) error {
	dir := filepath.Dir(targetExe)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("ensure dir '%s' failed: %w", dir, err)
	}

	tempNew := targetExe + ".download"
	if err := os.WriteFile(tempNew, newBytes, 0755); err != nil {
		return fmt.Errorf("write downloaded binary failed: %w", err)
	}

	// 若目标文件尚不存在，直接重命名就位
	if _, err := os.Stat(targetExe); os.IsNotExist(err) {
		return os.Rename(tempNew, targetExe)
	}

	oldExe := targetExe + ".old"
	_ = os.Remove(oldExe) // 清理可能存在的历史残留

	// 1. 将当前运行中的 exe 重命名为 .old (Windows 允许 Rename 运行中的可执行文件)
	if err := os.Rename(targetExe, oldExe); err != nil {
		_ = os.Remove(tempNew)
		return fmt.Errorf("rename existing binary to .old failed (Windows lock): %w", err)
	}

	// 2. 将新下载的文件移至目标位置
	if err := os.Rename(tempNew, targetExe); err != nil {
		// 尝试回滚
		_ = os.Rename(oldExe, targetExe)
		return fmt.Errorf("place new binary to '%s' failed: %w", targetExe, err)
	}

	// 3. 尝试清理 .old 文件 (若被当前进程占用则静默忽略，下次启动顺手删除)
	_ = os.Remove(oldExe)
	return nil
}

// RestartWindowsService 重启 cworker 系统服务 (若已安装并运行)
func RestartWindowsService() (bool, error) {
	home, _ := os.UserHomeDir()
	deployedExe := filepath.Join(home, protocol.DefaultDataDirName, "bin", "cw.exe")

	svcConfig := &service.Config{
		Name:        "cworker",
		DisplayName: "cworker - Carrot Worker Distributed Agent",
		Executable:  deployedExe,
	}

	s, err := service.New(nil, svcConfig)
	if err != nil {
		return false, nil
	}

	st, err := s.Status()
	if err != nil || st != service.StatusRunning {
		return false, nil
	}

	// 重启服务
	if err := s.Restart(); err != nil {
		return true, fmt.Errorf("restart windows service 'cworker' failed: %w", err)
	}
	return true, nil
}
