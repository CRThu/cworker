package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
)

func TestCmd_Diff_SingleFile_Identical(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "file1.txt")
	f2 := filepath.Join(tempDir, "file2.txt")
	content := []byte("identical content 12345")
	_ = os.WriteFile(f1, content, 0644)
	_ = os.WriteFile(f2, content, 0644)

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{f1, f2})
	if err != nil {
		t.Fatalf("expected nil error for identical single files, got: %v", err)
	}
}

func TestCmd_Diff_SingleFile_Mismatch(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "file1.txt")
	f2 := filepath.Join(tempDir, "file2.txt")
	_ = os.WriteFile(f1, []byte("content A"), 0644)
	_ = os.WriteFile(f2, []byte("content B differs"), 0644)

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{f1, f2})
	if err == nil {
		t.Fatal("expected error for mismatched single files")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError with code 1, got: %v", err)
	}
}

func TestCmd_Diff_SingleFile_Missing(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "file1.txt")
	f2 := filepath.Join(tempDir, "missing.txt")
	_ = os.WriteFile(f1, []byte("content A"), 0644)

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{f1, f2})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("expected ExitError with code 2 for missing file, got: %v", err)
	}
}

func TestCmd_Diff_Directory_WithoutRecursiveFlag(t *testing.T) {
	tempDir := t.TempDir()
	dir1 := filepath.Join(tempDir, "dir1")
	dir2 := filepath.Join(tempDir, "dir2")
	_ = os.MkdirAll(dir1, 0755)
	_ = os.MkdirAll(dir2, 0755)

	diffCmd.Flags().Set("recursive", "false")
	err := diffCmd.RunE(diffCmd, []string{dir1, dir2})
	if err == nil {
		t.Fatal("expected omitting directory error when -r is not passed")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 2 || !strings.Contains(exitErr.Msg, "omitting directory") {
		t.Fatalf("expected exit code 2 with omitting directory message, got: %v", err)
	}
}

func TestCmd_Diff_Directory_Identical(t *testing.T) {
	tempDir := t.TempDir()
	dir1 := filepath.Join(tempDir, "dir1")
	dir2 := filepath.Join(tempDir, "dir2")
	_ = os.MkdirAll(filepath.Join(dir1, "sub"), 0755)
	_ = os.MkdirAll(filepath.Join(dir2, "sub"), 0755)

	_ = os.WriteFile(filepath.Join(dir1, "a.txt"), []byte("data a"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "a.txt"), []byte("data a"), 0644)
	_ = os.WriteFile(filepath.Join(dir1, "sub", "b.txt"), []byte("data b in sub"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "sub", "b.txt"), []byte("data b in sub"), 0644)

	diffCmd.Flags().Set("recursive", "true")
	defer diffCmd.Flags().Set("recursive", "false")

	err := diffCmd.RunE(diffCmd, []string{dir1, dir2})
	if err != nil {
		t.Fatalf("expected identical directories to return nil, got: %v", err)
	}
}

func TestCmd_Diff_Directory_WithDifferences_OmitMatch(t *testing.T) {
	tempDir := t.TempDir()
	dir1 := filepath.Join(tempDir, "dir1")
	dir2 := filepath.Join(tempDir, "dir2")
	_ = os.MkdirAll(dir1, 0755)
	_ = os.MkdirAll(dir2, 0755)

	// 1. 同名且相同内容 (MATCH 项，应当被默认过滤掉)
	_ = os.WriteFile(filepath.Join(dir1, "matched.txt"), []byte("matched content"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "matched.txt"), []byte("matched content"), 0644)

	// 2. 同名但内容不同 (MODIFIED 项)
	_ = os.WriteFile(filepath.Join(dir1, "modified.txt"), []byte("original"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "modified.txt"), []byte("changed content"), 0644)

	// 3. 仅源端存在 (ADDED 项)
	_ = os.WriteFile(filepath.Join(dir1, "only_in_src.txt"), []byte("new file"), 0644)

	// 4. 仅目标端存在 (DELETED 项)
	_ = os.WriteFile(filepath.Join(dir2, "only_in_dst.txt"), []byte("deleted file"), 0644)

	diffCmd.Flags().Set("recursive", "true")
	defer diffCmd.Flags().Set("recursive", "false")

	// 捕获 stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := diffCmd.RunE(diffCmd, []string{dir1, dir2})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if err == nil {
		t.Fatal("expected error with exit code 1 when differences exist")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError with code 1, got: %v", err)
	}

	// 验证关键断言：
	// 1. [MATCH] 必须被默认排除！
	if strings.Contains(output, "[MATCH]") {
		t.Fatalf("output must NOT contain [MATCH] entries by default, got output:\n%s", output)
	}

	// 2. 差异项必须完整呈现
	if !strings.Contains(output, "[MODIFIED]") || !strings.Contains(output, "modified.txt") {
		t.Fatalf("missing [MODIFIED] entry in output:\n%s", output)
	}
	if !strings.Contains(output, "[ADDED]") || !strings.Contains(output, "only_in_src.txt") {
		t.Fatalf("missing [ADDED] entry in output:\n%s", output)
	}
	if !strings.Contains(output, "[DELETED]") || !strings.Contains(output, "only_in_dst.txt") {
		t.Fatalf("missing [DELETED] entry in output:\n%s", output)
	}

	// 3. 统计行必须准确记录匹配数与变动数
	expectedSummary := "Summary: 1 matched, 1 modified, 1 added, 1 deleted."
	if !strings.Contains(output, expectedSummary) {
		t.Fatalf("expected summary %q, got output:\n%s", expectedSummary, output)
	}
}

