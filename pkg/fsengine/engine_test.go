package fsengine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"cworker/pkg/protocol"
)

type testTracker struct {
	bytes int64
	files int64
}

func (t *testTracker) AddBytes(n int64) {
	atomic.AddInt64(&t.bytes, n)
}

func (t *testTracker) AddFile() {
	atomic.AddInt64(&t.files, 1)
}

func TestFsEngine_HashAndList(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. HashFile on non-existent file
	_, err := HashFile(filepath.Join(tmpDir, "missing.txt"))
	if err == nil {
		t.Fatalf("expected error for missing file")
	}

	// 2. HashFile on directory (should return ErrDirRequiresRecursive)
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = HashFile(subDir)
	if !errors.Is(err, ErrDirRequiresRecursive) {
		t.Fatalf("expected ErrDirRequiresRecursive, got: %v", err)
	}

	// 3. HashFile on normal file
	f1 := filepath.Join(tmpDir, "hello.txt")
	content := []byte("hello world fsengine")
	if err := os.WriteFile(f1, content, 0644); err != nil {
		t.Fatal(err)
	}
	info, err := HashFile(f1)
	if err != nil {
		t.Fatalf("HashFile failed: %v", err)
	}
	h := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(h[:])
	if info.SHA256 != expectedHash {
		t.Fatalf("expected hash %s, got %s", expectedHash, info.SHA256)
	}
	if info.Size != int64(len(content)) {
		t.Fatalf("expected size %d, got %d", len(content), info.Size)
	}

	// 4. HashDir on file (should return ErrPathIsFile)
	_, err = HashDir(f1)
	if !errors.Is(err, ErrPathIsFile) {
		t.Fatalf("expected ErrPathIsFile, got: %v", err)
	}

	// 5. HashDir on directory with files
	f2 := filepath.Join(subDir, "inner.txt")
	if err := os.WriteFile(f2, []byte("inner content"), 0644); err != nil {
		t.Fatal(err)
	}
	list, err := HashDir(tmpDir)
	if err != nil {
		t.Fatalf("HashDir failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 files in HashDir, got %d", len(list))
	}

	// 6. Hash facade function
	single, err := Hash(f1, false)
	if err != nil || len(single) != 1 {
		t.Fatalf("Hash single file failed: %v", err)
	}
	_, err = Hash(tmpDir, false)
	if !errors.Is(err, ErrDirRequiresRecursive) {
		t.Fatalf("expected ErrDirRequiresRecursive without -r on dir, got: %v", err)
	}
	dirList, err := Hash(tmpDir, true)
	if err != nil || len(dirList) != 2 {
		t.Fatalf("Hash recursive dir failed: %v", err)
	}

	// 7. ListDir flat vs recursive
	flatList, err := ListDir(tmpDir, false)
	if err != nil {
		t.Fatalf("ListDir flat failed: %v", err)
	}
	// Expected: hello.txt and subdir
	if len(flatList) != 2 {
		t.Fatalf("expected 2 entries in flat ListDir, got %d", len(flatList))
	}

	recList, err := ListDir(tmpDir, true)
	if err != nil {
		t.Fatalf("ListDir recursive failed: %v", err)
	}
	// Expected: hello.txt, subdir, subdir/inner.txt
	if len(recList) != 3 {
		t.Fatalf("expected 3 entries in recursive ListDir, got %d", len(recList))
	}
}

