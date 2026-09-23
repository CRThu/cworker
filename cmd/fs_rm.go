package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"github.com/spf13/cobra"
)

var (
	rmRecursive bool
	rmYes       bool
)

var rmCmd = &cobra.Command{
	Use:   "rm [flags] [<node>:]<path>",
	Short: "删除远端节点或本地的文件或目录 (非空目录需显式指定 -r)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node, path := pathutil.ParseNodePath(args[0])

		targetName := path
		if node != "" {
			targetName = fmt.Sprintf("%s:%s", node, path)
		}

		// 交互式二次确认防线
		if rmRecursive && !rmYes {
			fmt.Printf("Are you sure you want to recursively delete '%s'? [y/N]: ", targetName)
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err != nil {
				return err
			}
			input = strings.TrimSpace(strings.ToLower(input))
			if input != "y" && input != "yes" {
				fmt.Println("Operation canceled.")
				return nil
			}
		}

		cli := newCmdClient()
		startTime := time.Now()
		interval := client.GetDefaultHeartbeatInterval()
		isTTY := client.IsTerminal()

		var removedCount int64
		var stopHeartbeat chan struct{}
		if rmRecursive {
			stopHeartbeat = make(chan struct{})
			go func() {
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for {
					select {
					case <-stopHeartbeat:
						return
					case <-ticker.C:
						count := atomic.LoadInt64(&removedCount)
						elapsed := time.Since(startTime).Truncate(time.Second)
						if isTTY {
							if count > 0 {
								sec := time.Since(startTime).Seconds()
								speed := 0.0
								if sec > 0.05 {
									speed = float64(count) / sec
								}
								fmt.Printf("\rDeleting '%s'... %s items removed (%s items/s, elapsed %s)  ",
									targetName, client.FormatCount(count), client.FormatCount(int64(speed)), elapsed)
							} else {
								fmt.Printf("\rDeleting '%s'... (elapsed %s)  ", targetName, elapsed)
							}
						} else {
							if count > 0 {
								fmt.Printf("[cworker] Deleting '%s'... %s items removed (elapsed %s)...\n",
									targetName, client.FormatCount(count), elapsed)
							} else {
								fmt.Printf("[cworker] Deleting '%s' (elapsed %s)...\n", targetName, elapsed)
							}
						}
					}
				}
			}()
		}

		progressCb := func(count int64) {
			atomic.StoreInt64(&removedCount, count)
			if isTTY {
				elapsed := time.Since(startTime).Truncate(time.Second)
				sec := time.Since(startTime).Seconds()
				speed := 0.0
				if sec > 0.05 {
					speed = float64(count) / sec
				}
				fmt.Printf("\rDeleting '%s'... %s items removed (%s items/s, elapsed %s)  ",
					targetName, client.FormatCount(count), client.FormatCount(int64(speed)), elapsed)
			}
		}

		sigCtx, stopSig := signal.NotifyContext(cmdContext(cmd), os.Interrupt, syscall.SIGTERM)
		defer stopSig()

		finalCount, err := cli.DeleteWithProgress(sigCtx, node, path, rmRecursive, progressCb)
		if stopHeartbeat != nil {
			close(stopHeartbeat)
		}

		elapsed := time.Since(startTime)
		hasPrintedTTY := isTTY && (elapsed >= interval || atomic.LoadInt64(&removedCount) > 0)

		if sigCtx.Err() != nil {
			if hasPrintedTTY {
				fmt.Println()
			}
			fmt.Fprintf(os.Stderr, "[WARN] Removal interrupted by user (%s items deleted).\n", client.FormatCount(finalCount))
			return &ExitError{Code: 130, Msg: "removal interrupted by user"}
		}

		if err != nil {
			if hasPrintedTTY {
				fmt.Println()
			}
			return fmt.Errorf("delete failed: %w", err)
		}

		if hasPrintedTTY {
			fmt.Println()
		}

		if elapsed >= 3*time.Second {
			if finalCount > 1 {
				fmt.Printf("[OK] Deleted '%s' (%s items) in %s\n", targetName, client.FormatCount(finalCount), elapsed.Truncate(10*time.Millisecond))
			} else {
				fmt.Printf("[OK] Deleted '%s' in %s\n", targetName, elapsed.Truncate(10*time.Millisecond))
			}
		} else {
			fmt.Printf("[OK] Deleted '%s'\n", targetName)
		}
		return nil
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&rmRecursive, "recursive", "r", false, "递归删除目录及其下所有文件")
	rmCmd.Flags().BoolVarP(&rmYes, "yes", "y", false, "自动确认高危删除，跳过交互式提示 (适合脚本与 Agent 自动化)")
	RootCmd.AddCommand(rmCmd)
}
