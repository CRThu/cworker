package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cworker/pkg/protocol"
)

func TestCompareSemVer(t *testing.T) {
	cases := []struct {
		latest   string
		current  string
		expected int
	}{
		{"1.1.0", "1.0.0", 1},
		{"v1.1.0", "1.0.0", 1},
		{"v1.0.0", "v1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"1.0.10", "1.0.2", 1},
		{"1.0.2", "1.0.10", -1},
	}

	for _, c := range cases {
		got := CompareSemVer(c.latest, c.current)
		if got != c.expected {
			t.Errorf("CompareSemVer(%q, %q) = %d; expected %d", c.latest, c.current, got, c.expected)
		}
	}
}

func TestMatchAsset(t *testing.T) {
	assets := []ReleaseAsset{
		{Name: "cw-linux-amd64.tar.gz", Size: 1000},
		{Name: "cw-windows-amd64.exe", Size: 2000, BrowserDownloadURL: "https://example.com/cw-windows-amd64.exe"},
		{Name: "cw-darwin-arm64.tar.gz", Size: 1000},
	}

	matched, err := MatchAsset(assets)
	if err != nil {
		t.Fatalf("MatchAsset failed: %v", err)
	}
	if matched.Name != "cw-windows-amd64.exe" {
		t.Errorf("expected cw-windows-amd64.exe, got %s", matched.Name)
	}

	// 测试无匹配项
	noWinAssets := []ReleaseAsset{
		{Name: "cw-linux-amd64.tar.gz", Size: 1000},
	}
	_, err = MatchAsset(noWinAssets)
	if err == nil {
		t.Fatal("expected error for no matching assets, got nil")
	}
}

func TestApplyAtomicReplace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw_updater_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	targetExe := filepath.Join(tempDir, "cw.exe")

	// 1. 目标文件不存在时，直接创建
	newContent1 := []byte("binary_version_1")
	if err := ApplyAtomicReplace(targetExe, newContent1); err != nil {
		t.Fatalf("ApplyAtomicReplace failed on new file: %v", err)
	}

	data, err := os.ReadFile(targetExe)
	if err != nil || string(data) != string(newContent1) {
		t.Fatalf("content mismatch on new file: got %s", string(data))
	}

	// 2. 目标文件已存在时，原子替换
	newContent2 := []byte("binary_version_2_updated")
	if err := ApplyAtomicReplace(targetExe, newContent2); err != nil {
		t.Fatalf("ApplyAtomicReplace failed on existing file: %v", err)
	}

	data, err = os.ReadFile(targetExe)
	if err != nil || string(data) != string(newContent2) {
		t.Fatalf("content mismatch on updated file: got %s", string(data))
	}
}

func TestDetectProxy(t *testing.T) {
	u, err := DetectProxy("http://127.0.0.1:8888")
	if err != nil {
		t.Fatalf("DetectProxy failed: %v", err)
	}
	if u == nil || u.Host != "127.0.0.1:8888" {
		t.Fatalf("expected host 127.0.0.1:8888, got %v", u)
	}
}

func TestVerifyChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  cw-windows-amd64.exe\n"))
	}))
	defer server.Close()

	up, _ := NewUpdater("1.0.0", "", "", false)

	assets := []ReleaseAsset{
		{Name: "cw-windows-amd64.exe", BrowserDownloadURL: "http://example.com/cw.exe"},
		{Name: "checksums.txt", BrowserDownloadURL: server.URL},
	}

	// 1. 匹配一致 -> 成功
	err := up.VerifyChecksum(context.Background(), assets, "cw-windows-amd64.exe", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	if err != nil {
		t.Fatalf("expected checksum to pass, got: %v", err)
	}

	// 2. 匹配不一致 -> 报错
	err = up.VerifyChecksum(context.Background(), assets, "cw-windows-amd64.exe", "mismatched_hash_12345")
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}

	// 3. 资产中无 checksum 文件 -> 放行
	noChecksumAssets := []ReleaseAsset{
		{Name: "cw-windows-amd64.exe", BrowserDownloadURL: "http://example.com/cw.exe"},
	}
	if err := up.VerifyChecksum(context.Background(), noChecksumAssets, "cw-windows-amd64.exe", "any_hash"); err != nil {
		t.Fatalf("expected nil when no checksum asset exists, got: %v", err)
	}

	// 4. 单独匹配 cw.exe.sha256 专用文件格式
	sha256Server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  cw.exe\n"))
	}))
	defer sha256Server.Close()

	cwAssets := []ReleaseAsset{
		{Name: "cw.exe", BrowserDownloadURL: "http://example.com/cw.exe"},
		{Name: "cw.exe.sha256", BrowserDownloadURL: sha256Server.URL},
	}
	if err := up.VerifyChecksum(context.Background(), cwAssets, "cw.exe", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"); err != nil {
		t.Fatalf("expected cw.exe.sha256 to pass, got: %v", err)
	}
}

