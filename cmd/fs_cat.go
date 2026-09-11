package cmd

import (
	"io"
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
		if node == "" {
			cleanLocal, err := pathutil.NormalizeLocalPath(path)
			if err != nil {
				return err
			}
			f, err := os.Open(cleanLocal)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(os.Stdout, f)
			return err
		}

		cli := client.NewClient()

		return cli.DownloadFile(node, path, os.Stdout)
	},
}

func init() {
	RootCmd.AddCommand(catCmd)
}
