package cmd

import (
	"os"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var (
	catTail  int
	catHead  int
	catLines string
	catAll   bool
)

var catCmd = &cobra.Command{
	Use:   "cat [flags] [<node>:]<path>",
	Short: "直接在控制台终端打印远端或本地文件的文本内容 (默认 1MB 智能防线，支持行切片)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node, path := pathutil.ParseNodePath(args[0])
		cli := client.NewClient()
		opts := protocol.TextSliceOptions{
			Tail:      catTail,
			Head:      catHead,
			LineRange: catLines,
			All:       catAll,
		}
		return cli.CatWithSlice(cmdContext(cmd), node, path, opts, os.Stdout)
	},
}

func init() {
	catCmd.Flags().IntVarP(&catTail, "tail", "n", 0, "读取末尾指定行数")
	catCmd.Flags().IntVar(&catHead, "head", 0, "读取开头指定行数")
	catCmd.Flags().StringVarP(&catLines, "lines", "L", "", "读取指定行号区间 (如 '100:200', '50:', ':30')")
	catCmd.Flags().BoolVar(&catAll, "all", false, "输出完整内容 (无截断)")
	RootCmd.AddCommand(catCmd)
}