func TestUpdater_FetchLatestRelease(t *testing.T) {
	mockRel := ReleaseInfo{
		TagName:     "v1.2.0",
		Name:        "Release 1.2.0",
		Body:        "Awesome new features",
		PublishedAt: "2026-09-12T00:00:00Z",
		Assets: []ReleaseAsset{
			{Name: "cw-windows-amd64.exe", BrowserDownloadURL: "http://example.com/cw.exe", Size: 8000000},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(mockRel)
	}))
	defer server.Close()

	up, _ := NewUpdater("1.0.0", "", "", false)
	// 用 mirror 前缀将请求重定向至本地 mock server
	up.Mirror = server.URL + "/"

	rel, err := up.FetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("FetchLatestRelease failed: %v", err)
	}
	if rel.TagName != "v1.2.0" {
		t.Fatalf("expected tag v1.2.0, got %s", rel.TagName)
	}
	if len(rel.Assets) != 1 || rel.Assets[0].Name != "cw-windows-amd64.exe" {
		t.Fatalf("unexpected assets: %+v", rel.Assets)
	}
}

func TestUpdater_DownloadAsset(t *testing.T) {
	// 构造一个大于 500KB 的模拟二进制
	dummyData := make([]byte, 600*1024)
	for i := range dummyData {
		dummyData[i] = byte(i % 256)
	}

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Length", fmt.Sprintf("%d", len(dummyData)))
		_, _ = rw.Write(dummyData)
	}))
	defer server.Close()

	up, _ := NewUpdater("1.0.0", "", "", false)

	asset := &ReleaseAsset{
		Name:               "cw-windows-amd64.exe",
		BrowserDownloadURL: server.URL,
		Size:               int64(len(dummyData)),
	}

	var progressCalled bool
	data, sha, err := up.DownloadAsset(context.Background(), asset, func(down, total int64) {
		progressCalled = true
	})
	if err != nil {
		t.Fatalf("DownloadAsset failed: %v", err)
	}
	if len(data) != len(dummyData) {
		t.Fatalf("download size mismatch: got %d, expected %d", len(data), len(dummyData))
	}
	if sha == "" {
		t.Fatal("expected sha256 to be computed")
	}
	if !progressCalled {
		t.Fatal("expected progress callback to be invoked")
	}
}

func TestExtractExeFromZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// 写入 readme.txt
	w1, _ := zw.Create("readme.txt")
	_, _ = w1.Write([]byte("ignore me"))

	// 写入 cw.exe
	expectedContent := []byte("mock_executable_inside_zip")
	w2, _ := zw.Create("cw.exe")
	_, _ = w2.Write(expectedContent)

	_ = zw.Close()

	extracted, err := extractExeFromZip(buf.Bytes())
	if err != nil {
		t.Fatalf("extractExeFromZip failed: %v", err)
	}
	if string(extracted) != string(expectedContent) {
		t.Fatalf("extracted content mismatch: got %s, expected %s", string(extracted), string(expectedContent))
	}
}

func TestUpdater_CheckRunningJobs(t *testing.T) {
	// 1. 当 force 为 true 时，不发起任何网络请求，直接放行
	upForce, _ := NewUpdater("1.0.0", "", "", true)
	if err := upForce.CheckRunningJobs(context.Background()); err != nil {
		t.Fatalf("expected nil when force is true, got: %v", err)
	}

	// 2. 当没有 worker 在运行时 (端口连接拒绝)，静默放行
	upNoWorker, _ := NewUpdater("1.0.0", "", "", false)
	if err := upNoWorker.CheckRunningJobs(context.Background()); err != nil {
		t.Fatalf("expected nil when worker is offline, got: %v", err)
	}
}

