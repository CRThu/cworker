package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"cworker/pkg/client"
	"cworker/pkg/pathutil"
	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var (
	diffRecursive bool
	diffAll       bool
	diffLimit     int
)

var diffCmd = &cobra.Command{
	Use:          "diff [flags] [<node>:]<src> [<node>:]<dest>",
	Short:        "跨机或本地比对文件与目录 (基于 SHA-256 强校验，默认排除相同文件)",
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		srcRaw := args[0]
		dstRaw := args[1]

		srcNode, srcPath := pathutil.ParseNodePath(srcRaw)
		dstNode, dstPath := pathutil.ParseNodePath(dstRaw)

		recursive, _ := cmd.Flags().GetBool("recursive")
		all, _ := cmd.Flags().GetBool("all")
		limit, _ := cmd.Flags().GetInt("limit")

		ctx := cmdContext(cmd)

		cli := client.NewClient()

		// 1. 获取源端与目标端的文件清单与哈希 (通过 Client.Hash 统一本地与远端)
		srcFiles, err := cli.Hash(ctx, srcNode, srcPath, recursive)
		if err != nil {
			if strings.Contains(err.Error(), "requires recursive flag (-r)") {
				return &ExitError{Code: 2, Msg: fmt.Sprintf("omitting directory '%s' (use -r to diff recursively)", srcRaw)}
			}
			return &ExitError{Code: 2, Msg: fmt.Sprintf("failed to inspect source '%s': %v", srcRaw, err)}
		}

		dstFiles, err := cli.Hash(ctx, dstNode, dstPath, recursive)
		if err != nil {
			if strings.Contains(err.Error(), "requires recursive flag (-r)") {
				return &ExitError{Code: 2, Msg: fmt.Sprintf("omitting directory '%s' (use -r to diff recursively)", dstRaw)}
			}
			return &ExitError{Code: 2, Msg: fmt.Sprintf("failed to inspect destination '%s': %v", dstRaw, err)}
		}

		// 2. 单文件对比模式
		if !recursive {
			if len(srcFiles) == 1 && len(dstFiles) == 1 {
				sf := srcFiles[0]
				df := dstFiles[0]
				diffRes := client.CompareSingleFile(sf, df)

				if diffRes.Matched == 1 {
					fmt.Println("[MATCH] Files are identical.")
					fmt.Printf("  SHA-256: %s\n", sf.SHA256)
					fmt.Printf("  Size:    %s\n", formatBytes(sf.Size))
					return nil
				}

				fmt.Println("[MISMATCH] Files differ:")
				fmt.Printf("  Left:  %s\n", srcRaw)
				fmt.Printf("         Size: %s | SHA-256: %s\n", formatBytes(sf.Size), sf.SHA256)
				fmt.Printf("  Right: %s\n", dstRaw)
				fmt.Printf("         Size: %s | SHA-256: %s\n", formatBytes(df.Size), df.SHA256)
				return &ExitError{Code: 1}
			}
		}

		// 3. 目录递归对比模式 (-r)
		diffRes := client.CompareFileInfos(srcFiles, dstFiles)

		if diffRes.Modified == 0 && diffRes.Added == 0 && diffRes.Deleted == 0 {
			fmt.Printf("[OK] All %d files match (no differences).\n", diffRes.Matched)
			return nil
		}

		// 输出差异条目 (默认排除全部 MATCH 项，若差异条目过多自动截断省略)
		totalDiffs := diffRes.Modified + diffRes.Added + diffRes.Deleted
		effectiveLimit := limit
		if all || limit <= 0 {
			effectiveLimit = totalDiffs
		}

		w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
		fmt.Fprintln(w, "STATUS\tSIZE (SRC -> DST)\tPATH")
		displayed := 0
		for _, entry := range diffRes.Entries {
			if entry.Status == protocol.DiffStatusMatch {
				continue
			}
			if displayed >= effectiveLimit {
				break
			}

			var sizeStr string
			switch entry.Status {
			case protocol.DiffStatusModified:
				sizeStr = fmt.Sprintf("%s -> %s", formatBytes(entry.SrcSize), formatBytes(entry.DstSize))
			case protocol.DiffStatusAdded:
				sizeStr = fmt.Sprintf("%s -> -", formatBytes(entry.SrcSize))
			case protocol.DiffStatusDeleted:
				sizeStr = fmt.Sprintf("- -> %s", formatBytes(entry.DstSize))
			}

			fmt.Fprintf(w, "[%s]\t%s\t%s\n", entry.Status, sizeStr, entry.Path)
			displayed++
		}
		_ = w.Flush()

		if displayed < totalDiffs {
			fmt.Printf("... and %d more differing entries omitted (use --all to show all)\n", totalDiffs-displayed)
		}

		fmt.Printf("\nSummary: %d matched, %d modified, %d added, %d deleted.\n",
			diffRes.Matched, diffRes.Modified, diffRes.Added, diffRes.Deleted)

		return &ExitError{Code: 1}
	},
}

func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	floatB := float64(bytes)
	if floatB < 1024*1024 {
		return fmt.Sprintf("%.1f KB", floatB/1024)
	}
	if floatB < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", floatB/(1024*1024))
	}
	return fmt.Sprintf("%.2f GB", floatB/(1024*1024*1024))
}

func init() {
	diffCmd.Flags().BoolVarP(&diffRecursive, "recursive", "r", false, "递归比对目录及其下所有文件")
	diffCmd.Flags().BoolVar(&diffAll, "all", false, "显示全部差异条目，不进行截断省略")
	diffCmd.Flags().IntVar(&diffLimit, "limit", 50, "最大展示的差异条目数 (默认 50，设为 0 或传 --all 查看全部)")
	RootCmd.AddCommand(diffCmd)
}
