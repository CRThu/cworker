package cmd

import (
	"bufio"
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
	Use:   "rm [flags] [<node>:]<path>",
	Short: "删除远端节点或本地的文件或目录 (非空目录需显式指定 -r)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node, path := pathutil.ParseNodePath(args[0])

		targetName := path
		if node != "" {
			targetName = fmt.Sprintf("%s:%s", node, path)
		}

		// 交互式二次确认防线
		if rmRecursive && !rmYes {
			fmt.Printf("Are you sure you want to recursively delete '%s'? [y/N]: ", targetName)
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

		if node == "" {
			if err := client.DeleteLocal(path, rmRecursive); err != nil {
				return fmt.Errorf("delete failed: %w", err)
			}
			fmt.Printf("[OK] Deleted '%s'\n", path)
			return nil
		}

		cli := client.NewClient()

		if err := cli.Delete(node, path, rmRecursive); err != nil {
			return fmt.Errorf("delete failed: %w", err)
		}

		fmt.Printf("[OK] Deleted '%s'\n", targetName)
		return nil
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&rmRecursive, "recursive", "r", false, "递归删除目录及其下所有文件")
	rmCmd.Flags().BoolVarP(&rmYes, "yes", "y", false, "自动确认高危删除，跳过交互式提示 (适合脚本与 Agent 自动化)")
	RootCmd.AddCommand(rmCmd)
}