func TestCmd_Diff_CrossNode(t *testing.T) {
	tempProfile := t.TempDir()
	t.Setenv("USERPROFILE", tempProfile)

	localFile := filepath.Join(tempProfile, "local.txt")
	content := []byte("cross node content")
	_ = os.WriteFile(localFile, content, 0644)

	// Mock Worker Server
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/fs/hash" {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		path := r.URL.Query().Get("path")
		if path == "D:/same.txt" {
			list := []protocol.FileInfo{
				{Name: "same.txt", Path: "same.txt", Size: int64(len(content)), SHA256: "c18e1ef67c3df95a5639b56f8f48039d91a92e1281cb9fc61a941f534444585e"},
			}
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(list)
			return
		}
		if path == "D:/diff.txt" {
			list := []protocol.FileInfo{
				{Name: "diff.txt", Path: "diff.txt", Size: 999, SHA256: "different_hash_value"},
			}
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(list)
			return
		}
		rw.WriteHeader(http.StatusNotFound)
		_, _ = rw.Write([]byte("path not found"))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)

	// 配置 mock-node 和 mock-node2
	cli := client.NewClient()
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-node",
		Target: u.Host,
	})
	_ = cli.SaveKnownNode(protocol.KnownNode{
		Name:   "mock-node2",
		Target: u.Host,
	})

	diffCmd.Flags().Set("recursive", "false")

	// 1. 本地 vs 远端 (内容不同)
	diffTarget := "mock-node:D:/diff.txt"
	err := diffCmd.RunE(diffCmd, []string{localFile, diffTarget})
	if err == nil {
		t.Fatal("expected difference for local vs remote")
	}
	if exitErr, ok := err.(*ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError 1, got: %v", err)
	}

	// 2. 远端 vs 远端 (两远端节点内容不同)
	sameTarget := "mock-node2:D:/same.txt"
	err = diffCmd.RunE(diffCmd, []string{sameTarget, diffTarget})
	if err == nil {
		t.Fatal("expected difference for remote vs remote")
	}
	if exitErr, ok := err.(*ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected ExitError 1, got: %v", err)
	}

	// 3. 远端不存在路径报错 (ExitCode 2)
	missingTarget := "mock-node:D:/missing.txt"
	err = diffCmd.RunE(diffCmd, []string{localFile, missingTarget})
	if err == nil {
		t.Fatal("expected error for remote missing path")
	}
	if exitErr, ok := err.(*ExitError); !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("expected ExitError 2, got: %v", err)
	}
}
