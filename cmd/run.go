package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var (
	runNodeName string
	runJobName  string
	runDir      string
	runToken    string
	runWait     bool
	runClean    bool
)

var runCmd = &cobra.Command{
	Use:   "run [flags] <command>",
	Short: "派发新任务到指定节点 (或自动负载均衡调度)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// 互斥门禁：-c 必须搭配 -w 使用
		if runClean && !runWait {
			return fmt.Errorf("flag '-c / --clean' requires '-w / --wait' (ephemeral clean is only permitted during synchronous execution)")
		}

		rawCommand := strings.Join(args, " ")
		cli := client.NewClient()

		info, err := cli.RunJobWithContext(cmdContext(cmd), protocol.RunJobRequest{
			Name:    runJobName,
			Node:    runNodeName,
			Command: rawCommand,
			Dir:     runDir,
		}, runToken)
		if err != nil {
			return fmt.Errorf("dispatch failed: %w", err)
		}

		// 默认异步后台执行模式 (完全保持向后兼容)
		if !runWait {
			fmt.Printf("[OK] Job %s dispatched to node '%s' (PID: %d)\n", info.ID, info.Node, info.PID)
			fmt.Printf("Use 'cw logs %s -f' to stream live logs.\n", info.ID)
			return nil
		}

		// 前台同步阻塞模式 (-w)
		sigCtx, stopSig := signal.NotifyContext(cmdContext(cmd), os.Interrupt, syscall.SIGTERM)
		defer stopSig()

		// 挂接实时流式输出
		streamErr := cli.StreamLogsNode(sigCtx, info.Node, info.ID, os.Stdout)

		// 响应本地信号中断：联动强杀远端 Win32 Job Object 进程树并清理现场
		if sigCtx.Err() != nil {
			fmt.Fprintln(os.Stderr, "\n[WARN] Execution interrupted, terminating remote process tree...")
			_, _ = cli.KillJobNode(info.Node, info.ID)
			if runClean {
				_, _ = cli.CleanJobWithContext(context.Background(), info.Node, info.ID)
			}
			return &ExitError{Code: 130, Msg: "interrupted by user"}
		}

		// 查询任务终态 ExitCode
		exitCode := 0
		finalInfo, qErr := cli.GetJobInfoWithContext(cmdContext(cmd), info.Node, info.ID)
		if qErr == nil && finalInfo != nil {
			exitCode = finalInfo.ExitCode
		} else if streamErr != nil {
			exitCode = 1
		}

		// 若开启了 -c，执行完自动物理销毁远端临时任务及日志
		if runClean {
			_, cleanErr := cli.CleanJobWithContext(context.Background(), info.Node, info.ID)
			if cleanErr != nil && !strings.Contains(cleanErr.Error(), "400") {
				// 老版本 400 自动优雅降级，其余非预期异常仅输出警告，绝不污染用户退出码
				fmt.Fprintf(os.Stderr, "[WARN] Auto clean job failed: %v\n", cleanErr)
			}
		}

		// 退出码对齐契约
		if exitCode != 0 {
			return &ExitError{Code: exitCode}
		}
		return nil
	},
}

func init() {
	runCmd.Flags().StringVarP(&runNodeName, "node", "n", "", "目标节点名称/主机名/IP")
	runCmd.Flags().StringVar(&runJobName, "name", "", "任务自定义名称")
	runCmd.Flags().StringVar(&runDir, "dir", "", "任务执行工作路径 (CWD)")
	runCmd.Flags().StringVar(&runToken, "token", "", "远端 Worker 认证 Token (首次连接传入自动记忆)")
	runCmd.Flags().BoolVarP(&runWait, "wait", "w", false, "前台同步阻塞执行并实时流式输出日志，进程退出码严格对齐")
	runCmd.Flags().BoolVarP(&runClean, "clean", "c", false, "任务执行完毕后自动物理清理临时任务及日志 (必须搭配 -w/-wait)")
	RootCmd.AddCommand(runCmd)
}

