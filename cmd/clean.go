package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"cworker/pkg/client"
	"github.com/spf13/cobra"
)

var (
	cleanDays int
	cleanAll  bool
	cleanNode string
	cleanYes  bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "显式清理已结束的历史任务及其磁盘日志目录",
	Long: `显式清理 Worker 内存中已终态的任务记录，并同步物理删除对应的磁盘 jobs/<id> 日志目录。
必须显式指定 --days <N> 或 --all 之一，严禁未指定参数的静默全量清理。正在运行（RUNNING）的任务受严格保护，绝不被清理。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cleanAll && cleanDays <= 0 {
			return fmt.Errorf("must specify either --days <N> (e.g. --days 7) or --all to clean finished jobs")
		}

		targetDesc := "all known nodes"
		if cleanNode != "" {
			targetDesc = fmt.Sprintf("node '%s'", cleanNode)
		}

		conditionDesc := fmt.Sprintf("older than %d days", cleanDays)
		if cleanAll {
			conditionDesc = "all finished jobs"
		}

		// 交互式二次确认
		if !cleanYes {
			fmt.Printf("Are you sure you want to clean %s on %s? [y/N]: ", conditionDesc, targetDesc)
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
		results, err := cli.CleanJobs(cleanNode, cleanDays, cleanAll)
		if err != nil {
			return fmt.Errorf("clean jobs failed: %w", err)
		}

		if len(results) == 0 {
			fmt.Println("No online nodes responded or no jobs matched the criteria.")
			return nil
		}

		totalCleaned := 0
		var totalFreed int64

		for nodeName, res := range results {
			totalCleaned += res.CleanedCount
			totalFreed += res.FreedBytes
			fmt.Printf("[OK] %s: Cleaned %d finished jobs (freed %s)\n",
				nodeName, res.CleanedCount, formatBytes(res.FreedBytes))
		}

		fmt.Printf("Total: %d jobs cleaned, %s log space reclaimed across %d node(s).\n",
			totalCleaned, formatBytes(totalFreed), len(results))
		return nil
	},
}

func init() {
	cleanCmd.Flags().IntVarP(&cleanDays, "days", "d", 0, "清理结束时间早于指定天数的任务 (例如 --days 7)")
	cleanCmd.Flags().BoolVar(&cleanAll, "all", false, "清空所有已结束的任务记录与日志")
	cleanCmd.Flags().StringVarP(&cleanNode, "node", "n", "", "指定目标受控节点名称 (省略则对全集群所有节点生效)")
	cleanCmd.Flags().BoolVarP(&cleanYes, "yes", "y", false, "跳过交互式二次确认")
	RootCmd.AddCommand(cleanCmd)
}
