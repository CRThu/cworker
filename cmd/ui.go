package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
	"cworker/pkg/ui"
	"github.com/spf13/cobra"
)

var (
	uiPort   int
	uiNoOpen bool
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "启动本地集中式 Web 可视化控制台并管理集群节点与任务",
	Long: `启动本地集中式 Web 可视化控制台 (Dashboard)。
严格监听本地环回 127.0.0.1 (默认端口 19001)，提供全集群节点矩阵、任务生命周期治理、
WebSocket 实时终端流式推流与全双工跨机文件高速互传等全套可视化治理能力。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.NewClient()
		srv := ui.NewServer(ui.Config{
			BindAddr: "127.0.0.1",
			Port:     uiPort,
			Version:  Version,
			Client:   cli,
		})

		ctx, cancel := signal.NotifyContext(cmdContext(cmd), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		url := fmt.Sprintf("http://127.0.0.1:%d", uiPort)
		fmt.Printf("[cworker] Starting Web UI Dashboard on %s ...\n", url)

		if !uiNoOpen {
			go func() {
				// 略微延迟 300ms 确保端口监听已就绪后拉起默认浏览器
				time.Sleep(300 * time.Millisecond)
				openBrowser(url)
			}()
		}

		fmt.Println("[cworker] Press Ctrl+C to stop the dashboard server.")
		if err := srv.Start(ctx); err != nil && err != context.Canceled {
			return fmt.Errorf("dashboard server error: %w", err)
		}

		fmt.Println("\n[cworker] Dashboard server stopped gracefully.")
		return nil
	},
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func init() {
	uiCmd.Flags().IntVarP(&uiPort, "port", "p", protocol.DefaultUIPort, "控制台监听端口")
	uiCmd.Flags().BoolVar(&uiNoOpen, "no-open", false, "禁止启动时自动拉起系统浏览器")
	RootCmd.AddCommand(uiCmd)
}
