package fsengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cworker/pkg/pathutil"
	"cworker/pkg/protocol"
)

var (
	// ErrDirRequiresRecursive 目录未携带递归标志错误
	ErrDirRequiresRecursive = errors.New("path is a directory, requires recursive flag (-r)")
	// ErrNonEmptyDirRequiresRecursive 非空目录未携带递归标志错误
	ErrNonEmptyDirRequiresRecursive = errors.New("path is a non-empty directory, requires recursive flag (-r)")
	// ErrPathIsFile 路径为文件而非目录
	ErrPathIsFile = errors.New("path is a file, not a directory")
	// ErrDestinationIsDir 目标路径是已有目录错误
	ErrDestinationIsDir = errors.New("destination path is an existing directory")
	// ErrHashMismatch 散列校验不匹配错误
	ErrHashMismatch = errors.New("sha256 mismatch")
)

// ProgressListener 进度监听器接口 (解耦具体进度条实现，消除循环依赖)
type ProgressListener interface {
	AddBytes(int64)
	AddFile()
}

type countingReader struct {
	r       io.Reader
	tracker ProgressListener
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	if n > 0 && cr.tracker != nil {
		cr.tracker.AddBytes(int64(n))
	}
	return n, err
}

// HashFile 计算单文件的 SHA-256 并返回规范化 FileInfo
func HashFile(filePath string) (protocol.FileInfo, error) {
	cleanPath, err := pathutil.NormalizeLocalPath(filePath)
	if err != nil {
		return protocol.FileInfo{}, err
	}

	fi, err := os.Stat(cleanPath)
	if err != nil {
		return protocol.FileInfo{}, err
	}
	if fi.IsDir() {
		return protocol.FileInfo{}, ErrDirRequiresRecursive
	}

	hash, err := HashOnly(cleanPath)
	if err != nil {
		return protocol.FileInfo{}, err
	}

	return protocol.FileInfo{
		Name:    filepath.Base(cleanPath),
		Path:    filepath.Base(cleanPath),
		IsDir:   false,
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
		SHA256:  hash,
	}, nil
}

