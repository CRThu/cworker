package cmd

import (
	"fmt"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"github.com/spf13/cobra"
)

var (
	cpRecursive   bool
	cpConcurrency int
)

var cpCmd = &cobra.Command{
	Use:   "cp [flags] [<node>:]<src> [<node>:]<dest>",
	Short: "跨机或本地拷贝文件与目录 (支持 本地<->远端、远端<->远端、本地<->本地 管道直连与并发传输)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		srcRaw := args[0]
		dstRaw := args[1]

		srcNode, srcPath := pathutil.ParseNodePath(srcRaw)
		dstNode, dstPath := pathutil.ParseNodePath(dstRaw)

		recursive, _ := cmd.Flags().GetBool("recursive")
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		if concurrency <= 0 {
			concurrency = 8
		}

		ctx := cmdContext(cmd)

		cli := client.NewClient()
		tracker := client.NewProgressTracker(1, 0)

		opts := client.TransferOptions{
			SrcNode:     srcNode,
			SrcPath:     srcPath,
			DstNode:     dstNode,
			DstPath:     dstPath,
			Recursive:   recursive,
			Concurrency: concurrency,
		}

		if err := cli.Transfer(ctx, opts, tracker); err != nil {
			return err
		}

		fmt.Println("[OK] Transfer completed successfully.")
		return nil
	},
}

func init() {
	cpCmd.Flags().BoolVarP(&cpRecursive, "recursive", "r", false, "递归复制文件夹")
	cpCmd.Flags().IntVarP(&cpConcurrency, "concurrency", "j", 8, "并发传输连接数 (默认 8)")
	RootCmd.AddCommand(cpCmd)
}
