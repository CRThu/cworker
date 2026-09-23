package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cworker/pkg/pathutil"
)

// ErrDirectoryWithoutRecursive 当传输源为目录但未声明递归 (-r) 标志时返回该强类型契约错误
type ErrDirectoryWithoutRecursive struct {
	Node string
	Path string
}

func (e *ErrDirectoryWithoutRecursive) Error() string {
	if e.Node != "" {
		return fmt.Sprintf("omitting directory '%s:%s' (use -r to copy recursively)", e.Node, e.Path)
	}
	return fmt.Sprintf("omitting directory '%s' (use -r to copy recursively)", e.Path)
}

// TransferOptions 封装单向/双向/跨机/本地中继文件与目录传输的完整参数契约
type TransferOptions struct {
	SrcNode     string
	SrcPath     string
	DstNode     string
	DstPath     string
	Recursive   bool
	Concurrency int
}

// Transfer 执行跨机或本地文件/目录传输（单一调度内核权威实现）
// 自动分流四大象限（本地<->远端、远端<->远端、本地<->本地），并单点统领目录探测、-r 递归拦截、目标端子目录嵌套与统一进度监听。
func (c *Client) Transfer(ctx context.Context, opts TransferOptions, tracker *ProgressTracker) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(opts.SrcPath) == "" || strings.TrimSpace(opts.DstPath) == "" {
		return errors.New("source and destination paths cannot be empty")
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 8
	}

	srcNode := strings.TrimSpace(opts.SrcNode)
	dstNode := strings.TrimSpace(opts.DstNode)
	srcPath := strings.TrimSpace(opts.SrcPath)
	dstPath := strings.TrimSpace(opts.DstPath)

	// 1. 远端到远端中继传输
	if srcNode != "" && dstNode != "" {
		_, lsErr := c.ListDirWithContext(ctx, srcNode, srcPath)
		isSrcDir := lsErr == nil

		if isSrcDir {
			if !opts.Recursive {
				return &ErrDirectoryWithoutRecursive{Node: srcNode, Path: srcPath}
			}

			// 若目的端已存在且为目录，自动放入子目录中 (对齐 Unix cp -r 规范)
			if _, err := c.ListDirWithContext(ctx, dstNode, dstPath); err == nil {
				dstPath = pathutil.JoinRemotePath(dstPath, pathutil.SafeBaseName(srcPath))
			}

			return c.RelayCopyDir(ctx, srcNode, srcPath, dstNode, dstPath, concurrency, tracker)
		}

		// 单文件远端到远端中继拷贝
		if _, err := c.ListDirWithContext(ctx, dstNode, dstPath); err == nil {
			dstPath = pathutil.JoinRemotePath(dstPath, pathutil.SafeBaseName(srcPath))
		}

		if tracker != nil {
			tracker.SetTotals(1, 0)
			tracker.StartFile(pathutil.SafeBaseName(srcPath))
		}
		err := c.RelayCopyWithContext(ctx, srcNode, srcPath, dstNode, dstPath, tracker)
		if tracker != nil {
			tracker.EndFile(pathutil.SafeBaseName(srcPath))
			if err == nil {
				tracker.AddFile()
				tracker.Finish()
			}
		}
		return err
	}

	// 2. 本地到远端上传
	if srcNode == "" && dstNode != "" {
		cleanLocalSrc, err := pathutil.NormalizeLocalPath(srcPath)
		if err != nil {
			return err
		}

		fi, err := os.Stat(cleanLocalSrc)
		if err != nil {
			return fmt.Errorf("open local path failed: %w", err)
		}

		if fi.IsDir() {
			if !opts.Recursive {
				return &ErrDirectoryWithoutRecursive{Node: "", Path: srcPath}
			}

			// 若目的端已存在且为目录，自动放入子目录中
			if _, err := c.ListDirWithContext(ctx, dstNode, dstPath); err == nil {
				dstPath = pathutil.JoinRemotePath(dstPath, pathutil.SafeBaseName(cleanLocalSrc))
			}

			return c.UploadDir(ctx, dstNode, dstPath, cleanLocalSrc, concurrency, tracker)
		}

		// 单文件上传
		if _, err := c.ListDirWithContext(ctx, dstNode, dstPath); err == nil {
			dstPath = pathutil.JoinRemotePath(dstPath, pathutil.SafeBaseName(cleanLocalSrc))
		}

		f, err := os.Open(cleanLocalSrc)
		if err != nil {
			return fmt.Errorf("open local file failed: %w", err)
		}
		defer f.Close()

		if tracker != nil {
			tracker.SetTotals(1, fi.Size())
			tracker.StartFile(pathutil.SafeBaseName(cleanLocalSrc))
		}

		var r io.Reader = f
		if tracker != nil {
			r = NewCountingReader(f, tracker)
		}

		err = c.UploadFileWithContext(ctx, dstNode, dstPath, r)
		if tracker != nil {
			tracker.EndFile(pathutil.SafeBaseName(cleanLocalSrc))
			if err == nil {
				tracker.AddFile()
				tracker.Finish()
			}
		}
		return err
	}

	// 3. 远端到本地下载
	if srcNode != "" && dstNode == "" {
		cleanLocalDst, err := pathutil.NormalizeLocalPath(dstPath)
		if err != nil {
			return err
		}

		// 探测源端是否为目录
		_, lsErr := c.ListDirWithContext(ctx, srcNode, srcPath)
		isSrcDir := lsErr == nil

		if isSrcDir {
			if !opts.Recursive {
				return &ErrDirectoryWithoutRecursive{Node: srcNode, Path: srcPath}
			}

			// 若本地目标已存在且为目录，放入子目录中
			if fi, err := os.Stat(cleanLocalDst); err == nil && fi.IsDir() {
				cleanLocalDst = filepath.Join(cleanLocalDst, pathutil.SafeBaseName(srcPath))
			}

			return c.DownloadDir(ctx, srcNode, srcPath, cleanLocalDst, concurrency, tracker)
		}

		// 单文件下载
		if fi, err := os.Stat(cleanLocalDst); err == nil && fi.IsDir() {
			cleanLocalDst = filepath.Join(cleanLocalDst, pathutil.SafeBaseName(srcPath))
		}

		if tracker != nil {
			tracker.SetTotals(1, 0)
			tracker.StartFile(pathutil.SafeBaseName(srcPath))
		}

		err = c.DownloadToLocalFile(ctx, srcNode, srcPath, cleanLocalDst, tracker)
		if tracker != nil {
			tracker.EndFile(pathutil.SafeBaseName(srcPath))
			if err == nil {
				tracker.AddFile()
				tracker.Finish()
			}
		}
		return err
	}

	// 4. 本地到本地拷贝
	cleanLocalSrc, err := pathutil.NormalizeLocalPath(srcPath)
	if err != nil {
		return err
	}
	cleanLocalDst, err := pathutil.NormalizeLocalPath(dstPath)
	if err != nil {
		return err
	}

	fi, err := os.Stat(cleanLocalSrc)
	if err != nil {
		return fmt.Errorf("open local source path failed: %w", err)
	}

	if fi.IsDir() {
		if !opts.Recursive {
			return &ErrDirectoryWithoutRecursive{Node: "", Path: srcPath}
		}

		if dstFi, err := os.Stat(cleanLocalDst); err == nil && dstFi.IsDir() {
			cleanLocalDst = filepath.Join(cleanLocalDst, pathutil.SafeBaseName(cleanLocalSrc))
		}

		return c.LocalCopyDir(ctx, cleanLocalSrc, cleanLocalDst, concurrency, tracker)
	}

	// 单文件本地拷贝
	if dstFi, err := os.Stat(cleanLocalDst); err == nil && dstFi.IsDir() {
		cleanLocalDst = filepath.Join(cleanLocalDst, pathutil.SafeBaseName(cleanLocalSrc))
	}

	if tracker != nil {
		tracker.SetTotals(1, fi.Size())
		tracker.StartFile(pathutil.SafeBaseName(cleanLocalSrc))
	}

	err = c.LocalCopyFileWithContext(ctx, cleanLocalSrc, cleanLocalDst, tracker)
	if tracker != nil {
		tracker.EndFile(pathutil.SafeBaseName(cleanLocalSrc))
		if err == nil {
			tracker.AddFile()
			tracker.Finish()
		}
	}
	return err
}