// HashOnly 仅计算物理文件的 SHA-256 十六进制串
func HashOnly(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// hashFileWithProgress 计算物理文件 SHA-256，大文件 (>10MB) 按块分步触发心跳回调
func hashFileWithProgress(filePath string, totalSize int64, onProgress func(doneBytes int64) error) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if totalSize <= 10*1024*1024 || onProgress == nil {
		if _, err := io.Copy(hasher, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(hasher.Sum(nil)), nil
	}

	buf := make([]byte, 4*1024*1024) // 4MB 块流式读取
	var doneBytes int64
	lastProgress := time.Now()

	for {
		n, rErr := f.Read(buf)
		if n > 0 {
			hasher.Write(buf[:n])
			doneBytes += int64(n)
			now := time.Now()
			// 节流步进心跳：每 100ms 或达到文件末尾时上报一次 (与服务端网络 Flush 及终端 UI 渲染节拍对齐)
			if now.Sub(lastProgress) >= 100*time.Millisecond || doneBytes == totalSize {
				if err := onProgress(doneBytes); err != nil {
					return "", err
				}
				lastProgress = now
			}
		}
		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			return "", rErr
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// HashStream 统一物理路径哈希计算内核 (支持大文件心跳与目录分步流式回传, SSOT)
func HashStream(path string, recursive bool, onEvent func(protocol.FsHashEvent) error) ([]protocol.FileInfo, error) {
	cleanPath, err := pathutil.NormalizeLocalPath(path)
	if err != nil {
		return nil, err
	}

	fi, err := os.Stat(cleanPath)
	if err != nil {
		return nil, err
	}

	// 1. 单文件模式
	if !fi.IsDir() {
		if onEvent != nil {
			if err := onEvent(protocol.FsHashEvent{
				Event:      protocol.FsHashEventInit,
				TotalFiles: 1,
				TotalBytes: fi.Size(),
			}); err != nil {
				return nil, err
			}
		}

		hash, err := hashFileWithProgress(cleanPath, fi.Size(), func(doneBytes int64) error {
			if onEvent == nil {
				return nil
			}
			return onEvent(protocol.FsHashEvent{
				Event:       protocol.FsHashEventProgress,
				CurrentFile: fi.Name(),
				DoneBytes:   doneBytes,
				TotalBytes:  fi.Size(),
			})
		})
		if err != nil {
			return nil, err
		}

		entry := protocol.FileInfo{
			Name:    fi.Name(),
			Path:    filepath.ToSlash(fi.Name()),
			IsDir:   false,
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
			SHA256:  hash,
		}

		if onEvent != nil {
			if err := onEvent(protocol.FsHashEvent{
				Event: protocol.FsHashEventEntry,
				Entry: &entry,
			}); err != nil {
				return nil, err
			}
			if err := onEvent(protocol.FsHashEvent{
				Event:      protocol.FsHashEventDone,
				TotalFiles: 1,
				TotalBytes: fi.Size(),
			}); err != nil {
				return nil, err
			}
		}

		return []protocol.FileInfo{entry}, nil
	}

	// 2. 目录模式
	if !recursive {
		return nil, ErrDirRequiresRecursive
	}

	type scanItem struct {
		absPath string
		relPath string
		name    string
		size    int64
		modTime time.Time
	}

	// 阶段一：轻量元数据扫描，快速获取总文件数与总字节数
	var items []scanItem
	var totalBytes int64

	err = filepath.WalkDir(cleanPath, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == cleanPath {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		// 严密防御符号链接循环与跳逸
		if info.Mode()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(cleanPath, p)
		if err != nil {
			return err
		}

		items = append(items, scanItem{
			absPath: p,
			relPath: filepath.ToSlash(rel),
			name:    d.Name(),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		totalBytes += info.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}

	totalFiles := int64(len(items))

	if onEvent != nil {
		if err := onEvent(protocol.FsHashEvent{
			Event:      protocol.FsHashEventInit,
			TotalFiles: totalFiles,
			TotalBytes: totalBytes,
		}); err != nil {
			return nil, err
		}
	}

	// 阶段二：逐个计算文件 SHA-256 并实时流式回传
	list := make([]protocol.FileInfo, 0, len(items))
	for _, item := range items {
		hash, err := hashFileWithProgress(item.absPath, item.size, func(doneBytes int64) error {
			if onEvent == nil {
				return nil
			}
			return onEvent(protocol.FsHashEvent{
				Event:       protocol.FsHashEventProgress,
				CurrentFile: item.relPath,
				DoneBytes:   doneBytes,
				TotalBytes:  item.size,
			})
		})
		if err != nil {
			return nil, fmt.Errorf("hash file %s failed: %w", item.absPath, err)
		}

		entry := protocol.FileInfo{
			Name:    item.name,
			Path:    item.relPath,
			IsDir:   false,
			Size:    item.size,
			ModTime: item.modTime,
			SHA256:  hash,
		}
		list = append(list, entry)

		if onEvent != nil {
			if err := onEvent(protocol.FsHashEvent{
				Event: protocol.FsHashEventEntry,
				Entry: &entry,
			}); err != nil {
				return nil, err
			}
		}
	}

	if onEvent != nil {
		if err := onEvent(protocol.FsHashEvent{
			Event:      protocol.FsHashEventDone,
			TotalFiles: totalFiles,
			TotalBytes: totalBytes,
		}); err != nil {
			return nil, err
		}
	}

	return list, nil
}

// HashDir 递归遍历目录，计算每个非符号链接文件的 SHA-256 并返回清单 (委托给 HashStream, SSOT)
func HashDir(dirPath string) ([]protocol.FileInfo, error) {
	cleanPath, err := pathutil.NormalizeLocalPath(dirPath)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(cleanPath)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, ErrPathIsFile
	}
	return HashStream(cleanPath, true, nil)
}

// Hash 统一物理路径哈希计算门面 (单文件返回切片含 1 条，目录未带 -r 显式返回 ErrDirRequiresRecursive)
func Hash(path string, recursive bool) ([]protocol.FileInfo, error) {
	return HashStream(path, recursive, nil)
}


// ListDir 列出目录条目 (支持单层平铺或递归完整树扫描)
func ListDir(dirPath string, recursive bool) ([]protocol.FileInfo, error) {
	cleanPath, err := pathutil.NormalizeLocalPath(dirPath)
	if err != nil {
		return nil, err
	}

	fi, err := os.Stat(cleanPath)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, ErrPathIsFile
	}

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
			return nil, err
		}
	} else {
		entries, err := os.ReadDir(cleanPath)
		if err != nil {
			return nil, err
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

	if list == nil {
		list = []protocol.FileInfo{}
	}
	return list, nil
}

// MakeDir 递归创建目录 (带自动创建父级目录特性)
func MakeDir(dirPath string) error {
	cleanPath, err := pathutil.NormalizeLocalPath(dirPath)
	if err != nil {
		return err
	}
	return os.MkdirAll(cleanPath, 0755)
}

// FsRmProgressFunc 进度回调函数，用于流式报告已删除项数
type FsRmProgressFunc func(removedCount int64)

func removeSingleItem(path string) error {
	err := os.Remove(path)
	if err != nil && os.IsPermission(err) {
		_ = os.Chmod(path, 0666)
		err = os.Remove(path)
	}
	return err
}

func removeDirTree(ctx context.Context, dirPath string, count *int64) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	f, err := os.Open(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var readErr error
	for {
		if ctx != nil && ctx.Err() != nil {
			_ = f.Close()
			return ctx.Err()
		}

		entries, rErr := f.ReadDir(1024)
		if rErr != nil {
			if rErr != io.EOF {
				readErr = rErr
			}
			break
		}

		for _, entry := range entries {
			childPath := filepath.Join(dirPath, entry.Name())

			// 若为符号链接或 Windows Junction，不可递归深入其目标，直接解除链接
			if entry.Type()&os.ModeSymlink != 0 {
				if err := removeSingleItem(childPath); err != nil && !os.IsNotExist(err) {
					_ = f.Close()
					return err
				}
				atomic.AddInt64(count, 1)
				continue
			}

			if entry.IsDir() {
				if err := removeDirTree(ctx, childPath, count); err != nil {
					_ = f.Close()
					return err
				}
			} else {
				if err := removeSingleItem(childPath); err != nil && !os.IsNotExist(err) {
					_ = f.Close()
					return err
				}
				atomic.AddInt64(count, 1)
			}
		}
	}
	_ = f.Close()

	if readErr != nil {
		return readErr
	}

	// 删除当前目录本身
	if err := removeSingleItem(dirPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	atomic.AddInt64(count, 1)
	return nil
}

// Remove 安全删除文件或目录 (非空目录必须显式指定 recursive, 统一委托给 RemoveWithProgress, SSOT)
func Remove(targetPath string, recursive bool) error {
	_, err := RemoveWithProgress(context.Background(), targetPath, recursive, nil)
	return err
}

// RemoveWithProgress 安全删除文件或目录并支持流式项数统计与上下文中断 (SSOT)
func RemoveWithProgress(ctx context.Context, targetPath string, recursive bool, onProgress FsRmProgressFunc) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cleanPath, err := pathutil.NormalizeLocalPath(targetPath)
	if err != nil {
		return 0, err
	}

	fi, err := os.Lstat(cleanPath)
	if err != nil {
		return 0, err
	}

	// 1. 单文件或符号链接
	if !fi.IsDir() {
		if err := removeSingleItem(cleanPath); err != nil {
			return 0, err
		}
		if onProgress != nil {
			onProgress(1)
		}
		return 1, nil
	}

	// 2. 目录但未指定递归
	if !recursive {
		f, err := os.Open(cleanPath)
		if err != nil {
			return 0, err
		}
		entries, err := f.ReadDir(1)
		_ = f.Close()
		if err != nil && err != io.EOF {
			return 0, err
		}
		if len(entries) > 0 {
			return 0, ErrNonEmptyDirRequiresRecursive
		}
		if err := removeSingleItem(cleanPath); err != nil {
			return 0, err
		}
		if onProgress != nil {
			onProgress(1)
		}
		return 1, nil
	}

	// 3. 递归删除目录树 (DFS 单遍遍历即删，零预扫，节流汇报)
	var count int64
	var stopTicker chan struct{}
	if onProgress != nil {
		stopTicker = make(chan struct{})
		defer func() {
			close(stopTicker)
		}()
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			var lastReported int64
			for {
				select {
				case <-stopTicker:
					return
				case <-ticker.C:
					cur := atomic.LoadInt64(&count)
					if cur != lastReported {
						lastReported = cur
						onProgress(cur)
					}
				}
			}
		}()
	}

	err = removeDirTree(ctx, cleanPath, &count)
	finalCount := atomic.LoadInt64(&count)
	if onProgress != nil {
		onProgress(finalCount)
	}
	return finalCount, err
}


// SaveStreamWithValidator 将输入流原子落盘至指定目标文件，并在原子替换提交前执行校验回调 (SSOT)
func SaveStreamWithValidator(dstPath string, r io.Reader, validator func(computedHash string) error) (string, error) {
	cleanPath, err := pathutil.NormalizeLocalPath(dstPath)
	if err != nil {
		return "", err
	}

	parentDir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return "", fmt.Errorf("create parent dir failed: %w", err)
	}

	if fi, err := os.Stat(cleanPath); err == nil && fi.IsDir() {
		return "", ErrDestinationIsDir
	}

	// 临时文件原子写入
	tmpPath := filepath.Join(parentDir, fmt.Sprintf(".%s.cwsave-%d", filepath.Base(cleanPath), time.Now().UnixNano()))
	destFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("create temp file failed: %w", err)
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

	if _, err := io.Copy(destWriter, r); err != nil {
		return "", fmt.Errorf("write file failed: %w", err)
	}
	if err := destFile.Close(); err != nil {
		return "", fmt.Errorf("flush dest file failed: %w", err)
	}

	computedHash := hex.EncodeToString(hasher.Sum(nil))
	if validator != nil {
		if err := validator(computedHash); err != nil {
			return "", err
		}
	}

	// 原子替换
	if err := os.Rename(tmpPath, cleanPath); err != nil {
		_ = os.Remove(cleanPath)
		if err2 := os.Rename(tmpPath, cleanPath); err2 != nil {
			return "", fmt.Errorf("commit dest file failed: %w", err2)
		}
	}
	committed = true
	return computedHash, nil
}

// SaveStream 将输入流原子落盘至指定目标文件，校验 SHA-256，并在出错时物理回滚清理临时文件
func SaveStream(dstPath string, r io.Reader, expectedSha256 string) (string, error) {
	return SaveStreamWithValidator(dstPath, r, func(computedHash string) error {
		if expectedSha256 != "" && !strings.EqualFold(expectedSha256, computedHash) {
			return fmt.Errorf("%w: expected %s, got %s", ErrHashMismatch, expectedSha256, computedHash)
		}
		return nil
	})
}

// CopyFile 本地单文件安全原子拷贝 (计算 SHA-256 校验并支持进度追踪)
func CopyFile(srcPath, dstPath string, tracker ProgressListener) error {
	cleanSrc, err := pathutil.NormalizeLocalPath(srcPath)
	if err != nil {
		return err
	}
	cleanDst, err := pathutil.NormalizeLocalPath(dstPath)
	if err != nil {
		return err
	}

	srcFile, err := os.Open(cleanSrc)
	if err != nil {
		return fmt.Errorf("open source file failed: %w", err)
	}
	defer srcFile.Close()

	fi, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat source file failed: %w", err)
	}
	if fi.IsDir() {
		return ErrDirRequiresRecursive
	}

	var r io.Reader = srcFile
	if tracker != nil {
		r = &countingReader{r: srcFile, tracker: tracker}
	}

	_, err = SaveStream(cleanDst, r, "")
	return err
}

// CopyDir 本地目录并发递归拷贝 (空目录守恒)
func CopyDir(ctx context.Context, srcBaseDir, dstBaseDir string, concurrency int, tracker ProgressListener) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanSrc, err := pathutil.NormalizeLocalPath(srcBaseDir)
	if err != nil {
		return err
	}
	cleanDst, err := pathutil.NormalizeLocalPath(dstBaseDir)
	if err != nil {
		return err
	}

	srcFi, err := os.Stat(cleanSrc)
	if err != nil {
		return err
	}
	if !srcFi.IsDir() {
		return ErrPathIsFile
	}

	type localEntry struct {
		relPath string
		size    int64
	}
	var dirs []string
	var files []localEntry

	err = filepath.WalkDir(cleanSrc, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == cleanSrc {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(cleanSrc, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if d.IsDir() {
			dirs = append(dirs, relSlash)
		} else {
			files = append(files, localEntry{
				relPath: relSlash,
				size:    info.Size(),
			})
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk local source dir failed: %w", err)
	}

	var totalBytes int64
	for _, f := range files {
		totalBytes += f.size
	}
	if st, ok := tracker.(interface{ SetTotals(int64, int64) }); ok && st != nil {
		st.SetTotals(int64(len(files)), totalBytes)
	}

	// 先建目录骨架 (保证空目录守恒)
	if err := os.MkdirAll(cleanDst, 0755); err != nil {
		return fmt.Errorf("create destination dir '%s' failed: %w", cleanDst, err)
	}
	for _, d := range dirs {
		subDir := filepath.Join(cleanDst, filepath.FromSlash(d))
		if err := os.MkdirAll(subDir, 0755); err != nil {
			return fmt.Errorf("create local dir '%s' failed: %w", subDir, err)
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
	for _, fe := range files {
		select {
		case <-ctx.Done():
			break concurrencyLoop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(entry localEntry) {
			defer func() {
				<-sem
				wg.Done()
			}()

			select {
			case <-ctx.Done():
				return
			default:
			}

			srcF := filepath.Join(cleanSrc, filepath.FromSlash(entry.relPath))
			dstF := filepath.Join(cleanDst, filepath.FromSlash(entry.relPath))
			if st, ok := tracker.(interface{ StartFile(string); EndFile(string) }); ok && st != nil {
				st.StartFile(entry.relPath)
				defer st.EndFile(entry.relPath)
			}
			if err := CopyFile(srcF, dstF, tracker); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("copy '%s' failed: %w", entry.relPath, err)
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
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}
