package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var (
	psAll   bool
	psLimit int
	psNode  string
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "查看全集群或指定节点的任务运行状态、硬件开销与进程 ID",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.NewClient()
		jobs, err := cli.ListJobsWithContext(cmdContext(cmd), psNode)
		if err != nil {
			return err
		}

		if len(jobs) == 0 {
			if psNode != "" {
				fmt.Printf("No jobs found on node '%s'.\n", psNode)
			} else {
				fmt.Println("No jobs found in cluster.")
			}
			return nil
		}

		// 排序保障：RUNNING 状态任务置顶优先，其余按启动时间倒序排
		sortJobsForDisplay(jobs)

		displayCount := len(jobs)
		omittedCount := 0
		if !psAll && psLimit > 0 && len(jobs) > psLimit {
			displayCount = psLimit
			omittedCount = len(jobs) - psLimit
		}

		w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNODE\tNAME\tSTATUS\tCPU%\tMEM(MB)\tUPTIME\tCOMMAND")

		for i := 0; i < displayCount; i++ {
			j := jobs[i]
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
		if err := w.Flush(); err != nil {
			return err
		}

		if omittedCount > 0 {
			fmt.Printf("... and %d older finished jobs omitted (use 'cw ps --all' to show all)\n", omittedCount)
		}
		return nil
	},
}

// sortJobsForDisplay 优先将 RUNNING 任务排在最前，其余任务按启动时间倒序
func sortJobsForDisplay(jobs []protocol.JobInfo) {
	sort.SliceStable(jobs, func(i, j int) bool {
		iRunning := jobs[i].Status == protocol.JobStatusRunning
		jRunning := jobs[j].Status == protocol.JobStatusRunning
		if iRunning && !jRunning {
			return true
		}
		if !iRunning && jRunning {
			return false
		}
		return jobs[i].StartTime.After(jobs[j].StartTime)
	})
}

func init() {
	psCmd.Flags().BoolVarP(&psAll, "all", "a", false, "显示所有历史任务（取消默认显示条数限制）")
	psCmd.Flags().IntVarP(&psLimit, "limit", "l", 20, "限制终端显示的任务条数（默认 20 条）")
	psCmd.Flags().StringVarP(&psNode, "node", "n", "", "仅查看指定受控节点上的任务 (省略则查看全集群)")
	RootCmd.AddCommand(psCmd)
}