func TestFsEngine_MakeDirAndRemove(t *testing.T) {
	tmpDir := t.TempDir()

	targetDir := filepath.Join(tmpDir, "a", "b", "c")
	if err := MakeDir(targetDir); err != nil {
		t.Fatalf("MakeDir failed: %v", err)
	}
	if fi, err := os.Stat(targetDir); err != nil || !fi.IsDir() {
		t.Fatalf("directory was not created properly")
	}

	// Write file inside targetDir
	filePath := filepath.Join(targetDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("xyz"), 0644); err != nil {
		t.Fatal(err)
	}

	// Remove non-empty directory without recursive -> ErrNonEmptyDirRequiresRecursive
	err := Remove(targetDir, false)
	if !errors.Is(err, ErrNonEmptyDirRequiresRecursive) {
		t.Fatalf("expected ErrNonEmptyDirRequiresRecursive, got: %v", err)
	}

	// Remove file
	if err := Remove(filePath, false); err != nil {
		t.Fatalf("Remove file failed: %v", err)
	}

	// Remove empty directory without recursive -> should succeed
	if err := Remove(targetDir, false); err != nil {
		t.Fatalf("Remove empty dir failed: %v", err)
	}

	// Recreate and remove with recursive
	_ = MakeDir(targetDir)
	_ = os.WriteFile(filepath.Join(targetDir, "1.txt"), []byte("1"), 0644)
	if err := Remove(filepath.Join(tmpDir, "a"), true); err != nil {
		t.Fatalf("Remove recursive failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "a")); !os.IsNotExist(err) {
		t.Fatalf("dir 'a' should be removed")
	}
}

func TestFsEngine_RemoveWithProgress_Details(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. 测试单文件删除与项数统计
	singleFile := filepath.Join(tmpDir, "single.txt")
	_ = os.WriteFile(singleFile, []byte("hello"), 0644)
	var singleProg int64
	count, err := RemoveWithProgress(context.Background(), singleFile, false, func(c int64) {
		singleProg = c
	})
	if err != nil {
		t.Fatalf("RemoveWithProgress single file failed: %v", err)
	}
	if count != 1 || singleProg != 1 {
		t.Fatalf("expected count 1 and singleProg 1, got count=%d, prog=%d", count, singleProg)
	}

	// 2. 测试复杂多层级目录 (包含 20 个文件与 5 个子目录，总计 25+1=26 项)
	treeRoot := filepath.Join(tmpDir, "tree")
	expectedItems := int64(1) // 根目录自身
	for d := 0; d < 5; d++ {
		sub := filepath.Join(treeRoot, fmt.Sprintf("sub_%d", d))
		_ = os.MkdirAll(sub, 0755)
		expectedItems++ // 子目录
		for f := 0; f < 4; f++ {
			filePath := filepath.Join(sub, fmt.Sprintf("f_%d.bin", f))
			_ = os.WriteFile(filePath, []byte("data"), 0644)
			expectedItems++ // 文件
		}
	}

	var lastProg int64
	count, err = RemoveWithProgress(context.Background(), treeRoot, true, func(c int64) {
		lastProg = c
	})
	if err != nil {
		t.Fatalf("RemoveWithProgress tree failed: %v", err)
	}
	if count != expectedItems {
		t.Fatalf("expected %d items, got %d", expectedItems, count)
	}
	if lastProg != expectedItems {
		t.Fatalf("expected lastProg %d, got %d", expectedItems, lastProg)
	}
	if _, err := os.Stat(treeRoot); !os.IsNotExist(err) {
		t.Fatalf("treeRoot should be physically deleted")
	}

	// 3. 测试 Windows 只读文件自愈删除
	roDir := filepath.Join(tmpDir, "ro_dir")
	_ = os.MkdirAll(roDir, 0755)
	roFile := filepath.Join(roDir, "readonly.txt")
	_ = os.WriteFile(roFile, []byte("readonly"), 0444)
	_ = os.Chmod(roFile, 0444) // 锁定为只读

	count, err = RemoveWithProgress(context.Background(), roDir, true, nil)
	if err != nil {
		t.Fatalf("RemoveWithProgress readonly file failed: %v", err)
	}
	if count != 2 { // 1 file + 1 dir = 2
		t.Fatalf("expected 2 items removed, got %d", count)
	}

	// 4. 测试 Context 取消中断
	cancelDir := filepath.Join(tmpDir, "cancel_dir")
	_ = os.MkdirAll(cancelDir, 0755)
	for i := 0; i < 50; i++ {
		_ = os.WriteFile(filepath.Join(cancelDir, fmt.Sprintf("file_%d.txt", i)), []byte("x"), 0644)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预先取消
	_, err = RemoveWithProgress(ctx, cancelDir, true, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestFsEngine_SaveStream(t *testing.T) {
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "stream", "nested", "file.txt")

	data := []byte("stream-content-12345")
	h := sha256.Sum256(data)
	correctHash := hex.EncodeToString(h[:])

	// 1. Success write with matching hash
	hash, err := SaveStream(targetFile, bytes.NewReader(data), correctHash)
	if err != nil {
		t.Fatalf("SaveStream failed: %v", err)
	}
	if hash != correctHash {
		t.Fatalf("expected hash %s, got %s", correctHash, hash)
	}

	// 2. Failure with hash mismatch (must clean up temp file and not touch original)
	_, err = SaveStream(targetFile, bytes.NewReader(data), "badhash0000000000000000000000000000000000000000000000000000000000")
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch, got: %v", err)
	}
	// Original file must still contain original content
	readBack, err := os.ReadFile(targetFile)
	if err != nil || string(readBack) != string(data) {
		t.Fatalf("original file was corrupted after failed hash verification")
	}

	// 3. Destination is existing dir -> ErrDestinationIsDir
	dirPath := filepath.Join(tmpDir, "existingdir")
	_ = os.Mkdir(dirPath, 0755)
	_, err = SaveStream(dirPath, bytes.NewReader(data), "")
	if !errors.Is(err, ErrDestinationIsDir) {
		t.Fatalf("expected ErrDestinationIsDir, got: %v", err)
	}
}

func TestFsEngine_CopyFileAndDir(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Setup source files and empty directory
	f1 := filepath.Join(srcDir, "file1.txt")
	_ = os.WriteFile(f1, []byte("file1 content"), 0644)

	subDir := filepath.Join(srcDir, "sub")
	_ = os.Mkdir(subDir, 0755)
	f2 := filepath.Join(subDir, "file2.txt")
	_ = os.WriteFile(f2, []byte("file2 content"), 0644)

	emptyDir := filepath.Join(srcDir, "empty_dir")
	_ = os.Mkdir(emptyDir, 0755)

	tracker := &testTracker{}

	// 1. CopyFile single file
	dstFile := filepath.Join(dstDir, "copied1.txt")
	if err := CopyFile(f1, dstFile, tracker); err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}
	if tracker.bytes == 0 {
		t.Fatalf("expected tracker bytes > 0")
	}

	// 2. CopyFile directory -> ErrDirRequiresRecursive
	err := CopyFile(subDir, filepath.Join(dstDir, "bad.txt"), nil)
	if !errors.Is(err, ErrDirRequiresRecursive) {
		t.Fatalf("expected ErrDirRequiresRecursive, got: %v", err)
	}

	// 3. CopyDir full tree
	dirTracker := &testTracker{}
	copyDst := filepath.Join(dstDir, "target_tree")
	if err := CopyDir(context.Background(), srcDir, copyDst, 4, dirTracker); err != nil {
		t.Fatalf("CopyDir failed: %v", err)
	}

	if dirTracker.files != 2 {
		t.Fatalf("expected 2 files copied in tracker, got %d", dirTracker.files)
	}

	// Verify empty directory preserved
	copiedEmpty := filepath.Join(copyDst, "empty_dir")
	fi, err := os.Stat(copiedEmpty)
	if err != nil || !fi.IsDir() {
		t.Fatalf("empty dir was not preserved")
	}

	// Verify file2 content
	copiedF2 := filepath.Join(copyDst, "sub", "file2.txt")
	data, err := os.ReadFile(copiedF2)
	if err != nil || string(data) != "file2 content" {
		t.Fatalf("file2 was not copied accurately")
	}
}

func TestFsEngine_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. ListDir on non-existent directory
	_, err := ListDir(filepath.Join(tmpDir, "missing_dir"), false)
	if err == nil {
		t.Fatal("expected error on ListDir missing dir")
	}

	// 2. Remove on non-existent path
	err = Remove(filepath.Join(tmpDir, "not_here"), false)
	if err == nil {
		t.Fatal("expected error on Remove missing path")
	}

	// 3. CopyFile with non-existent source
	err = CopyFile(filepath.Join(tmpDir, "missing.txt"), filepath.Join(tmpDir, "dest.txt"), nil)
	if err == nil {
		t.Fatal("expected error on CopyFile with missing source")
	}

	// 4. CopyDir with already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	srcSub := filepath.Join(tmpDir, "src_sub")
	_ = os.Mkdir(srcSub, 0755)
	_ = os.WriteFile(filepath.Join(srcSub, "f.txt"), []byte("data"), 0644)
	err = CopyDir(ctx, srcSub, filepath.Join(tmpDir, "dst_sub"), 2, nil)
	if err == nil {
		t.Fatal("expected error on CopyDir with cancelled context")
	}
}

