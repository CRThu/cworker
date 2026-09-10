package cmd

import (
	"errors"
	"os"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"github.com/spf13/cobra"
)

var catCmd = &cobra.Command{
	Use:   "cat <node>:<path>",
	Short: "直接在本地终端打印远端文件的文本内容，免去临时下载",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node, path := pathutil.ParseNodePath(args[0])
		if node == "" {
			return errors.New("missing node target, format: <node>:<path>")
		}

		cli := client.NewClient()

		return cli.DownloadFile(node, path, os.Stdout)
	},
}

func init() {
	RootCmd.AddCommand(catCmd)
}
