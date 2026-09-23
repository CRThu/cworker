package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"cworker/pkg/updater"
	"github.com/spf13/cobra"
)

var (
	updateYes    bool
	updateCheck  bool
	updateForce  bool
	updateMirror string
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "从官方 GitHub Releases (crthu/cworker) 手动检查并自升级",
	Long: `纯手动拉包更新 cworker 至最新发布版本。
自动检测 Windows 注册表系统代理与环境变量，采用 Windows 原位无锁原子替换 (Rename-Replace 模式)。
遇到本机有正在 RUNNING 的活跃任务时默认直接报错拦截，除非显式传递 --force。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Checking for updates from github.com/crthu/cworker...")

		up, err := updater.NewUpdater(Version, globalProxy, globalNoProxy, updateMirror, updateForce)
		if err != nil {
			return fmt.Errorf("init updater failed: %w", err)
		}

		sigCtx, stopSig := signal.NotifyContext(cmdContext(cmd), os.Interrupt, syscall.SIGTERM)
		defer stopSig()
		rel, err := up.FetchLatestRelease(sigCtx)
		if err != nil {
			return fmt.Errorf("check latest release failed: %w", err)
		}

		cleanLatest := strings.TrimPrefix(strings.TrimPrefix(rel.TagName, "v"), "V")
		cleanCurrent := strings.TrimPrefix(strings.TrimPrefix(Version, "v"), "V")

		fmt.Printf("Current version: v%s\n", cleanCurrent)
		fmt.Printf("Latest version:  v%s\n", cleanLatest)

		cmp := updater.CompareSemVer(cleanLatest, cleanCurrent)
		if cmp <= 0 && !updateForce {
			fmt.Printf("\n[OK] cworker is already up to date (v%s).\n", cleanCurrent)
			return nil
		}

		if rel.Body != "" {
			fmt.Println("\n--- Release Notes ---")
			lines := strings.Split(strings.TrimSpace(rel.Body), "\n")
			for _, l := range lines {
				fmt.Println("  " + l)
			}
			fmt.Println("---------------------")
		}

		if updateCheck {
			if cmp > 0 {
				fmt.Printf("\nNew release v%s is available! Run 'cw update' to upgrade.\n", cleanLatest)
			}
			return nil
		}

		// 匹配系统架构资产
		asset, err := updater.MatchAsset(rel.Assets)
		if err != nil {
			return err
		}
		if asset.Size > 0 {
			fmt.Printf("\nMatched asset: %s (%.1f MB)\n", asset.Name, float64(asset.Size)/(1024*1024))
		} else {
			fmt.Printf("\nMatched asset: %s\n", asset.Name)
		}

		// 检查运行中活跃任务冲突
		if err := up.CheckRunningJobs(sigCtx); err != nil {
			if sigCtx.Err() != nil {
				return &ExitError{Code: 130, Msg: "update interrupted by user"}
			}
			return err
		}

		// 交互式二次确认
		if !updateYes {
			fmt.Printf("Do you want to proceed with update to v%s? [y/N]: ", cleanLatest)
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err != nil {
				trimmed := strings.TrimSpace(input)
				if err == io.EOF && trimmed == "" {
					fmt.Println("\nUpdate canceled (EOF detected. In non-interactive or script environments, use 'cw update -y').")
					return nil
				}
				if err != io.EOF {
					return err
				}
			}
			input = strings.TrimSpace(strings.ToLower(input))
			if input != "y" && input != "yes" {
				fmt.Println("Update canceled.")
				return nil
			}
		}

		// 流式下载并显示进度
		fmt.Printf("Downloading %s...\n", asset.Name)
		var lastPercent int = -1
		bytesData, shaSum, err := up.DownloadAsset(sigCtx, asset, func(downloaded, total int64) {
			if total > 0 {
				pct := int(float64(downloaded) / float64(total) * 100)
				if pct != lastPercent {
					lastPercent = pct
					fmt.Printf("\rDownloading: %d%% (%s / %s)", pct, formatBytes(downloaded), formatBytes(total))
				}
			}
		})
		fmt.Println() // 换行
		if sigCtx.Err() != nil {
			fmt.Fprintln(os.Stderr, "\n[WARN] Update interrupted by user.")
			return &ExitError{Code: 130, Msg: "update interrupted by user"}
		}
		if err != nil {
			return fmt.Errorf("download asset failed: %w", err)
		}

		// SHA-256 强校验 (若 Release 提供了 checksums.txt 或 *.sha256)
		if err := up.VerifyChecksum(sigCtx, rel.Assets, asset.Name, shaSum); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
		fmt.Printf("Verified SHA-256 hash successfully (%s...)\n", shaSum[:12])

		// 原地原子替换
		deployedExe, currentExe := updater.GetTargetExePaths()
		replacedCount := 0

		// 若部署路径存在，优先更新部署副本
		if _, err := os.Stat(deployedExe); err == nil {
			if err := updater.ApplyAtomicReplace(deployedExe, bytesData); err != nil {
				return fmt.Errorf("replace deployed binary failed: %w", err)
			}
			fmt.Printf("[OK] Replaced binary at %s\n", deployedExe)
			replacedCount++
		}

		// 若当前运行的 exe 与部署副本不同，且当前文件存在，也同步更新
		if currentExe != "" && !strings.EqualFold(filepath.Clean(currentExe), filepath.Clean(deployedExe)) {
			if _, err := os.Stat(currentExe); err == nil {
				if err := updater.ApplyAtomicReplace(currentExe, bytesData); err == nil {
					fmt.Printf("[OK] Replaced binary at %s\n", currentExe)
					replacedCount++
				}
			}
		}

		if replacedCount == 0 {
			// 两者都不存在则直接写入部署路径
			if err := updater.ApplyAtomicReplace(deployedExe, bytesData); err != nil {
				return fmt.Errorf("write binary failed: %w", err)
			}
			fmt.Printf("[OK] Installed binary to %s\n", deployedExe)
		}

		// 检查并平滑重启 Windows 服务
		restarted, err := updater.RestartWindowsService()
		if err != nil {
			fmt.Printf("[WARNING] Service restart encountered error: %v (run 'cw service restart' manually)\n", err)
		} else if restarted {
			fmt.Println("[OK] Windows service 'cworker' restarted successfully.")
		}

		fmt.Printf("\n[OK] Successfully updated cworker to v%s!\n", cleanLatest)
		return nil
	},
}

func init() {
	updateCmd.Flags().BoolVarP(&updateYes, "yes", "y", false, "跳过交互式二次确认")
	updateCmd.Flags().BoolVar(&updateCheck, "check", false, "仅检查是否有最新版本，不执行下载与替换")
	updateCmd.Flags().BoolVar(&updateForce, "force", false, "强制更新（忽略活跃任务冲突或强制重新覆盖）")
	updateCmd.Flags().StringVar(&updateMirror, "mirror", "", "指定 GitHub 镜像加速前缀 (例如 --mirror https://ghproxy.net/)")
	RootCmd.AddCommand(updateCmd)
}
