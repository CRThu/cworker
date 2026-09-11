package cmd

import (
	"fmt"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"github.com/spf13/cobra"
)

var mdCmd = &cobra.Command{
	Use:   "md [<node>:]<path>",
	Short: "在远端节点或本地递归创建空目录 (自动附带 -p 行为)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node, path := pathutil.ParseNodePath(args[0])
		if node == "" {
			if err := client.MakeLocalDir(path); err != nil {
				return fmt.Errorf("local mkdir failed: %w", err)
			}
			fmt.Printf("[OK] Local directory created: '%s'\n", path)
			return nil
		}

		cli := client.NewClient()

		if err := cli.MakeDir(node, path); err != nil {
			return fmt.Errorf("mkdir failed: %w", err)
		}

		fmt.Printf("[OK] Directory created on '%s:%s'\n", node, path)
		return nil
	},
}

func init() {
	RootCmd.AddCommand(mdCmd)
}