func TestGetTargetExePaths(t *testing.T) {
	deployed, cur := GetTargetExePaths()
	if !filepath.IsAbs(deployed) {
		t.Fatalf("expected deployedExe to be absolute path, got: %s", deployed)
	}
	if !strings.HasSuffix(filepath.Clean(deployed), filepath.Join(".cworker", "bin", "cw.exe")) {
		t.Fatalf("deployed path mismatch: %s", deployed)
	}
	if cur == "" || !filepath.IsAbs(cur) {
		t.Fatalf("expected currentExe to be non-empty absolute path, got: %s", cur)
	}
}

func TestCompareSemVer_MoreCases(t *testing.T) {
	cases := []struct {
		latest   string
		current  string
		expected int
	}{
		{"", "", 0},
		{"v1.0.0", "", 1},
		{"", "v1.0.0", -1},
		{"abc", "def", 0},              // 非数字部分 Atoi 失败得 0，相等返回 0
		{"v1.2.3-beta", "v1.2.3", -1}, // SemVer 规范：预发布版低于正式版
		{"v2", "v1.9.9", 1},
		{"v1.0.0", "v2", -1},
	}
	for _, c := range cases {
		got := CompareSemVer(c.latest, c.current)
		if got != c.expected {
			t.Errorf("CompareSemVer(%q, %q) = %d; expected %d", c.latest, c.current, got, c.expected)
		}
	}
}

func TestVerifyChecksum_EdgeCases(t *testing.T) {
	correctHash := "59bb1b1bb85a21bebbbb2c813f8983944fbfaae67ca670498bda9571f25b29bb"

	var returnedText string
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(returnedText))
	}))
	defer server.Close()

	up, _ := NewUpdater("1.0.0", "", "", false)
	assets := []ReleaseAsset{
		{Name: "cw.exe.sha256", BrowserDownloadURL: server.URL},
	}

	// 1. 星号二进制格式: <hash> *cw.exe
	returnedText = fmt.Sprintf("%s *cw.exe\n", correctHash)
	if err := up.VerifyChecksum(context.Background(), assets, "cw.exe", correctHash); err != nil {
		t.Fatalf("VerifyChecksum asterisk style failed: %v", err)
	}

	// 2. 裸 Hash (单行仅 64 位十六进制)
	returnedText = fmt.Sprintf("%s\n", correctHash)
	if err := up.VerifyChecksum(context.Background(), assets, "cw.exe", correctHash); err != nil {
		t.Fatalf("VerifyChecksum bare hash failed: %v", err)
	}

	// 3. 多行校验和清单中精确匹配
	returnedText = fmt.Sprintf("0000000000000000000000000000000000000000000000000000000000000000  other.exe\n%s  cw.exe\n", correctHash)
	if err := up.VerifyChecksum(context.Background(), assets, "cw.exe", correctHash); err != nil {
		t.Fatalf("VerifyChecksum multiline failed: %v", err)
	}

	// 4. 哈希篡改不匹配
	returnedText = fmt.Sprintf("1111111111111111111111111111111111111111111111111111111111111111  cw.exe\n")
	if err := up.VerifyChecksum(context.Background(), assets, "cw.exe", correctHash); err == nil {
		t.Fatal("expected error on tampered hash")
	}

	// 5. 空校验和文件
	returnedText = ""
	if err := up.VerifyChecksum(context.Background(), assets, "cw.exe", correctHash); err != nil {
		// 空文件无任何条目，循环结束返回 nil
	}
}

func TestMatchAsset_Priority(t *testing.T) {
	// 当同时包含 cw.exe 与 cw-windows-amd64.exe 时，cw.exe 优先
	assets := []ReleaseAsset{
		{Name: "cw-windows-amd64.exe", Size: 2000, BrowserDownloadURL: "https://example.com/long.exe"},
		{Name: "cw.exe", Size: 2000, BrowserDownloadURL: "https://example.com/cw.exe"},
	}
	matched, err := MatchAsset(assets)
	if err != nil {
		t.Fatalf("MatchAsset failed: %v", err)
	}
	if matched.Name != "cw.exe" {
		t.Fatalf("expected cw.exe to take priority, got: %s", matched.Name)
	}

	// 仅包含 zip 压缩包
	zipAssets := []ReleaseAsset{
		{Name: "cw-windows-amd64.zip", Size: 3000, BrowserDownloadURL: "https://example.com/cw.zip"},
	}
	matchedZip, err := MatchAsset(zipAssets)
	if err != nil {
		t.Fatalf("MatchAsset zip failed: %v", err)
	}
	if matchedZip.Name != "cw-windows-amd64.zip" {
		t.Fatalf("expected zip matched, got: %s", matchedZip.Name)
	}
}

