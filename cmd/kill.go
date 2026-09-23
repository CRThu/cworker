package cmd

import (
	"fmt"
	"strings"

	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var killNode string

var killCmd = &cobra.Command{
	Use:   "kill [<node>:]<job_id>",
	Short: "终止指定任务并由 Windows 内核彻底销毁整棵子进程树 (支持 -n/--node 定向终止)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rawTarget := args[0]
		targetNode := killNode
		jobID := rawTarget

		// 支持 [<node>:]<job_id> 语法糖 (例如 carrot-work:job-a8846cf5)
		if idx := strings.Index(rawTarget, ":"); idx != -1 {
			targetNode = rawTarget[:idx]
			jobID = rawTarget[idx+1:]
		}

		cli := newCmdClient()
		var info *protocol.JobInfo
		var err error

		if targetNode != "" {
			info, err = cli.KillJobNodeWithContext(cmdContext(cmd), targetNode, jobID)
		} else {
			info, err = cli.KillJobWithContext(cmdContext(cmd), jobID)
		}
		if err != nil {
			return fmt.Errorf("kill failed: %w", err)
		}

		if info.Node != "" {
			fmt.Printf("[OK] Job %s terminated (Node: %s, Status: %s, ExitCode: %d)\n", info.ID, info.Node, info.Status, info.ExitCode)
		} else {
			fmt.Printf("[OK] Job %s terminated (Status: %s, ExitCode: %d)\n", info.ID, info.Status, info.ExitCode)
		}
		return nil
	},
}

func init() {
	killCmd.Flags().StringVarP(&killNode, "node", "n", "", "定向指定目标 Worker 节点 (免去全集群广播，精准终止)")
	RootCmd.AddCommand(killCmd)
}
