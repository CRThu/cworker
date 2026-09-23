package cmd

import (
	"context"
	"os"

	"cworker/pkg/client"
	"github.com/spf13/cobra"
)

var (
	globalProxy   string
	globalNoProxy bool
)

func init() {
	RootCmd.PersistentFlags().StringVar(&globalProxy, "proxy", "", "显式指定 HTTP/HTTPS/SOCKS5 代理地址 (例如 http://127.0.0.1:7890)")
	RootCmd.PersistentFlags().BoolVar(&globalNoProxy, "no-proxy", false, "显式禁用所有代理，强制物理直连")
}

// newCmdClient 统一根据全局命令行参数构造 Client 实例
func newCmdClient() *client.Client {
	return client.NewClient(
		client.WithProxy(globalProxy),
		client.WithNoProxy(globalNoProxy),
	)
}

// cmdContext 安全提取 Cobra 命令的 Context，并在为 nil 时安全回退至 context.Background()
func cmdContext(cmd *cobra.Command) context.Context {
	if cmd != nil {
		if ctx := cmd.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

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
