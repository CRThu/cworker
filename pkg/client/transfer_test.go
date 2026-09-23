package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cworker/pkg/protocol"
)

func TestClient_Transfer_Validation(t *testing.T) {
	cli := NewClient()
	ctx := context.Background()

	// 1. 空路径校验
	if err := cli.Transfer(ctx, TransferOptions{SrcPath: "", DstPath: "D:/dst"}, nil); err == nil {
		t.Fatal("expected error for empty SrcPath")
	}
	if err := cli.Transfer(ctx, TransferOptions{SrcPath: "D:/src", DstPath: ""}, nil); err == nil {
		t.Fatal("expected error for empty DstPath")
	}

	// 2. 本地不存在的源文件
	tempDir := t.TempDir()
	nonExistent := filepath.Join(tempDir, "non_existent.txt")
	err := cli.Transfer(ctx, TransferOptions{SrcPath: nonExistent, DstPath: filepath.Join(tempDir, "dst.txt")}, nil)
	if err == nil {
		t.Fatal("expected error for non existent local src")
	}
	if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
		t.Fatalf("expected os.ErrNotExist in chain, got: %v", err)
	}
}

func TestClient_Transfer_LocalToLocal(t *testing.T) {
	tempDir := t.TempDir()
	cli := NewClient()
	ctx := context.Background()

	// 1. 本地单文件传输与进度验证
	srcFile := filepath.Join(tempDir, "file1.txt")
	dstFile := filepath.Join(tempDir, "dst_file1.txt")
	testData := []byte("hello-local-to-local-single-file-transfer")
	if err := os.WriteFile(srcFile, testData, 0644); err != nil {
		t.Fatal(err)
	}

	tracker := NewProgressTracker(0, 0)
	tracker.SetTTY(false)
	var lastSnap ProgressSnapshot
	tracker.SetUpdateCallback(func(snap ProgressSnapshot) {
		lastSnap = snap
	})

	err := cli.Transfer(ctx, TransferOptions{
		SrcPath: srcFile,
		DstPath: dstFile,
	}, tracker)
	if err != nil {
		t.Fatalf("local single file transfer failed: %v", err)
	}

	copied, err := os.ReadFile(dstFile)
	if err != nil || string(copied) != string(testData) {
		t.Fatalf("copied content mismatch: %v, got %s", err, string(copied))
	}
	if lastSnap.Percent != 100 || lastSnap.CompletedFiles != 1 {
		t.Errorf("expected 100%% with 1 completed file, got %+v", lastSnap)
	}

	// 2. 本地目录未传 -r 被严格拦截为 ErrDirectoryWithoutRecursive
	srcDir := filepath.Join(tempDir, "test_src_dir")
	_ = os.MkdirAll(filepath.Join(srcDir, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(srcDir, "sub", "inner.txt"), []byte("inner data"), 0644)
	dstDir := filepath.Join(tempDir, "test_dst_dir")

	err = cli.Transfer(ctx, TransferOptions{
		SrcPath:   srcDir,
		DstPath:   dstDir,
		Recursive: false,
	}, nil)
	if err == nil {
		t.Fatal("expected error for dir transfer without recursive")
	}
	var dirErr *ErrDirectoryWithoutRecursive
	if !errors.As(err, &dirErr) {
		t.Fatalf("expected *ErrDirectoryWithoutRecursive, got %T: %v", err, err)
	}
	if !strings.Contains(dirErr.Error(), "omitting directory") || !strings.Contains(dirErr.Error(), "use -r") {
		t.Fatalf("unexpected error message: %v", dirErr.Error())
	}

	// 3. 本地目录递归拷贝成功
	err = cli.Transfer(ctx, TransferOptions{
		SrcPath:     srcDir,
		DstPath:     dstDir,
		Recursive:   true,
		Concurrency: 4,
	}, nil)
	if err != nil {
		t.Fatalf("local recursive dir transfer failed: %v", err)
	}
	innerCopied, err := os.ReadFile(filepath.Join(dstDir, "sub", "inner.txt"))
	if err != nil || string(innerCopied) != "inner data" {
		t.Fatalf("recursive dir content mismatch: %v, got %s", err, string(innerCopied))
	}

	// 4. 目标为已存在目录时，自动将单文件或子目录嵌套放入其内部 (对齐 Unix cp 规范)
	existingDir := filepath.Join(tempDir, "existing_dir")
	_ = os.MkdirAll(existingDir, 0755)

	// 嵌套单文件
	err = cli.Transfer(ctx, TransferOptions{
		SrcPath: srcFile,
		DstPath: existingDir,
	}, nil)
	if err != nil {
		t.Fatalf("nesting single file into existing dir failed: %v", err)
	}
	nestedFile := filepath.Join(existingDir, "file1.txt")
	if data, err := os.ReadFile(nestedFile); err != nil || string(data) != string(testData) {
		t.Fatalf("nested file mismatch: %v, got %s", err, string(data))
	}

	// 嵌套文件夹
	err = cli.Transfer(ctx, TransferOptions{
		SrcPath:   srcDir,
		DstPath:   existingDir,
		Recursive: true,
	}, nil)
	if err != nil {
		t.Fatalf("nesting dir into existing dir failed: %v", err)
	}
	nestedDirFile := filepath.Join(existingDir, filepath.Base(srcDir), "sub", "inner.txt")
	if data, err := os.ReadFile(nestedDirFile); err != nil || string(data) != "inner data" {
		t.Fatalf("nested dir file mismatch: %v, got %s", err, string(data))
	}
}

// setupTransferMockWorker 构建用于测试多机传输协议的轻量 Mock Worker
func setupTransferMockWorker(t *testing.T) (*httptest.Server, *sync.Map) {
	storage := &sync.Map{} // path -> []byte or "DIR"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")

		switch r.URL.Path {
		case "/api/v1/fs/ls":
			// 探测目录或列出目录文件
			val, ok := storage.Load(path)
			if !ok {
				http.Error(w, "path not found", http.StatusNotFound)
				return
			}
			if val != "DIR" {
				http.Error(w, "not a directory", http.StatusBadRequest)
				return
			}
			isRec := r.URL.Query().Get("recursive") == "true"
			var entries []protocol.FileInfo
			storage.Range(func(k, v any) bool {
				p := k.(string)
				if strings.HasPrefix(p, path+"/") && p != path {
					rel := strings.TrimPrefix(p, path+"/")
					if isRec || !strings.Contains(rel, "/") {
						isDir := v == "DIR"
						sz := int64(0)
						if b, ok := v.([]byte); ok {
							sz = int64(len(b))
						}
						entries = append(entries, protocol.FileInfo{
							Name:  filepath.Base(p),
							Path:  rel,
							IsDir: isDir,
							Size:  sz,
						})
					}
				}
				return true
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(entries)

		case "/api/v1/fs/upload":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			storage.Store(path, body)
			h := sha256.Sum256(body)
			hashStr := hex.EncodeToString(h[:])
			w.Header().Set("X-File-SHA256", hashStr)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "size": len(body), "sha256": hashStr})

		case "/api/v1/fs/download":
			val, ok := storage.Load(path)
			if !ok {
				http.Error(w, "file not found", http.StatusNotFound)
				return
			}
			data, isBytes := val.([]byte)
			if !isBytes {
				http.Error(w, "cannot download directory directly", http.StatusBadRequest)
				return
			}
			h := sha256.Sum256(data)
			hashStr := hex.EncodeToString(h[:])
			w.Header().Set("X-File-SHA256", hashStr)
			w.Header().Set("X-File-Size", fmt.Sprintf("%d", len(data)))
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(data)

		case "/api/v1/fs/md":
			storage.Store(path, "DIR")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})

		case "/api/v1/fs/hash":
			// 用于目录核验
			var items []protocol.FileInfo
			storage.Range(func(k, v any) bool {
				p := k.(string)
				if strings.HasPrefix(p, path) {
					isDir := v == "DIR"
					sz := int64(0)
					hStr := ""
					if b, ok := v.([]byte); ok {
						sz = int64(len(b))
						sum := sha256.Sum256(b)
						hStr = hex.EncodeToString(sum[:])
					}
					rel := strings.TrimPrefix(p, path)
					rel = strings.TrimPrefix(rel, "/")
					items = append(items, protocol.FileInfo{
						Name:   filepath.Base(p),
						Path:   rel,
						IsDir:  isDir,
						Size:   sz,
						SHA256: hStr,
					})
				}
				return true
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)

		default:
			http.NotFound(w, r)
		}
	}))

	return srv, storage
}