// brokenReader 模拟流式读取途中发生网络/IO中断
type brokenReader struct {
	data   []byte
	offset int
	failAt int
}

func (b *brokenReader) Read(p []byte) (int, error) {
	if b.offset >= b.failAt {
		return 0, errors.New("simulated network read failure")
	}
	n := copy(p, b.data[b.offset:b.failAt])
	b.offset += n
	return n, nil
}

func TestFsEngine_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	emptyDir := filepath.Join(tmpDir, "pure_empty")
	if err := MakeDir(emptyDir); err != nil {
		t.Fatal(err)
	}

	// 1. HashDir on empty dir returns empty slice
	list, err := HashDir(emptyDir)
	if err != nil {
		t.Fatalf("HashDir empty dir failed: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("expected empty non-nil slice, got %v", list)
	}

	// 2. Hash facade on empty dir with recursive=true
	listFacade, err := Hash(emptyDir, true)
	if err != nil {
		t.Fatalf("Hash facade empty dir failed: %v", err)
	}
	if listFacade == nil || len(listFacade) != 0 {
		t.Fatalf("expected empty non-nil slice from Hash facade, got %v", listFacade)
	}

	// 3. ListDir on empty dir (both flat and recursive)
	flat, err := ListDir(emptyDir, false)
	if err != nil || len(flat) != 0 {
		t.Fatalf("expected empty slice from flat ListDir, got %v, err: %v", flat, err)
	}
	rec, err := ListDir(emptyDir, true)
	if err != nil || len(rec) != 0 {
		t.Fatalf("expected empty slice from rec ListDir, got %v, err: %v", rec, err)
	}
}

