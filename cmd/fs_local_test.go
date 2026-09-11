package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCmd_Local_CatAndMd(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 测试 md 本地递归创建目录 (带多层子目录)
	targetDir := filepath.Join(tempDir, "a", "b", "c")
	if err := mdCmd.RunE(mdCmd, []string{targetDir}); err != nil {
		t.Fatalf("local md failed: %v", err)
	}
	if fi, err := os.Stat(targetDir); err != nil || !fi.IsDir() {
		t.Fatalf("expected directory %s to be created", targetDir)
	}

	// 2. 测试 cat 本地输出文件内容
	testFile := filepath.Join(targetDir, "test.txt")
	testContent := []byte("antigravity cworker local cat test")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := catCmd.RunE(catCmd, []string{testFile})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if err != nil {
		t.Fatalf("local cat failed: %v", err)
	}
	if buf.String() != string(testContent) {
		t.Fatalf("expected cat output %q, got %q", string(testContent), buf.String())
	}
}

func TestCmd_Local_Cp_EmptyDirAndFiles(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	dstDir := filepath.Join(tempDir, "dst")

	// 创建空文件与空子目录
	emptySubDir := filepath.Join(srcDir, "empty_sub")
	_ = os.MkdirAll(emptySubDir, 0755)
	emptyFile := filepath.Join(srcDir, "empty.txt")
	_ = os.WriteFile(emptyFile, []byte{}, 0644)

	cpCmd.Flags().Set("recursive", "true")
	defer cpCmd.Flags().Set("recursive", "false")

	if err := cpCmd.RunE(cpCmd, []string{srcDir, dstDir}); err != nil {
		t.Fatalf("local copy with empty dirs and files failed: %v", err)
	}

	// 验证空子目录守恒
	dstEmptySub := filepath.Join(dstDir, "empty_sub")
	if fi, err := os.Stat(dstEmptySub); err != nil || !fi.IsDir() {
		t.Fatalf("empty directory was not copied: %v", err)
	}

	// 验证空文件
	dstEmptyFile := filepath.Join(dstDir, "empty.txt")
	if fi, err := os.Stat(dstEmptyFile); err != nil || fi.Size() != 0 {
		t.Fatalf("empty file was not preserved: %v", err)
	}
}

func TestCmd_Local_LsAndRm(t *testing.T) {
	tempDir := t.TempDir()
	file1 := filepath.Join(tempDir, "f1.txt")
	subDir := filepath.Join(tempDir, "sub")
	_ = os.WriteFile(file1, []byte("test"), 0644)
	_ = os.MkdirAll(subDir, 0755)

	// 1. 本地 ls 测试
	if err := lsCmd.RunE(lsCmd, []string{tempDir}); err != nil {
		t.Fatalf("local ls failed: %v", err)
	}

	// 2. 本地 rm 单文件测试 (-y 自动确认)
	rmCmd.Flags().Set("yes", "true")
	rmCmd.Flags().Set("recursive", "false")
	if err := rmCmd.RunE(rmCmd, []string{file1}); err != nil {
		t.Fatalf("local rm single file failed: %v", err)
	}
	if _, err := os.Stat(file1); !os.IsNotExist(err) {
		t.Fatalf("file %s should have been deleted", file1)
	}

	// 3. 本地 rm 非空目录未传 -r 被拦截
	subFile := filepath.Join(subDir, "subfile.txt")
	_ = os.WriteFile(subFile, []byte("sub"), 0644)
	err := rmCmd.RunE(rmCmd, []string{subDir})
	if err == nil {
		t.Fatal("expected error when deleting non-empty directory without -r")
	}

	// 4. 本地 rm 递归删除 (-r -y)
	rmCmd.Flags().Set("recursive", "true")
	defer func() {
		rmCmd.Flags().Set("recursive", "false")
		rmCmd.Flags().Set("yes", "false")
	}()
	if err := rmCmd.RunE(rmCmd, []string{subDir}); err != nil {
		t.Fatalf("local rm recursive failed: %v", err)
	}
	if _, err := os.Stat(subDir); !os.IsNotExist(err) {
		t.Fatalf("directory %s should have been deleted", subDir)
	}
}
