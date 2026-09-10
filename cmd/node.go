package cmd

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var (
	nodeToken string
)

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "管理本地已知节点记忆账本 (添加/移除远端 Worker)",
}

var nodeAddCmd = &cobra.Command{
	Use:   "add <target> [address]",
	Short: "向本地已知账本添加新节点 (支持单参数同名添加：cw node add desktop-4090 --token xxx)",
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

		cli := client.NewClient()
		err := cli.SaveKnownNode(protocol.KnownNode{
			Name:   name,
			Target: target,
			Token:  token,
		})
		if err != nil {
			return fmt.Errorf("add node failed: %w", err)
		}

		fmt.Printf("[OK] Added node '%s' (%s) to known nodes ledger.\n", name, target)
		return nil
	},
}

var nodeRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "从本地已知账本移除节点",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cli := client.NewClient()
		if err := cli.RemoveKnownNode(name); err != nil {
			return fmt.Errorf("remove node failed: %w", err)
		}
		fmt.Printf("[OK] Removed node '%s' from known nodes.\n", name)
		return nil
	},
}

func init() {
	nodeAddCmd.Flags().StringVar(&nodeToken, "token", "", "远端 Worker 认证 Token (首次配对使用)")

	nodeCmd.AddCommand(nodeAddCmd)
	nodeCmd.AddCommand(nodeRmCmd)
	RootCmd.AddCommand(nodeCmd)
}
