package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"cworker/pkg/client"
	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "查看全集群所有任务的运行状态、硬件开销与进程 ID",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.NewClient()
		jobs, err := cli.ListJobs()
		if err != nil {
			return err
		}

		if len(jobs) == 0 {
			fmt.Println("No jobs found in cluster.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNODE\tNAME\tSTATUS\tCPU%\tMEM(MB)\tUPTIME\tCOMMAND")

		for _, j := range jobs {
			name := j.Name
			if name == "" {
				name = "-"
			}

			// 计算运行时长
			endTime := time.Now()
			if j.EndTime != nil {
				endTime = *j.EndTime
			}
			uptime := endTime.Sub(j.StartTime).Truncate(time.Second).String()

			// 截断过长命令防止终端溢出折行 (按 UTF-8 rune 截断，严防多字节截断损坏字符)
			displayCmd := j.Command
			cmdRunes := []rune(displayCmd)
			if len(cmdRunes) > 35 {
				displayCmd = string(cmdRunes[:32]) + "..."
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.1f%%\t%dM\t%s\t%s\n",
				j.ID, j.Node, name, j.Status, j.Metrics.CPUPercent, j.Metrics.MemoryMB, uptime, displayCmd)
		}
		return w.Flush()
	},
}

func init() {
	RootCmd.AddCommand(psCmd)
}
