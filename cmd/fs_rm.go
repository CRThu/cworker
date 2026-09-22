package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
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

		cli := client.NewClient()
		startTime := time.Now()
		interval := client.GetDefaultHeartbeatInterval()

		var stopHeartbeat chan struct{}
		if rmRecursive {
			stopHeartbeat = make(chan struct{})
			isTTY := client.IsTerminal()
			go func() {
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for {
					select {
					case <-stopHeartbeat:
						return
					case <-ticker.C:
						elapsed := time.Since(startTime).Truncate(time.Second)
						if isTTY {
							fmt.Printf("\rDeleting '%s'... (elapsed %s)  ", targetName, elapsed)
						} else {
							fmt.Printf("[cworker] Deleting '%s' (elapsed %s)...\n", targetName, elapsed)
						}
					}
				}
			}()
		}

		err := cli.DeleteWithContext(cmdContext(cmd), node, path, rmRecursive)
		if stopHeartbeat != nil {
			close(stopHeartbeat)
		}

		elapsed := time.Since(startTime)

		if err != nil {
			if client.IsTerminal() && elapsed >= interval {
				fmt.Println()
			}
			return fmt.Errorf("delete failed: %w", err)
		}

		if client.IsTerminal() && elapsed >= interval {
			fmt.Println()
		}

		if elapsed >= 3*time.Second {
			fmt.Printf("[OK] Deleted '%s' in %s\n", targetName, elapsed.Truncate(10*time.Millisecond))
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