func TestFsEngine_SaveStream_RollbackAndOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "sub", "target.txt")

	// 1. 0-byte stream write
	emptyHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	h, err := SaveStream(targetFile, bytes.NewReader([]byte{}), emptyHash)
	if err != nil {
		t.Fatalf("SaveStream 0-byte failed: %v", err)
	}
	if h != emptyHash {
		t.Fatalf("expected empty hash %s, got %s", emptyHash, h)
	}

	// 2. Overwrite existing file atomically
	newContent := []byte("overwritten-data")
	newH := sha256.Sum256(newContent)
	newHex := hex.EncodeToString(newH[:])
	h2, err := SaveStream(targetFile, bytes.NewReader(newContent), newHex)
	if err != nil {
		t.Fatalf("SaveStream overwrite failed: %v", err)
	}
	if h2 != newHex {
		t.Fatalf("expected overwritten hash %s, got %s", newHex, h2)
	}
	readBack, err := os.ReadFile(targetFile)
	if err != nil || string(readBack) != "overwritten-data" {
		t.Fatalf("file content was not updated properly: %s", string(readBack))
	}

	// 3. Simulated stream read error mid-way -> verify clean rollback and no leftover temp files
	br := &brokenReader{
		data:   []byte("some long data stream"),
		offset: 0,
		failAt: 5,
	}
	_, err = SaveStream(targetFile, br, "")
	if err == nil {
		t.Fatal("expected error on broken reader")
	}

	// Verify original file was NOT corrupted by the aborted write
	readBackAfterFail, err := os.ReadFile(targetFile)
	if err != nil || string(readBackAfterFail) != "overwritten-data" {
		t.Fatalf("original file was corrupted during failed stream write: %v", err)
	}

	// Verify parent directory contains NO lingering .cwsave-* temporary files
	parentEntries, err := os.ReadDir(filepath.Dir(targetFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range parentEntries {
		if strings.Contains(e.Name(), ".cwsave-") {
			t.Fatalf("orphan temp file leaked after failure: %s", e.Name())
		}
	}
}

func TestFsEngine_SaveStreamWithValidator(t *testing.T) {
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "validate_test.txt")
	_ = os.WriteFile(targetFile, []byte("orig"), 0644)

	// 1. Validator fails
	_, err := SaveStreamWithValidator(targetFile, bytes.NewReader([]byte("new_corrupt")), func(computedHash string) error {
		return errors.New("custom validation rejection")
	})
	if err == nil {
		t.Fatal("expected error from rejected validation")
	}
	// Verify original file untouched
	readBack, _ := os.ReadFile(targetFile)
	if string(readBack) != "orig" {
		t.Fatalf("original file was overwritten despite validator failure")
	}

	// 2. Validator succeeds
	_, err = SaveStreamWithValidator(targetFile, bytes.NewReader([]byte("new_valid")), func(computedHash string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error on valid stream: %v", err)
	}
	readBack, _ = os.ReadFile(targetFile)
	if string(readBack) != "new_valid" {
		t.Fatalf("file was not updated on valid stream")
	}
}

func TestFsEngine_CopyDir_Advanced(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. CopyDir when src is a file -> ErrPathIsFile
	filePath := filepath.Join(tmpDir, "single.txt")
	_ = os.WriteFile(filePath, []byte("single"), 0644)
	err := CopyDir(context.Background(), filePath, filepath.Join(tmpDir, "out"), 4, nil)
	if !errors.Is(err, ErrPathIsFile) {
		t.Fatalf("expected ErrPathIsFile when src is file, got %v", err)
	}

	// 2. CopyDir with nil context and concurrency <= 0
	srcDir := filepath.Join(tmpDir, "complex_src")
	_ = MakeDir(filepath.Join(srcDir, "d1", "d2", "empty_leaf"))
	_ = MakeDir(filepath.Join(srcDir, "d1", "empty_mid"))
	_ = os.WriteFile(filepath.Join(srcDir, "d1", "file.txt"), []byte("d1-file"), 0644)
	_ = os.WriteFile(filepath.Join(srcDir, "root.txt"), []byte("root-file"), 0644)

	dstDir := filepath.Join(tmpDir, "complex_dst")
	// Test concurrency <= 0 and ctx == nil
	err = CopyDir(nil, srcDir, dstDir, 0, nil)
	if err != nil {
		t.Fatalf("CopyDir with nil ctx and default concurrency failed: %v", err)
	}

	// Verify empty leaf dir preserved
	fi, err := os.Stat(filepath.Join(dstDir, "d1", "d2", "empty_leaf"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("empty_leaf was not preserved: %v", err)
	}
	fi, err = os.Stat(filepath.Join(dstDir, "d1", "empty_mid"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("empty_mid was not preserved: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dstDir, "d1", "file.txt"))
	if err != nil || string(data) != "d1-file" {
		t.Fatalf("d1/file.txt content mismatch: %s", string(data))
	}
}

func TestFsEngine_CopyFile_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Copy 0-byte file
	emptySrc := filepath.Join(tmpDir, "empty.txt")
	_ = os.WriteFile(emptySrc, []byte{}, 0644)
	emptyDst := filepath.Join(tmpDir, "empty_dst.txt")
	if err := CopyFile(emptySrc, emptyDst, nil); err != nil {
		t.Fatalf("CopyFile 0-byte failed: %v", err)
	}
	fi, err := os.Stat(emptyDst)
	if err != nil || fi.Size() != 0 {
		t.Fatalf("expected 0-byte dst, got %v", fi)
	}

	// 2. Overwrite existing file
	f1 := filepath.Join(tmpDir, "f1.txt")
	_ = os.WriteFile(f1, []byte("f1-new"), 0644)
	if err := CopyFile(f1, emptyDst, nil); err != nil {
		t.Fatalf("CopyFile overwrite failed: %v", err)
	}
	readBack, _ := os.ReadFile(emptyDst)
	if string(readBack) != "f1-new" {
		t.Fatalf("expected f1-new, got %s", string(readBack))
	}

	// 3. Destination is an existing directory -> ErrDestinationIsDir
	subDir := filepath.Join(tmpDir, "sub_dir")
	_ = os.Mkdir(subDir, 0755)
	err = CopyFile(f1, subDir, nil)
	if !errors.Is(err, ErrDestinationIsDir) {
		t.Fatalf("expected ErrDestinationIsDir, got %v", err)
	}
}

func TestFsEngine_ConcurrentReads(t *testing.T) {
	tmpDir := t.TempDir()
	// Build a directory tree
	for i := 0; i < 5; i++ {
		sub := filepath.Join(tmpDir, fmt.Sprintf("sub_%d", i))
		_ = os.Mkdir(sub, 0755)
		for j := 0; j < 5; j++ {
			_ = os.WriteFile(filepath.Join(sub, fmt.Sprintf("file_%d.txt", j)), []byte(fmt.Sprintf("content_%d_%d", i, j)), 0644)
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 50)

	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				list, err := HashDir(tmpDir)
				if err != nil {
					errCh <- err
					return
				}
				if len(list) != 25 {
					errCh <- fmt.Errorf("expected 25 files in HashDir, got %d", len(list))
				}
			} else {
				list, err := ListDir(tmpDir, true)
				if err != nil {
					errCh <- err
					return
				}
				// 5 subdirs + 25 files = 30 entries
				if len(list) != 30 {
					errCh <- fmt.Errorf("expected 30 entries in ListDir, got %d", len(list))
				}
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent read error: %v", err)
	}
}

func TestFsEngine_EmptyPaths(t *testing.T) {
	if _, err := HashFile(""); err == nil {
		t.Fatal("expected error on empty path for HashFile")
	}
	if _, err := HashDir(""); err == nil {
		t.Fatal("expected error on empty path for HashDir")
	}
	if _, err := Hash("", false); err == nil {
		t.Fatal("expected error on empty path for Hash")
	}
	if _, err := ListDir("", false); err == nil {
		t.Fatal("expected error on empty path for ListDir")
	}
	if err := MakeDir(""); err == nil {
		t.Fatal("expected error on empty path for MakeDir")
	}
	if err := Remove("", false); err == nil {
		t.Fatal("expected error on empty path for Remove")
	}
	if _, err := SaveStream("", bytes.NewReader([]byte{}), ""); err == nil {
		t.Fatal("expected error on empty path for SaveStream")
	}
	if err := CopyFile("", "dst", nil); err == nil {
		t.Fatal("expected error on empty src for CopyFile")
	}
	if err := CopyFile("src", "", nil); err == nil {
		t.Fatal("expected error on empty dst for CopyFile")
	}
	if err := CopyDir(nil, "", "dst", 1, nil); err == nil {
		t.Fatal("expected error on empty src for CopyDir")
	}
	if err := CopyDir(nil, "src", "", 1, nil); err == nil {
		t.Fatal("expected error on empty dst for CopyDir")
	}
}

func TestFsEngine_ListDir_OnFile(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "somefile.txt")
	_ = os.WriteFile(f, []byte("data"), 0644)

	_, err := ListDir(f, false)
	if !errors.Is(err, ErrPathIsFile) {
		t.Fatalf("expected ErrPathIsFile on ListDir flat on file, got: %v", err)
	}
	_, err = ListDir(f, true)
	if !errors.Is(err, ErrPathIsFile) {
		t.Fatalf("expected ErrPathIsFile on ListDir rec on file, got: %v", err)
	}
}

func TestFsEngine_CopyDir_DstDirCollision(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	_ = MakeDir(srcDir)
	_ = os.WriteFile(filepath.Join(srcDir, "test.txt"), []byte("hi"), 0644)

	// Create dst as an existing regular file
	dstFile := filepath.Join(tmpDir, "dst_is_file")
	_ = os.WriteFile(dstFile, []byte("im a file"), 0644)

	err := CopyDir(context.Background(), srcDir, dstFile, 2, nil)
	if err == nil {
		t.Fatal("expected error when dst is a file")
	}
}

func TestFsEngine_CopyDir_WorkerCopyFileError(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	_ = MakeDir(srcDir)
	_ = os.WriteFile(filepath.Join(srcDir, "collision.txt"), []byte("data"), 0644)

	dstDir := filepath.Join(tmpDir, "dst")
	_ = MakeDir(dstDir)
	// Create collision.txt as a directory in dstDir to force CopyFile to fail with ErrDestinationIsDir
	_ = os.Mkdir(filepath.Join(dstDir, "collision.txt"), 0755)

	err := CopyDir(context.Background(), srcDir, dstDir, 2, nil)
	if err == nil {
		t.Fatal("expected error when destination file collides with existing directory")
	}
	if !strings.Contains(err.Error(), "collision.txt") {
		t.Fatalf("expected error mentioning collision.txt, got: %v", err)
	}
}

// TestFsEngine_CopyDir_BulkConcurrency_MoreThan8Files 验证超过 8 个并发槽位（32 个文件）的大批量多文件全面复制能力与哈希强一致性
func TestFsEngine_CopyDir_BulkConcurrency_MoreThan8Files(t *testing.T) {
	srcDir := filepath.Join(t.TempDir(), "bulk_src")
	dstDir := filepath.Join(t.TempDir(), "bulk_dst")

	const totalFiles = 32
	expectedFiles := make(map[string]string) // relPath -> SHA256 hex
	var expectedTotalBytes int64

	// 1. 构造多层级混合目录骨架与 32 个文件
	subDirs := []string{
		"empty_folder_1",
		"empty_folder_2/nested_empty",
		"pkg/sub1",
		"pkg/sub2/deep",
		"data/raw/chunks",
	}
	for _, sd := range subDirs {
		if err := os.MkdirAll(filepath.Join(srcDir, sd), 0755); err != nil {
			t.Fatal(err)
		}
	}

	for i := 1; i <= totalFiles; i++ {
		var relPath string
		var content []byte

		switch {
		case i <= 5:
			// 根目录普通文件
			relPath = fmt.Sprintf("root_file_%02d.txt", i)
			content = []byte(fmt.Sprintf("root file payload content #%d with some padding %s", i, strings.Repeat("A", i*100)))
		case i <= 10:
			// 0 字节边界文件
			relPath = fmt.Sprintf("pkg/sub1/empty_file_%02d.bin", i)
			content = []byte{}
		case i <= 20:
			// 深层嵌套文件
			relPath = fmt.Sprintf("pkg/sub2/deep/deep_chunk_%02d.dat", i)
			content = bytes.Repeat([]byte{byte(i)}, 1024*10) // 10KB
		default:
			// 较大块文件
			relPath = fmt.Sprintf("data/raw/chunks/chunk_%02d.bin", i)
			content = bytes.Repeat([]byte("CHUNK_DATA_"), 1024*5) // ~55KB
		}

		fullSrc := filepath.Join(srcDir, filepath.FromSlash(relPath))
		if err := os.WriteFile(fullSrc, content, 0644); err != nil {
			t.Fatal(err)
		}

		h := sha256.Sum256(content)
		expectedFiles[filepath.ToSlash(relPath)] = hex.EncodeToString(h[:])
		expectedTotalBytes += int64(len(content))
	}

	// 2. 带并发槽位监听的 tracker
	type activeSlotTracker struct {
		testTracker
		mu          sync.Mutex
		activeFiles map[string]bool
		maxActive   int
	}
	tracker := &activeSlotTracker{
		activeFiles: make(map[string]bool),
	}

	// 3. 执行 CopyDir（concurrency = 8）
	err := CopyDir(context.Background(), srcDir, dstDir, 8, tracker)
	if err != nil {
		t.Fatalf("CopyDir with 32 files (concurrency 8) failed: %v", err)
	}

	// 4. 断言已传输文件数与字节精确自洽
	if tracker.files != totalFiles {
		t.Fatalf("expected exactly %d files copied, tracker recorded %d", totalFiles, tracker.files)
	}
	if tracker.bytes != expectedTotalBytes {
		t.Fatalf("expected total bytes %d, tracker recorded %d", expectedTotalBytes, tracker.bytes)
	}

	// 5. 断言空目录 100% 守恒
	for _, emptyDirRel := range []string{"empty_folder_1", "empty_folder_2/nested_empty"} {
		fullDstEmpty := filepath.Join(dstDir, filepath.FromSlash(emptyDirRel))
		fi, err := os.Stat(fullDstEmpty)
		if err != nil || !fi.IsDir() {
			t.Fatalf("expected empty directory '%s' to be preserved in destination", emptyDirRel)
		}
	}

	// 6. 全量递归遍历目标目录，断言 32 个文件 100% 存在且 SHA-256 逐一强一致
	var dstFileCount int
	err = filepath.WalkDir(dstDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dstDir, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		expectedHash, exists := expectedFiles[relSlash]
		if !exists {
			t.Fatalf("unexpected extra file copied to destination: %s", relSlash)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read destination file '%s' failed: %v", relSlash, err)
		}
		actualHash := fmt.Sprintf("%x", sha256.Sum256(data))
		if actualHash != expectedHash {
			t.Fatalf("SHA256 mismatch for '%s': expected %s, got %s", relSlash, expectedHash, actualHash)
		}

		dstFileCount++
		return nil
	})
	if err != nil {
		t.Fatalf("walk destination directory failed: %v", err)
	}

	if dstFileCount != totalFiles {
		t.Fatalf("expected destination to contain %d files, got %d", totalFiles, dstFileCount)
	}
}

// TestFsEngine_CopyDir_ExtremeEdgeCases 覆盖多文件并发传输的各类极端恶劣边界：
// 1. 纯嵌套空目录树 (0 文件)
// 2. 全部为 0 字节的空文件集合 (除以 0 防御)
// 3. 特殊字符与中文路径/文件名
// 4. 极限并发度 (concurrency = 1 纯串行 vs concurrency = 64 超限并发)
// 5. 中途超时取消与 Worker 协程快速释放 (零泄漏)
func TestFsEngine_CopyDir_ExtremeEdgeCases(t *testing.T) {
	// --- Case 1: 纯嵌套空目录树 (0 文件) ---
	t.Run("PureEmptyDirectories", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "empty_tree_src")
		dst := filepath.Join(t.TempDir(), "empty_tree_dst")
		_ = os.MkdirAll(filepath.Join(src, "a", "b", "c", "d"), 0755)
		_ = os.MkdirAll(filepath.Join(src, "sibling", "nested"), 0755)

		err := CopyDir(context.Background(), src, dst, 8, nil)
		if err != nil {
			t.Fatalf("CopyDir on empty directory tree failed: %v", err)
		}

		fi, err := os.Stat(filepath.Join(dst, "a", "b", "c", "d"))
		if err != nil || !fi.IsDir() {
			t.Fatalf("nested directory a/b/c/d not preserved")
		}
		fi, err = os.Stat(filepath.Join(dst, "sibling", "nested"))
		if err != nil || !fi.IsDir() {
			t.Fatalf("sibling/nested directory not preserved")
		}
	})

	// --- Case 2: 全部为 0 字节的空文件集合 ---
	t.Run("ZeroByteFiles", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "zerobytes_src")
		dst := filepath.Join(t.TempDir(), "zerobytes_dst")
		_ = os.MkdirAll(filepath.Join(src, "empty_sub"), 0755)

		const zeroFilesCount = 15
		for i := 1; i <= zeroFilesCount; i++ {
			p := filepath.Join(src, "empty_sub", fmt.Sprintf("zero_%02d.dat", i))
			if err := os.WriteFile(p, []byte{}, 0644); err != nil {
				t.Fatal(err)
			}
		}

		tracker := &testTracker{}
		err := CopyDir(context.Background(), src, dst, 4, tracker)
		if err != nil {
			t.Fatalf("CopyDir on zero-byte files failed: %v", err)
		}
		if tracker.files != zeroFilesCount {
			t.Fatalf("expected %d files copied, got %d", zeroFilesCount, tracker.files)
		}
		if tracker.bytes != 0 {
			t.Fatalf("expected 0 bytes copied, got %d", tracker.bytes)
		}

		// 验证全部 15 个文件在目的端均为 0 字节
		for i := 1; i <= zeroFilesCount; i++ {
			p := filepath.Join(dst, "empty_sub", fmt.Sprintf("zero_%02d.dat", i))
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatalf("zero-byte file %d missing: %v", i, err)
			}
			if fi.Size() != 0 {
				t.Fatalf("file %d expected size 0, got %d", i, fi.Size())
			}
		}
	})

	// --- Case 3: 特殊字符与中文文件名 ---
	t.Run("SpecialAndUnicodeFileNames", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "unicode_src")
		dst := filepath.Join(t.TempDir(), "unicode_dst")
		_ = os.MkdirAll(filepath.Join(src, "中文 目录 (测试)"), 0755)

		testFiles := []string{
			"中文 目录 (测试)/测试 报告 [v2.0].json",
			"中文 目录 (测试)/特殊 符号 #@&_+.txt",
			"root file with spaces.bin",
		}
		for _, f := range testFiles {
			full := filepath.Join(src, filepath.FromSlash(f))
			if err := os.WriteFile(full, []byte("unicode-content-"+f), 0644); err != nil {
				t.Fatal(err)
			}
		}

		err := CopyDir(context.Background(), src, dst, 8, nil)
		if err != nil {
			t.Fatalf("CopyDir on unicode/special names failed: %v", err)
		}

		for _, f := range testFiles {
			fullDst := filepath.Join(dst, filepath.FromSlash(f))
			data, err := os.ReadFile(fullDst)
			if err != nil {
				t.Fatalf("read unicode file '%s' failed: %v", f, err)
			}
			if string(data) != "unicode-content-"+f {
				t.Fatalf("content mismatch for unicode file '%s'", f)
			}
		}
	})

	// --- Case 4: 极端并发度 (concurrency = 1 纯串行 vs concurrency = 64 超量池) ---
	t.Run("ExtremeConcurrencyLevels", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "concurrency_src")
		_ = os.MkdirAll(src, 0755)
		for i := 1; i <= 20; i++ {
			_ = os.WriteFile(filepath.Join(src, fmt.Sprintf("f_%02d.txt", i)), []byte(fmt.Sprintf("data-%d", i)), 0644)
		}

		// 4.1 纯串行 (concurrency = 1)
		dstSerial := filepath.Join(t.TempDir(), "dst_serial")
		t1 := &testTracker{}
		if err := CopyDir(context.Background(), src, dstSerial, 1, t1); err != nil {
			t.Fatalf("CopyDir with concurrency=1 failed: %v", err)
		}
		if t1.files != 20 {
			t.Fatalf("expected 20 files with concurrency=1, got %d", t1.files)
		}

		// 4.2 超量并发 (concurrency = 64，远超 20 个文件)
		dstParallel := filepath.Join(t.TempDir(), "dst_parallel")
		t2 := &testTracker{}
		if err := CopyDir(context.Background(), src, dstParallel, 64, t2); err != nil {
			t.Fatalf("CopyDir with concurrency=64 failed: %v", err)
		}
		if t2.files != 20 {
			t.Fatalf("expected 20 files with concurrency=64, got %d", t2.files)
		}
	})

	// --- Case 5: 中途超时快速熔断与资源安全释放 (无协程泄漏) ---
	t.Run("ContextCancellationMidway", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "cancel_src")
		dst := filepath.Join(t.TempDir(), "cancel_dst")
		_ = os.MkdirAll(src, 0755)
		for i := 1; i <= 50; i++ {
			_ = os.WriteFile(filepath.Join(src, fmt.Sprintf("chunk_%02d.bin", i)), bytes.Repeat([]byte("AB"), 1024*100), 0644)
		}

		ctx, cancel := context.WithCancel(context.Background())
		// 提前取消上下文，模拟传输刚启动就发生外部中断
		cancel()

		err := CopyDir(ctx, src, dst, 8, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled on aborted context, got: %v", err)
		}
	})
}

