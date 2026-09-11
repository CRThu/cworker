package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls [<node>:]<path>",
	Short: "查看远端节点或本地目录的内容与文件元数据",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node, path := pathutil.ParseNodePath(args[0])
		var files []protocol.FileInfo
		var err error

		if node == "" {
			files, err = client.ListLocalDir(path)
		} else {
			cli := client.NewClient()
			files, err = cli.ListDir(node, path)
		}
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
		fmt.Fprintln(w, "MODE\tSIZE\tMODIFIED\tNAME")
		for _, f := range files {
			mode := "-rw-r--r--"
			sizeStr := fmt.Sprintf("%d", f.Size)
			if f.IsDir {
				mode = "drwxr-xr-x"
				sizeStr = "<DIR>"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				mode, sizeStr, f.ModTime.Format("2006-01-02 15:04:05"), f.Name)
		}
		return w.Flush()
	},
}

func init() {
	RootCmd.AddCommand(lsCmd)
}
