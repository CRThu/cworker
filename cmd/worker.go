package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"cworker/pkg/protocol"
	"cworker/pkg/worker"
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

var (
	workerName string
	workerPort int
	workerData string
)

var workerCmd = &cobra.Command{
	Use:     "worker",
	Aliases: []string{"agent"},
	Short:   "前台启动工作节点服务 (Worker，适合临时测试与排查)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1. 若由 Windows Service Control Manager (SCM) 启动，接入 SCM 调度生命周期
		if !service.Interactive() {
			prg := &serviceProgram{
				cfg: worker.Config{
					Name:    workerName,
					Port:    workerPort,
					DataDir: workerData,
				},
			}
			targetExe, _ := os.Executable()
			s, err := service.New(prg, getServiceConfig(targetExe))
			if err != nil {
				return err
			}
			return s.Run()
		}

		// 2. 交互式控制台前台启动
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		w, err := worker.NewWorker(worker.Config{
			Name:    workerName,
			Port:    workerPort,
			DataDir: workerData,
		})
		if err != nil {
			return fmt.Errorf("init worker failed: %w", err)
		}

		fmt.Printf("[cworker] Starting worker '%s' on port %d...\n", w.Name(), w.Port())
		fmt.Printf("[cworker] Security Token: %s\n", w.Token())
		fmt.Printf("[cworker] To connect from another machine (Dynamic DNS / Hostname):\n  cw node add %s --token %s\n", w.Name(), w.Token())

		nics := getSystemInterfaces()
		if len(nics) > 0 {
			fmt.Println("\n[cworker] Alternative connection commands by Network Interface:")
			for _, nic := range nics {
				target := fmt.Sprintf("%s:%d", nic.IP, w.Port())
				if nic.IsIPv6 {
					target = fmt.Sprintf("[%s]:%d", nic.IP, w.Port())
				}
				fmt.Printf("  cw node add %s %s --token %s  # [%s]\n", w.Name(), target, w.Token(), nic.Name)
			}
		}
		fmt.Println()

		return w.Start(ctx)
	},
}

func init() {
	workerCmd.Flags().StringVarP(&workerName, "name", "n", "", "自定义节点名称 (默认为系统计算机名 Hostname)")
	workerCmd.Flags().IntVarP(&workerPort, "port", "p", protocol.DefaultPort, "Worker HTTP 服务监听端口")
	workerCmd.Flags().StringVar(&workerData, "data-dir", "", "工作数据与本地日志存放根目录 (默认为 ~/.cworker)")
	RootCmd.AddCommand(workerCmd)
}
