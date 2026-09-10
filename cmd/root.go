package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// RootCmd cworker 顶层主命令 cw
var RootCmd = &cobra.Command{
	Use:   "cw",
	Short: "cworker (Carrot Worker) - 去中心化内网分布式任务编排与原生进程治理工具",
	Long: `cworker (Carrot Worker)
专为 Windows 内网环境打造的去中心化任务编排与原生进程治理系统。
全网平权无 Server，开箱即用，支持全盘原生权限、Win32 作业对象孤儿治理与点对点流式传输。`,
	SilenceUsage: true,
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
