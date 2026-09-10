package cmd

import (
	"fmt"
	"strings"

	"cworker/pkg/client"
	"cworker/pkg/protocol"
	"github.com/spf13/cobra"
)

var (
	runNodeName string
	runJobName  string
	runDir      string
	runToken    string
)

var runCmd = &cobra.Command{
	Use:   "run [flags] <command>",
	Short: "派发新任务到指定节点 (或自动负载均衡调度)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rawCommand := strings.Join(args, " ")
		cli := client.NewClient()

		info, err := cli.RunJob(protocol.RunJobRequest{
			Name:    runJobName,
			Node:    runNodeName,
			Command: rawCommand,
			Dir:     runDir,
		}, runToken)
		if err != nil {
			return fmt.Errorf("dispatch failed: %w", err)
		}

		fmt.Printf("[OK] Job %s dispatched to node '%s' (PID: %d)\n", info.ID, info.Node, info.PID)
		fmt.Printf("Use 'cw logs %s -f' to stream live logs.\n", info.ID)
		return nil
	},
}

func init() {
	runCmd.Flags().StringVarP(&runNodeName, "node", "n", "", "目标节点名称/主机名/IP")
	runCmd.Flags().StringVar(&runJobName, "name", "", "任务自定义名称")
	runCmd.Flags().StringVar(&runDir, "dir", "", "任务执行工作路径 (CWD)")
	runCmd.Flags().StringVar(&runToken, "token", "", "远端 Worker 认证 Token (首次连接传入自动记忆)")
	RootCmd.AddCommand(runCmd)
}