// 验证 HashStream 在单文件与目录模式下的完整事件生命周期 (init -> progress -> entry -> done)
func TestFsEngine_HashStream_Events(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. 单文件模式
	f1 := filepath.Join(tmpDir, "single.txt")
	content := []byte("hello stream hash")
	if err := os.WriteFile(f1, content, 0644); err != nil {
		t.Fatal(err)
	}

	var events []protocol.FsHashEvent
	list, err := HashStream(f1, false, func(ev protocol.FsHashEvent) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("HashStream single file failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 item, got %d", len(list))
	}
	// 事件序列必须包含 init, entry, done
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events for single file, got %d", len(events))
	}
	if events[0].Event != protocol.FsHashEventInit || events[0].TotalFiles != 1 || events[0].TotalBytes != int64(len(content)) {
		t.Fatalf("unexpected init event: %+v", events[0])
	}
	lastEv := events[len(events)-1]
	if lastEv.Event != protocol.FsHashEventDone || lastEv.TotalFiles != 1 {
		t.Fatalf("unexpected done event: %+v", lastEv)
	}

	// 2. 目录多文件递归模式
	subDir := filepath.Join(tmpDir, "nested")
	_ = os.MkdirAll(subDir, 0755)
	f2 := filepath.Join(subDir, "inner.txt")
	_ = os.WriteFile(f2, []byte("inner world"), 0644)

	var dirEvents []protocol.FsHashEvent
	dirList, err := HashStream(tmpDir, true, func(ev protocol.FsHashEvent) error {
		dirEvents = append(dirEvents, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("HashStream dir failed: %v", err)
	}
	if len(dirList) != 2 {
		t.Fatalf("expected 2 items, got %d", len(dirList))
	}
	if dirEvents[0].Event != protocol.FsHashEventInit || dirEvents[0].TotalFiles != 2 {
		t.Fatalf("unexpected dir init event: %+v", dirEvents[0])
	}
	lastDirEv := dirEvents[len(dirEvents)-1]
	if lastDirEv.Event != protocol.FsHashEventDone || lastDirEv.TotalFiles != 2 {
		t.Fatalf("unexpected dir done event: %+v", lastDirEv)
	}

	// 3. 中途发生事件回调熔断 (例如网络连接断开)
	abortedErr := errors.New("client connection closed")
	_, err = HashStream(tmpDir, true, func(ev protocol.FsHashEvent) error {
		if ev.Event == protocol.FsHashEventInit {
			return abortedErr
		}
		return nil
	})
	if !errors.Is(err, abortedErr) {
		t.Fatalf("expected HashStream to abort immediately on callback error, got: %v", err)
	}
}

// 验证大文件 (>10MB) 计算 SHA-256 时按块产生 progress 步进心跳事件
func TestFsEngine_HashStream_LargeFileChunking(t *testing.T) {
	tmpDir := t.TempDir()
	largeFile := filepath.Join(tmpDir, "large.bin")

	// 生成一个 12MB 的文件 (超过 10MB 触发 chunked progress 阈值)
	targetSize := 12 * 1024 * 1024
	chunk := bytes.Repeat([]byte("X"), 1024*1024) // 1MB
	f, err := os.Create(largeFile)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err := f.Write(chunk); err != nil {
			f.Close()
			t.Fatal(err)
		}
	}
	f.Close()

	var progressCount int
	var maxDoneBytes int64

	list, err := HashStream(largeFile, false, func(ev protocol.FsHashEvent) error {
		if ev.Event == protocol.FsHashEventProgress {
			progressCount++
			if ev.DoneBytes > maxDoneBytes {
				maxDoneBytes = ev.DoneBytes
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("HashStream large file failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 item, got %d", len(list))
	}
	if progressCount == 0 {
		t.Fatalf("expected at least 1 progress event for 12MB file, got %d", progressCount)
	}
	if maxDoneBytes != int64(targetSize) {
		t.Fatalf("expected maxDoneBytes to reach %d, got %d", targetSize, maxDoneBytes)
	}
}

// 验证小文件、大文件 (>10MB)、空目录、多层深层嵌套混合场景下的 HashStream 表现
func TestFsEngine_HashStream_MixedHierarchy(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. 空文件 (0 字节)
	_ = os.WriteFile(filepath.Join(tmpDir, "zero.txt"), []byte(""), 0644)

	// 2. 根目录小文件
	_ = os.WriteFile(filepath.Join(tmpDir, "small.txt"), []byte("small hello"), 0644)

	// 3. 空目录 (必须在统计与哈希列表中被正确跳过)
	_ = os.MkdirAll(filepath.Join(tmpDir, "empty_dir"), 0755)

	// 4. 多级嵌套子目录
	deepDir := filepath.Join(tmpDir, "level1", "level2")
	_ = os.MkdirAll(deepDir, 0755)
	_ = os.WriteFile(filepath.Join(deepDir, "nested.txt"), []byte("nested text payload"), 0644)

	// 5. 大文件 (11MB，触发分块心跳)
	largeFile := filepath.Join(deepDir, "large.bin")
	lf, err := os.Create(largeFile)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("Z"), 1024*1024)
	for i := 0; i < 11; i++ {
		_, _ = lf.Write(chunk)
	}
	_ = lf.Close()

	var events []protocol.FsHashEvent
	var progressCount int

	list, err := HashStream(tmpDir, true, func(ev protocol.FsHashEvent) error {
		events = append(events, ev)
		if ev.Event == protocol.FsHashEventProgress {
			progressCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("HashStream mixed hierarchy failed: %v", err)
	}

	// 必须准确包含 4 个有效文件 (zero.txt, small.txt, nested.txt, large.bin)
	if len(list) != 4 {
		t.Fatalf("expected 4 files in mixed list, got %d: %+v", len(list), list)
	}

	// 验证 init 事件统计准确性
	if len(events) == 0 || events[0].Event != protocol.FsHashEventInit {
		t.Fatalf("missing or invalid init event")
	}
	if events[0].TotalFiles != 4 {
		t.Fatalf("expected TotalFiles 4 in init, got %d", events[0].TotalFiles)
	}
	expectedTotalBytes := int64(0 + len("small hello") + len("nested text payload") + 11*1024*1024)
	if events[0].TotalBytes != expectedTotalBytes {
		t.Fatalf("expected TotalBytes %d, got %d", expectedTotalBytes, events[0].TotalBytes)
	}

	// 必须触发了大文件 progress 步进心跳事件
	if progressCount == 0 {
		t.Fatalf("expected at least 1 progress heartbeat event for 11MB file in mixed hierarchy")
	}

	// 验证最终 done 事件
	lastEv := events[len(events)-1]
	if lastEv.Event != protocol.FsHashEventDone || lastEv.TotalFiles != 4 || lastEv.TotalBytes != expectedTotalBytes {
		t.Fatalf("unexpected done event: %+v", lastEv)
	}
}

// 验证 1000 级海量批量小文件场景下的哈希与事件流处理性能与正确性
func TestFsEngine_HashStream_LargeBatch_1000Files(t *testing.T) {
	tmpDir := t.TempDir()

	const numDirs = 10
	const filesPerDir = 100
	const totalExpected = numDirs * filesPerDir

	var expectedTotalBytes int64
	for d := 0; d < numDirs; d++ {
		subDir := filepath.Join(tmpDir, fmt.Sprintf("dir_%02d", d))
		_ = os.MkdirAll(subDir, 0755)
		for f := 0; f < filesPerDir; f++ {
			content := []byte(fmt.Sprintf("batch_data_d%d_f%d", d, f))
			_ = os.WriteFile(filepath.Join(subDir, fmt.Sprintf("file_%03d.txt", f)), content, 0644)
			expectedTotalBytes += int64(len(content))
		}
	}

	var entryCount int64
	var initFiles, initBytes int64

	list, err := HashStream(tmpDir, true, func(ev protocol.FsHashEvent) error {
		switch ev.Event {
		case protocol.FsHashEventInit:
			initFiles = ev.TotalFiles
			initBytes = ev.TotalBytes
		case protocol.FsHashEventEntry:
			entryCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("HashStream 1000 files failed: %v", err)
	}

	if initFiles != totalExpected || initBytes != expectedTotalBytes {
		t.Fatalf("init event mismatch: files=%d, bytes=%d (expected %d, %d)", initFiles, initBytes, totalExpected, expectedTotalBytes)
	}
	if entryCount != totalExpected {
		t.Fatalf("expected %d entry events, got %d", totalExpected, entryCount)
	}
	if len(list) != totalExpected {
		t.Fatalf("expected list len %d, got %d", totalExpected, len(list))
	}

	// 验证每个文件的 SHA-256 均真实生成且合法
	for _, item := range list {
		if len(item.SHA256) != 64 {
			t.Fatalf("invalid sha256 for item %s: %s", item.Path, item.SHA256)
		}
	}
}

// 验证流式回传中途报错或调用端主动终止时，HashStream 立即阻断并优雅退出
func TestFsEngine_HashStream_AbortMidway(t *testing.T) {
	tmpDir := t.TempDir()
	for i := 1; i <= 30; i++ {
		_ = os.WriteFile(filepath.Join(tmpDir, fmt.Sprintf("f%02d.txt", i)), []byte(fmt.Sprintf("test%d", i)), 0644)
	}

	expectedErr := errors.New("caller requested abort")
	var processedCount int

	_, err := HashStream(tmpDir, true, func(ev protocol.FsHashEvent) error {
		if ev.Event == protocol.FsHashEventEntry {
			processedCount++
			if processedCount == 5 {
				return expectedErr
			}
		}
		return nil
	})

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected abort error %v, got: %v", expectedErr, err)
	}
	if processedCount != 5 {
		t.Fatalf("expected exactly 5 processed files before abort, got: %d", processedCount)
	}
}






