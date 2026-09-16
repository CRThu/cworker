package cmd

import (
	"os"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"github.com/spf13/cobra"
)

var catCmd = &cobra.Command{
	Use:   "cat [<node>:]<path>",
	Short: "直接在控制台终端打印远端或本地文件的文本内容",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node, path := pathutil.ParseNodePath(args[0])
		cli := client.NewClient()
		return cli.Cat(cmdContext(cmd), node, path, os.Stdout)
	},
}

func init() {
	RootCmd.AddCommand(catCmd)
}