func TestClient_Transfer_RemoteTopologies(t *testing.T) {
	srvA, storageA := setupTransferMockWorker(t)
	defer srvA.Close()
	srvB, storageB := setupTransferMockWorker(t)
	defer srvB.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-a",
		Target: strings.TrimPrefix(srvA.URL, "http://"),
		Token:  "tok-a",
	})
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "node-b",
		Target: strings.TrimPrefix(srvB.URL, "http://"),
		Token:  "tok-b",
	})

	ctx := context.Background()
	tempDir := t.TempDir()

	// -------------------------------------------------------------
	// 1. 本地到远端 (Local to Remote)
	// -------------------------------------------------------------
	// 1.1 单文件上传
	localFile := filepath.Join(tempDir, "upload.txt")
	uploadData := []byte("upload-content-direct")
	_ = os.WriteFile(localFile, uploadData, 0644)

	err := cli.Transfer(ctx, TransferOptions{
		SrcPath: localFile,
		DstNode: "node-a",
		DstPath: "D:/remote/uploaded.txt",
	}, nil)
	if err != nil {
		t.Fatalf("local to remote file upload failed: %v", err)
	}
	if val, ok := storageA.Load("D:/remote/uploaded.txt"); !ok || string(val.([]byte)) != string(uploadData) {
		t.Fatalf("uploaded file mismatch on node-a: %v", val)
	}

	// 1.2 本地目录上传（缺少 -r 拦截）
	localDir := filepath.Join(tempDir, "local_dir")
	_ = os.MkdirAll(filepath.Join(localDir, "child"), 0755)
	_ = os.WriteFile(filepath.Join(localDir, "child", "f.txt"), []byte("child-f"), 0644)

	err = cli.Transfer(ctx, TransferOptions{
		SrcPath:   localDir,
		DstNode:   "node-a",
		DstPath:   "D:/remote/dir",
		Recursive: false,
	}, nil)
	if err == nil {
		t.Fatal("expected error for local dir upload without recursive")
	}
	var dirErr *ErrDirectoryWithoutRecursive
	if !errors.As(err, &dirErr) {
		t.Fatalf("expected *ErrDirectoryWithoutRecursive, got: %v", err)
	}

	// 1.3 本地目录上传（带 -r）
	err = cli.Transfer(ctx, TransferOptions{
		SrcPath:     localDir,
		DstNode:     "node-a",
		DstPath:     "D:/remote/dir",
		Recursive:   true,
		Concurrency: 4,
	}, nil)
	if err != nil {
		t.Fatalf("local dir upload failed: %v", err)
	}
	if val, ok := storageA.Load("D:/remote/dir/child/f.txt"); !ok || string(val.([]byte)) != "child-f" {
		t.Fatalf("uploaded dir file mismatch on node-a: %v", val)
	}

	// -------------------------------------------------------------
	// 2. 远端到本地 (Remote to Local)
	// -------------------------------------------------------------
	// 2.1 远端单文件下载
	storageA.Store("D:/remote/source.txt", []byte("remote-download-payload"))
	downloadDest := filepath.Join(tempDir, "downloaded.txt")

	err = cli.Transfer(ctx, TransferOptions{
		SrcNode: "node-a",
		SrcPath: "D:/remote/source.txt",
		DstPath: downloadDest,
	}, nil)
	if err != nil {
		t.Fatalf("remote to local file download failed: %v", err)
	}
	if val, err := os.ReadFile(downloadDest); err != nil || string(val) != "remote-download-payload" {
		t.Fatalf("downloaded content mismatch: %v, got %s", err, string(val))
	}

	// 2.2 远端目录下载（缺少 -r 拦截）
	storageA.Store("D:/remote/source_dir", "DIR")
	storageA.Store("D:/remote/source_dir/item.txt", []byte("item data"))

	err = cli.Transfer(ctx, TransferOptions{
		SrcNode:   "node-a",
		SrcPath:   "D:/remote/source_dir",
		DstPath:   filepath.Join(tempDir, "dl_dir"),
		Recursive: false,
	}, nil)
	if err == nil {
		t.Fatal("expected error for remote dir download without recursive")
	}
	if !errors.As(err, &dirErr) {
		t.Fatalf("expected *ErrDirectoryWithoutRecursive, got: %v", err)
	}
	if dirErr.Node != "node-a" {
		t.Errorf("expected node in ErrDirectoryWithoutRecursive to be 'node-a', got '%s'", dirErr.Node)
	}

	// 2.3 远端目录下载（带 -r）
	dlTargetDir := filepath.Join(tempDir, "dl_dir_ok")
	err = cli.Transfer(ctx, TransferOptions{
		SrcNode:     "node-a",
		SrcPath:     "D:/remote/source_dir",
		DstPath:     dlTargetDir,
		Recursive:   true,
		Concurrency: 4,
	}, nil)
	if err != nil {
		t.Fatalf("remote dir download failed: %v", err)
	}
	if val, err := os.ReadFile(filepath.Join(dlTargetDir, "item.txt")); err != nil || string(val) != "item data" {
		t.Fatalf("downloaded dir content mismatch: %v, got %s", err, string(val))
	}

	// -------------------------------------------------------------
	// 3. 远端到远端中继传输 (Remote to Remote Relay)
	// -------------------------------------------------------------
	// 3.1 远端到远端单文件拷贝
	storageA.Store("D:/remote/relay_src.txt", []byte("relay-direct-payload"))
	err = cli.Transfer(ctx, TransferOptions{
		SrcNode: "node-a",
		SrcPath: "D:/remote/relay_src.txt",
		DstNode: "node-b",
		DstPath: "D:/remote/relay_dst.txt",
	}, nil)
	if err != nil {
		t.Fatalf("remote to remote single file relay failed: %v", err)
	}
	if val, ok := storageB.Load("D:/remote/relay_dst.txt"); !ok || string(val.([]byte)) != "relay-direct-payload" {
		t.Fatalf("relayed file mismatch on node-b: %v", val)
	}

	// 3.2 远端到远端目录拷贝（缺少 -r 拦截）
	storageA.Store("D:/remote/relay_dir", "DIR")
	storageA.Store("D:/remote/relay_dir/sub.txt", []byte("sub data"))

	err = cli.Transfer(ctx, TransferOptions{
		SrcNode:   "node-a",
		SrcPath:   "D:/remote/relay_dir",
		DstNode:   "node-b",
		DstPath:   "D:/remote/relay_dir_dst",
		Recursive: false,
	}, nil)
	if err == nil {
		t.Fatal("expected error for remote-to-remote dir relay without -r")
	}
	if !errors.As(err, &dirErr) {
		t.Fatalf("expected *ErrDirectoryWithoutRecursive, got: %v", err)
	}

	// 3.3 远端到远端目录拷贝（带 -r）
	err = cli.Transfer(ctx, TransferOptions{
		SrcNode:     "node-a",
		SrcPath:     "D:/remote/relay_dir",
		DstNode:     "node-b",
		DstPath:     "D:/remote/relay_dir_dst",
		Recursive:   true,
		Concurrency: 4,
	}, nil)
	if err != nil {
		t.Fatalf("remote-to-remote dir relay failed: %v", err)
	}
	if val, ok := storageB.Load("D:/remote/relay_dir_dst/sub.txt"); !ok || string(val.([]byte)) != "sub data" {
		t.Fatalf("relayed dir file mismatch on node-b: %v", val)
	}
}

