package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"cworker/pkg/client"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsLines  int
)

var logsCmd = &cobra.Command{
	Use:   "logs <job_id>",
	Short: "查看任务的输出日志 (支持 -f 实时跟随与 -n 行数截取)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jobID := args[0]
		cli := client.NewClient()

		if logsFollow {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			return cli.StreamLogs(ctx, jobID, os.Stdout)
		}

		output, err := cli.GetLogs(jobID, logsLines)
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
	RootCmd.AddCommand(logsCmd)
}
