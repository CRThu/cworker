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

// HashDir 递归遍历目录，计算每个非符号链接文件的 SHA-256 并返回清单 (SSOT)
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

	var list []protocol.FileInfo
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

		rel, err := filepath.Rel(cleanPath, path)
		if err != nil {
			return err
		}

		hash, err := HashOnly(path)
		if err != nil {
			return fmt.Errorf("hash file %s failed: %w", path, err)
		}

		list = append(list, protocol.FileInfo{
			Name:    d.Name(),
			Path:    filepath.ToSlash(rel),
			IsDir:   false,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			SHA256:  hash,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	if list == nil {
		list = []protocol.FileInfo{}
	}
	return list, nil
}

// Hash 统一物理路径哈希计算门面 (单文件返回切片含 1 条，目录未带 -r 显式返回 ErrDirRequiresRecursive)
func Hash(path string, recursive bool) ([]protocol.FileInfo, error) {
	cleanPath, err := pathutil.NormalizeLocalPath(path)
	if err != nil {
		return nil, err
	}

	fi, err := os.Stat(cleanPath)
	if err != nil {
		return nil, err
	}

	if fi.IsDir() {
		if !recursive {
			return nil, ErrDirRequiresRecursive
		}
		return HashDir(cleanPath)
	}

	info, err := HashFile(cleanPath)
	if err != nil {
		return nil, err
	}
	return []protocol.FileInfo{info}, nil
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

// Remove 安全删除文件或目录 (非空目录必须显式指定 recursive)
func Remove(targetPath string, recursive bool) error {
	cleanPath, err := pathutil.NormalizeLocalPath(targetPath)
	if err != nil {
		return err
	}

	fi, err := os.Stat(cleanPath)
	if err != nil {
		return err
	}

	if fi.IsDir() {
		if !recursive {
			entries, err := os.ReadDir(cleanPath)
			if err != nil {
				return err
			}
			if len(entries) > 0 {
				return ErrNonEmptyDirRequiresRecursive
			}
			return os.Remove(cleanPath)
		}
		return os.RemoveAll(cleanPath)
	}

	return os.Remove(cleanPath)
}

// SaveStream 将输入流原子落盘至指定目标文件，校验 SHA-256，并在出错时物理回滚清理临时文件
func SaveStream(dstPath string, r io.Reader, expectedSha256 string) (string, error) {
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
	if expectedSha256 != "" && !strings.EqualFold(expectedSha256, computedHash) {
		return "", fmt.Errorf("%w: expected %s, got %s", ErrHashMismatch, expectedSha256, computedHash)
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
