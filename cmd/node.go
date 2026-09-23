package cmd

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"text/tabwriter"

	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var (
	nodeToken string
)

var nodeCmd = &cobra.Command{
	Use:     "node",
	Aliases: []string{"nodes"},
	Short:   "查看集群节点状态或管理本地已知节点",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runNodeList(cmdContext(cmd))
	},
}

var nodeLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "查看集群中所有 Worker 节点的在线状态与实时硬件负载",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runNodeList(cmdContext(cmd))
	},
}

func runNodeList(ctx ...context.Context) error {
	var c context.Context = context.Background()
	if len(ctx) > 0 && ctx[0] != nil {
		c = ctx[0]
	}
	cli := newCmdClient()
	nodes, err := cli.ListNodesWithContext(c)
	if err != nil {
		return err
	}

	if len(nodes) == 0 {
		fmt.Println("No active workers discovered in cluster.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tVERSION\tOS\tADDRESS\tCPU\tMEM\tJOBS")
	for _, n := range nodes {
		var cpuStr string
		if n.Metrics.CPUCores > 0 {
			usedPercent := int(math.Round(n.Metrics.CPUPercent * float64(n.Metrics.CPUCores)))
			totalPercent := n.Metrics.CPUCores * 100
			cpuStr = fmt.Sprintf("%d%% / %d%%", usedPercent, totalPercent)
		} else {
			cpuStr = fmt.Sprintf("%.1f%%", n.Metrics.CPUPercent)
		}

		var memUsedMB uint64
		if n.Metrics.MemTotalMB > n.Metrics.MemFreeMB {
			memUsedMB = n.Metrics.MemTotalMB - n.Metrics.MemFreeMB
		}
		var memStr string
		if n.Metrics.MemTotalMB >= 1024 {
			memStr = fmt.Sprintf("%.1fG / %.1fG", float64(memUsedMB)/1024.0, float64(n.Metrics.MemTotalMB)/1024.0)
		} else {
			memStr = fmt.Sprintf("%dM / %dM", memUsedMB, n.Metrics.MemTotalMB)
		}

		verStr := n.Version
		if verStr == "" {
			verStr = "-"
		} else if !strings.HasPrefix(verStr, "v") {
			verStr = "v" + verStr
		}

		osStr := n.OSVersion
		if osStr == "" {
			osStr = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
			n.Name, n.Status, verStr, osStr, n.Address, cpuStr, memStr, n.ActiveJobs)
	}
	return w.Flush()
}

var nodeAddCmd = &cobra.Command{
	Use:   "add <target> [address]",
	Short: "添加已知节点 (支持单参数同名添加：cw node add desktop-4090 --token xxx)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		var target string

		if len(args) == 1 {
			// 单参数模式：cw node add desktop-4090 或 cw node add desktop-4090:19000
			input := args[0]
			if host, _, err := net.SplitHostPort(input); err == nil {
				name = host
				target = input
			} else {
				name = input
				target = fmt.Sprintf("%s:%s", input, protocol.DefaultPortStr)
			}
		} else {
			// 双参数模式：cw node add <name> <target>
			name = args[0]
			target = args[1]
			if _, _, err := net.SplitHostPort(target); err != nil {
				target = fmt.Sprintf("%s:%s", target, protocol.DefaultPortStr)
			}
		}

		token := nodeToken
		// 交互式提示补全 Token (如果未通过 --token 传入且在交互式终端中)
		if token == "" {
			fileInfo, _ := os.Stdin.Stat()
			if (fileInfo.Mode() & os.ModeCharDevice) != 0 {
				fmt.Printf("Enter security token for '%s' (press Enter to skip): ", name)
				reader := bufio.NewReader(os.Stdin)
				line, _ := reader.ReadString('\n')
				token = strings.TrimSpace(line)
			}
		}

		if token == "" {
			fmt.Printf("[WARN] Note: No security token configured for '%s'. Unauthenticated requests will be rejected with 401.\n", name)
		}

		cli := newCmdClient()
		err := cli.SaveKnownNode(protocol.KnownNode{
			Name:   name,
			Target: target,
			Token:  token,
		})
		if err != nil {
			return fmt.Errorf("add node failed: %w", err)
		}

		fmt.Printf("[OK] Added node '%s' (%s) to known nodes.\n", name, target)
		return nil
	},
}

var nodeRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "移除已知节点",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cli := newCmdClient()
		if err := cli.RemoveKnownNode(name); err != nil {
			return fmt.Errorf("remove node failed: %w", err)
		}
		fmt.Printf("[OK] Removed node '%s' from known nodes.\n", name)
		return nil
	},
}

func init() {
	nodeAddCmd.Flags().StringVar(&nodeToken, "token", "", "远端 Worker 认证 Token (首次配对使用)")

	nodeCmd.AddCommand(nodeLsCmd)
	nodeCmd.AddCommand(nodeAddCmd)
	nodeCmd.AddCommand(nodeRmCmd)
	RootCmd.AddCommand(nodeCmd)
}
