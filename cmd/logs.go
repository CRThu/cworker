package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"cworker/pkg/client"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsLines  int
	logsNode   string
)

var logsCmd = &cobra.Command{
	Use:   "logs [<node>:]<job_id>",
	Short: "查看任务的输出日志 (支持 -f 实时跟随、-n 行数截取与 --node 定向直连)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rawTarget := args[0]
		targetNode := logsNode
		jobID := rawTarget

		// 支持 [<node>:]<job_id> 语法糖 (例如 carrot-work:job-a8846cf5)
		if idx := strings.Index(rawTarget, ":"); idx != -1 {
			targetNode = rawTarget[:idx]
			jobID = rawTarget[idx+1:]
		}

		cli := client.NewClient()

		if logsFollow {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			if targetNode != "" {
				return cli.StreamLogsNode(ctx, targetNode, jobID, os.Stdout)
			}
			return cli.StreamLogs(ctx, jobID, os.Stdout)
		}

		var output string
		var err error
		if targetNode != "" {
			output, err = cli.GetLogsNode(targetNode, jobID, logsLines)
		} else {
			output, err = cli.GetLogs(jobID, logsLines)
		}
		if err != nil {
			return err
		}

		fmt.Print(output)
		return nil
	},
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "实时流式监听日志输出 (WebSocket)")
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", 100, "读取末尾指定行数")
	logsCmd.Flags().StringVar(&logsNode, "node", "", "定向指定目标 Worker 节点 (跳过全集群探测，毫秒级直连)")
	RootCmd.AddCommand(logsCmd)
}
