package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	// Version 当前软件发布版本号 (SSOT 单一事实来源)
	Version = "1.2.0"
	// BuildDate 构建日期
	BuildDate = "2026-09-11"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "查看 cworker CLI 与运行环境版本信息",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("cworker (cw) version %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
		fmt.Printf("Go runtime: %s\n", runtime.Version())
		if BuildDate != "" {
			fmt.Printf("Build date: %s\n", BuildDate)
		}
	},
}

func init() {
	RootCmd.Version = Version
	RootCmd.Flags().BoolP("version", "v", false, "查看 cworker CLI 版本")
	RootCmd.AddCommand(versionCmd)
}
