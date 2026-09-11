package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"github.com/spf13/cobra"
)

var (
	cpRecursive   bool
	cpConcurrency int
)

var cpCmd = &cobra.Command{
	Use:   "cp [flags] <src> <dest>",
	Short: "跨机拷贝文件或目录 (支持 本地->远端、远端->本地、远端->远端 管道直连与并发传输)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		srcRaw := args[0]
		dstRaw := args[1]

		srcNode, srcPath := pathutil.ParseNodePath(srcRaw)
		dstNode, dstPath := pathutil.ParseNodePath(dstRaw)

		recursive, _ := cmd.Flags().GetBool("recursive")
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		if concurrency <= 0 {
			concurrency = 8
		}

		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		cli := client.NewClient()

		// 1. 远端到远端拷贝
		if srcNode != "" && dstNode != "" {
			// 探测源端是否为目录
			_, lsErr := cli.ListDirWithContext(ctx, srcNode, srcPath)
			isSrcDir := lsErr == nil

			if isSrcDir {
				if !recursive {
					return fmt.Errorf("omitting directory '%s:%s' (use -r to copy recursively)", srcNode, srcPath)
				}

				// 若目的端已存在且为目录，自动放入子目录中 (对齐 Unix cp -r 规范)
				if _, err := cli.ListDirWithContext(ctx, dstNode, dstPath); err == nil {
					dstPath = pathutil.JoinRemotePath(dstPath, pathutil.SafeBaseName(srcPath))
				}

				fmt.Printf("[cworker] Relay copying directory '%s:%s' -> '%s:%s' (concurrency: %d)...\n",
					srcNode, srcPath, dstNode, dstPath, concurrency)
				if err := cli.RelayCopyDir(ctx, srcNode, srcPath, dstNode, dstPath, concurrency, nil); err != nil {
					return fmt.Errorf("relay copy directory failed: %w", err)
				}
				fmt.Println("[OK] Remote to remote directory copy completed successfully.")
				return nil
			}

			// 单文件远端到远端中继拷贝
			if _, err := cli.ListDirWithContext(ctx, dstNode, dstPath); err == nil {
				dstPath = pathutil.JoinRemotePath(dstPath, pathutil.SafeBaseName(srcPath))
			}

			tracker := client.NewProgressTracker(1, 0)
			fmt.Printf("[cworker] Relay copying '%s:%s' -> '%s:%s' directly via CLI pipe...\n", srcNode, srcPath, dstNode, dstPath)
			if err := cli.RelayCopyWithContext(ctx, srcNode, srcPath, dstNode, dstPath, tracker); err != nil {
				return fmt.Errorf("relay copy failed: %w", err)
			}
			tracker.AddFile()
			tracker.Finish()
			fmt.Println("[OK] Remote to remote copy completed successfully.")
			return nil
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
				if !recursive {
					return fmt.Errorf("omitting directory '%s' (use -r to copy recursively)", srcPath)
				}

				// 若目的端已存在且为目录，自动放入子目录中
				if _, err := cli.ListDirWithContext(ctx, dstNode, dstPath); err == nil {
					dstPath = pathutil.JoinRemotePath(dstPath, pathutil.SafeBaseName(cleanLocalSrc))
				}

				fmt.Printf("[cworker] Uploading directory '%s' -> '%s:%s' (concurrency: %d)...\n",
					cleanLocalSrc, dstNode, dstPath, concurrency)
				if err := cli.UploadDir(ctx, dstNode, dstPath, cleanLocalSrc, concurrency, nil); err != nil {
					return fmt.Errorf("upload directory failed: %w", err)
				}
				fmt.Println("[OK] Directory upload completed.")
				return nil
			}

			// 单文件上传
			if _, err := cli.ListDirWithContext(ctx, dstNode, dstPath); err == nil {
				dstPath = pathutil.JoinRemotePath(dstPath, pathutil.SafeBaseName(cleanLocalSrc))
			}

			f, err := os.Open(cleanLocalSrc)
			if err != nil {
				return fmt.Errorf("open local file failed: %w", err)
			}
			defer f.Close()

			tracker := client.NewProgressTracker(1, fi.Size())
			r := client.NewCountingReader(f, tracker)

			fmt.Printf("[cworker] Uploading local '%s' -> '%s:%s'...\n", cleanLocalSrc, dstNode, dstPath)
			if err := cli.UploadFileWithContext(ctx, dstNode, dstPath, r); err != nil {
				return fmt.Errorf("upload failed: %w", err)
			}
			tracker.AddFile()
			tracker.Finish()
			fmt.Println("[OK] Upload completed.")
			return nil
		}

		// 3. 远端到本地下载
		if srcNode != "" && dstNode == "" {
			cleanLocalDst, err := pathutil.NormalizeLocalPath(dstPath)
			if err != nil {
				return err
			}

			// 探测源端是否为目录
			_, lsErr := cli.ListDirWithContext(ctx, srcNode, srcPath)
			isSrcDir := lsErr == nil

			if isSrcDir {
				if !recursive {
					return fmt.Errorf("omitting directory '%s:%s' (use -r to copy recursively)", srcNode, srcPath)
				}

				// 若本地目标已存在且为目录，放入子目录中
				if fi, err := os.Stat(cleanLocalDst); err == nil && fi.IsDir() {
					cleanLocalDst = filepath.Join(cleanLocalDst, pathutil.SafeBaseName(srcPath))
				}

				fmt.Printf("[cworker] Downloading directory '%s:%s' -> local '%s' (concurrency: %d)...\n",
					srcNode, srcPath, cleanLocalDst, concurrency)
				if err := cli.DownloadDir(ctx, srcNode, srcPath, cleanLocalDst, concurrency, nil); err != nil {
					return fmt.Errorf("download directory failed: %w", err)
				}
				fmt.Println("[OK] Directory download completed.")
				return nil
			}

			// 单文件下载
			if fi, err := os.Stat(cleanLocalDst); err == nil && fi.IsDir() {
				cleanLocalDst = filepath.Join(cleanLocalDst, pathutil.SafeBaseName(srcPath))
			}

			dstDir := filepath.Dir(cleanLocalDst)
			_ = os.MkdirAll(dstDir, 0755)

			// 使用同目录下的临时文件落盘，下载并校验成功后原子重命名，严防失败时误删/破坏已有目标文件
			tmpFile := filepath.Join(dstDir, fmt.Sprintf(".%s.cwtemp-%d", filepath.Base(cleanLocalDst), time.Now().UnixNano()))
			f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return fmt.Errorf("create temp download file failed: %w", err)
			}
			committed := false
			defer func() {
				_ = f.Close()
				if !committed {
					_ = os.Remove(tmpFile)
				}
			}()

			tracker := client.NewProgressTracker(1, 0)
			fmt.Printf("[cworker] Downloading '%s:%s' -> local '%s'...\n", srcNode, srcPath, cleanLocalDst)
			if err := cli.DownloadFileWithContext(ctx, srcNode, srcPath, f, tracker); err != nil {
				return fmt.Errorf("download failed: %w", err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("flush local file failed: %w", err)
			}

			// 原子重命名替换目标文件
			if err := os.Rename(tmpFile, cleanLocalDst); err != nil {
				_ = os.Remove(cleanLocalDst)
				if err2 := os.Rename(tmpFile, cleanLocalDst); err2 != nil {
					return fmt.Errorf("atomic rename failed: %w", err2)
				}
			}
			committed = true
			tracker.AddFile()
			tracker.Finish()
			fmt.Println("[OK] Download completed.")
			return nil
		}

		// 4. 本地到本地拷贝
		if srcNode == "" && dstNode == "" {
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
				if !recursive {
					return fmt.Errorf("omitting directory '%s' (use -r to copy recursively)", srcPath)
				}

				if dstFi, err := os.Stat(cleanLocalDst); err == nil && dstFi.IsDir() {
					cleanLocalDst = filepath.Join(cleanLocalDst, pathutil.SafeBaseName(cleanLocalSrc))
				}

				fmt.Printf("[cworker] Copying local directory '%s' -> '%s' (concurrency: %d)...\n",
					cleanLocalSrc, cleanLocalDst, concurrency)
				if err := cli.LocalCopyDir(ctx, cleanLocalSrc, cleanLocalDst, concurrency, nil); err != nil {
					return fmt.Errorf("local copy directory failed: %w", err)
				}
				fmt.Println("[OK] Local directory copy completed.")
				return nil
			}

			// 单文件本地拷贝
			if dstFi, err := os.Stat(cleanLocalDst); err == nil && dstFi.IsDir() {
				cleanLocalDst = filepath.Join(cleanLocalDst, pathutil.SafeBaseName(cleanLocalSrc))
			}

			tracker := client.NewProgressTracker(1, fi.Size())
			fmt.Printf("[cworker] Copying local file '%s' -> '%s'...\n", cleanLocalSrc, cleanLocalDst)
			if err := cli.LocalCopyFile(cleanLocalSrc, cleanLocalDst, tracker); err != nil {
				return fmt.Errorf("local copy failed: %w", err)
			}
			tracker.AddFile()
			tracker.Finish()
			fmt.Println("[OK] Local copy completed.")
			return nil
		}

		return errors.New("unsupported copy parameters")
	},
}

func init() {
	cpCmd.Flags().BoolVarP(&cpRecursive, "recursive", "r", false, "递归复制文件夹")
	cpCmd.Flags().IntVarP(&cpConcurrency, "concurrency", "j", 8, "并发传输连接数 (默认 8)")
	RootCmd.AddCommand(cpCmd)
}
