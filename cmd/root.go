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
	SilenceUsage:  true,
	SilenceErrors: true,
}

type exitCoder interface {
	ExitCode() int
}

// ExitError 携带自定义状态码的 CLI 退出错误
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	return e.Msg
}

func (e *ExitError) ExitCode() int {
	return e.Code
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		exitCode := 1
		var msg string
		if ec, ok := err.(exitCoder); ok {
			exitCode = ec.ExitCode()
			msg = err.Error()
		} else {
			msg = err.Error()
		}
		if msg != "" {
			os.Stderr.WriteString("Error: " + msg + "\n")
		}
		os.Exit(exitCode)
	}
}
