package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsLines  int
	logsNode   string
	logsHead   int
	logsRange  string
	logsAll    bool
)

var logsCmd = &cobra.Command{
	Use:   "logs [flags] [<node>:]<job_id>",
	Short: "查看任务的输出日志 (默认 1MB 智能防线，支持 -f 跟随、-n 末尾、--head 开头、-L 区间与 --all)",
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

		cli := newCmdClient()

		if logsFollow {
			ctx, cancel := signal.NotifyContext(cmdContext(cmd), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			if targetNode != "" {
				return cli.StreamLogsNode(ctx, targetNode, jobID, os.Stdout)
			}
			return cli.StreamLogs(ctx, jobID, os.Stdout)
		}

		opts := protocol.TextSliceOptions{
			Head:      logsHead,
			LineRange: logsRange,
			All:       logsAll,
		}
		if cmd.Flags().Changed("lines") {
			opts.Tail = logsLines
		}

		output, err := cli.GetLogsWithOptionsWithContext(cmdContext(cmd), targetNode, jobID, opts)
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
	logsCmd.Flags().IntVar(&logsHead, "head", 0, "读取开头指定行数 (抓启动崩溃根因)")
	logsCmd.Flags().StringVarP(&logsRange, "range", "L", "", "读取指定行号区间 (如 '100:200', '50:', ':30')")
	logsCmd.Flags().BoolVar(&logsAll, "all", false, "输出全部日志 (无截断)")
	logsCmd.Flags().StringVar(&logsNode, "node", "", "定向指定目标 Worker 节点 (跳过全集群探测，毫秒级直连)")
	RootCmd.AddCommand(logsCmd)
}
