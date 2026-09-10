package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"github.com/spf13/cobra"
)

var (
	rmRecursive bool
	rmYes       bool
)

var rmCmd = &cobra.Command{
	Use:   "rm [flags] <node>:<path>",
	Short: "删除远端节点的文件或目录 (非空目录需显式指定 -r)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node, path := pathutil.ParseNodePath(args[0])
		if node == "" {
			return errors.New("missing node target, format: <node>:<path>")
		}

		// 交互式二次确认防线
		if rmRecursive && !rmYes {
			fmt.Printf("Are you sure you want to recursively delete '%s:%s'? [y/N]: ", node, path)
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err != nil {
				return err
			}
			input = strings.TrimSpace(strings.ToLower(input))
			if input != "y" && input != "yes" {
				fmt.Println("Operation canceled.")
				return nil
			}
		}

		cli := client.NewClient()

		if err := cli.Delete(node, path, rmRecursive); err != nil {
			return fmt.Errorf("delete failed: %w", err)
		}

		fmt.Printf("[OK] Deleted '%s:%s'\n", node, path)
		return nil
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&rmRecursive, "recursive", "r", false, "递归删除目录及其下所有文件")
	rmCmd.Flags().BoolVarP(&rmYes, "yes", "y", false, "自动确认高危删除，跳过交互式提示 (适合脚本与 Agent 自动化)")
	RootCmd.AddCommand(rmCmd)
}
