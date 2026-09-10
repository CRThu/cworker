package cmd

import (
	"fmt"

	"cworker/pkg/client"
	"github.com/spf13/cobra"
)

var killCmd = &cobra.Command{
	Use:   "kill <job_id>",
	Short: "终止指定任务并由 Windows 内核彻底销毁整棵子进程树",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jobID := args[0]
		cli := client.NewClient()
		info, err := cli.KillJob(jobID)
		if err != nil {
			return fmt.Errorf("kill failed: %w", err)
		}

		fmt.Printf("[OK] Job %s terminated (Status: %s, ExitCode: %d)\n", info.ID, info.Status, info.ExitCode)
		return nil
	},
}

func init() {
	RootCmd.AddCommand(killCmd)
}
