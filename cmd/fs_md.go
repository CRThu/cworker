package cmd

import (
	"errors"
	"fmt"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"github.com/spf13/cobra"
)

var mdCmd = &cobra.Command{
	Use:   "md <node>:<path>",
	Short: "在远端节点递归创建空目录 (自动附带 -p 行为)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node, path := pathutil.ParseNodePath(args[0])
		if node == "" {
			return errors.New("missing node target, format: <node>:<path>")
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