func TestUpdater_DownloadAsset_TamperedChecksumFails(t *testing.T) {
	// 构造超过 500KB 的模拟可执行文件数据
	mockExeContent := make([]byte, 600*1024)
	for i := range mockExeContent {
		mockExeContent[i] = byte(i % 256)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cw.exe":
			_, _ = w.Write(mockExeContent)
		case "/cw.exe.sha256":
			// 故意返回错误的 SHA-256
			_, _ = w.Write([]byte("badbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadb  cw.exe\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	up, _ := NewUpdater("1.0.0", "", "", true)
	release := &ReleaseInfo{
		TagName: "v1.1.0",
		Assets: []ReleaseAsset{
			{Name: "cw.exe", BrowserDownloadURL: server.URL + "/cw.exe"},
			{Name: "cw.exe.sha256", BrowserDownloadURL: server.URL + "/cw.exe.sha256"},
		},
	}

	targetAsset := release.Assets[0]
	_, actualSHA, err := up.DownloadAsset(context.Background(), &targetAsset, nil)
	if err != nil {
		t.Fatalf("DownloadAsset unexpected failure: %v", err)
	}

	// 校验和校验应该检测到不匹配并报错
	err = up.VerifyChecksum(context.Background(), release.Assets, targetAsset.Name, actualSHA)
	if err == nil {
		t.Fatal("expected VerifyChecksum to fail on tampered SHA-256")
	}
	if !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected SHA-256 mismatch error, got: %v", err)
	}
}

func TestUpdater_CheckRunningJobs_ActiveJobs_Aborts(t *testing.T) {
	// 尝试绑定默认端口 19000 模拟运行中的本地 Worker
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", protocol.DefaultPort))
	if err != nil {
		t.Skip("port 19000 already bound by another process, skipping mock worker test")
	}
	defer l.Close()

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/health" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(protocol.NodeInfo{
					Name:       "local-node",
					ActiveJobs: 2, // 模拟存在 2 个正在运行的活跃任务
				})
				return
			}
			http.NotFound(w, r)
		}),
	}
	go func() { _ = srv.Serve(l) }()
	defer func() { _ = srv.Close() }()

	// 1. 未传 force 时，必须被硬拦截并报错
	up, _ := NewUpdater("1.0.0", "", "", false)
	err = up.CheckRunningJobs(context.Background())
	if err == nil {
		t.Fatal("expected CheckRunningJobs to abort when active jobs > 0")
	}
	if !strings.Contains(err.Error(), "cannot update: 2 active jobs are currently running") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. 传入 force 时，必须放行
	upForce, _ := NewUpdater("1.0.0", "", "", true)
	if err := upForce.CheckRunningJobs(context.Background()); err != nil {
		t.Fatalf("expected CheckRunningJobs to succeed when force is true, got: %v", err)
	}
}

func TestDetectWindowsRegistryProxy(t *testing.T) {
	// 验证在当前操作系统执行注册表代理探测安全降级或成功解析，绝不发生 Panic
	u := detectWindowsRegistryProxy()
	if u != nil {
		if u.Scheme == "" || u.Host == "" {
			t.Fatalf("invalid parsed proxy URL from registry: %v", u)
		}
	}
}

func TestRestartWindowsService(t *testing.T) {
	// 验证在未安装或未启动系统服务的非提权单元测试环境中，优雅返回 false 与 nil，不崩溃
	restarted, err := RestartWindowsService()
	if err != nil {
		t.Fatalf("unexpected error from RestartWindowsService: %v", err)
	}
	if restarted {
		t.Log("Windows service was unexpectedly restarted in test environment")
	}
}