// TestClient_Transfer_RemoteError_NoFalseFallbackToFile 验证远端探测遇到网络/服务器异常时，立即暴露真实底层错误，严禁盲目降级走入单文件下载导致误报“请使用 -r”
func TestClient_Transfer_RemoteError_NoFalseFallbackToFile(t *testing.T) {
	downloadHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/fs/ls" {
			// 模拟远端目录列表网络中断或内部 500 异常
			http.Error(w, "connection reset / internal server glitch", http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/api/v1/fs/download" {
			downloadHit = true
			http.Error(w, "path is a directory, use recursive copy (-r)", http.StatusBadRequest)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cli := NewClient()
	cli.dataDir = t.TempDir()

	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "glitch-node",
		Target: strings.TrimPrefix(srv.URL, "http://"),
		Token:  "test-tok",
	})

	ctx := context.Background()
	tempDir := t.TempDir()

	// 用户明确传入了 Recursive: true
	err := cli.Transfer(ctx, TransferOptions{
		SrcNode:   "glitch-node",
		SrcPath:   "D:/some/remote/dir",
		DstPath:   filepath.Join(tempDir, "local_dst"),
		Recursive: true,
	}, nil)

	if err == nil {
		t.Fatal("expected error when remote ls failed, got nil")
	}

	// 核心断言 1：必须直接返回真实网络/服务端底层错误，严禁掩盖
	if !strings.Contains(err.Error(), "connection reset / internal server glitch") && !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected real underlying error, got: %v", err)
	}

	// 核心断言 2：绝不应向下穿透调用 /api/v1/fs/download 假装为单文件下载
	if downloadHit {
		t.Fatal("security/architecture violation: client falsely fell through to /api/v1/fs/download on remote ls failure")
	}
}

