package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"cworker/pkg/client"
	"github.com/spf13/cobra"
)

var nodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "查看集群中所有 Worker 节点的在线状态与实时硬件负载",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.NewClient()
		nodes, err := cli.ListNodes()
		if err != nil {
			return err
		}

		if len(nodes) == 0 {
			fmt.Println("No active workers discovered in cluster.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tADDRESS\tCPU(%)\tMEM(FREE/TOTAL)\tJOBS")
		for _, n := range nodes {
			memStr := fmt.Sprintf("%dM / %dM", n.Metrics.MemFreeMB, n.Metrics.MemTotalMB)
			cpuStr := fmt.Sprintf("%.1f%%", n.Metrics.CPUPercent)
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n",
				n.Name, n.Status, n.Address, cpuStr, memStr, n.ActiveJobs)
		}
		return w.Flush()
	},
}

func init() {
	RootCmd.AddCommand(nodesCmd)
}
