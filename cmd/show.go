package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"cworker/pkg/protocol"
	"cworker/pkg/worker"
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

var (
	showRefresh bool
)

type InterfaceIP struct {
	Name   string
	IP     string
	IsIPv6 bool
}

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "查看本机节点名片、运行状态、各网卡 IP 与一键配对命令 (带 --refresh 轮换密钥)",
	RunE: func(cmd *cobra.Command, args []string) error {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			homeDir = "."
		}
		dataDir := filepath.Join(homeDir, protocol.DefaultDataDirName)
		tokenPath := filepath.Join(dataDir, protocol.TokenFileName)

		var tokenStr string
		if showRefresh {
			tokenStr, err = worker.RefreshToken(dataDir)
			if err != nil {
				return fmt.Errorf("refresh token failed: %w", err)
			}
			fmt.Println("[OK] Security token refreshed successfully!")
		} else {
			tokenStr, err = worker.LoadOrCreateToken(dataDir)
			if err != nil {
				return fmt.Errorf("load or create token failed: %w", err)
			}
		}

		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "worker-node"
		}

		// 检测 Windows Service 运行状态
		_, targetExe := getDeployedExePath()
		svcCfg := getServiceConfig(targetExe)
		prg := &serviceProgram{}
		s, _ := service.New(prg, svcCfg)
		serviceStatus := "NOT_INSTALLED"
		if s != nil {
			if st, err := s.Status(); err == nil {
				switch st {
				case service.StatusRunning:
					serviceStatus = "RUNNING (Windows Service)"
				case service.StatusStopped:
					serviceStatus = "STOPPED"
				default:
					serviceStatus = "UNKNOWN"
				}
			}
		}

		nics := getSystemInterfaces()
		port := protocol.DefaultPort

		fmt.Println("==========================================================")
		fmt.Println(" cworker Node Identity (本机节点名片)")
		fmt.Println("==========================================================")
		fmt.Printf(" Node Name:       %s\n", hostname)
		fmt.Printf(" Service Status:  %s\n", serviceStatus)
		fmt.Printf(" Default Port:    %d\n", port)
		fmt.Printf(" Security Token:  %s\n", tokenStr)
		fmt.Printf(" Token File:      %s\n", tokenPath)
		if len(nics) > 0 {
			fmt.Println(" Network Interfaces (所有活动网卡):")
			for _, nic := range nics {
				fmt.Printf("   - [%s] %s\n", nic.Name, nic.IP)
			}
		}
		fmt.Println("==========================================================")
		fmt.Println("\nTo connect from your control machine (首选计算机名/动态DNS):")
		fmt.Printf("  cw node add %s --token %s\n", hostname, tokenStr)

		if len(nics) > 0 {
			fmt.Println("\nAlternative connection commands by Network Interface (按指定网卡直连):")
			for _, nic := range nics {
				target := fmt.Sprintf("%s:%d", nic.IP, port)
				if nic.IsIPv6 {
					target = fmt.Sprintf("[%s]:%d", nic.IP, port)
				}
				fmt.Printf("  cw node add %s %s --token %s  # [%s]\n", hostname, target, tokenStr, nic.Name)
			}
		}
		fmt.Println()
		return nil
	},
}

func getSystemInterfaces() []InterfaceIP {
	var result []InterfaceIP
	ifaces, err := net.Interfaces()
	if err != nil {
		return result
	}

	for _, iface := range ifaces {
		// 忽略处于 DOWN 状态或 Loopback 接口
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					result = append(result, InterfaceIP{
						Name:   iface.Name,
						IP:     ip4.String(),
						IsIPv6: false,
					})
				} else if ip6 := ipnet.IP.To16(); ip6 != nil && !ip6.IsLinkLocalUnicast() {
					result = append(result, InterfaceIP{
						Name:   iface.Name + " (IPv6)",
						IP:     ip6.String(),
						IsIPv6: true,
					})
				}
			}
		}
	}
	return result
}

func init() {
	showCmd.Flags().BoolVar(&showRefresh, "refresh", false, "重新生成并轮换本机的 Ed25519 安全 Token")
	RootCmd.AddCommand(showCmd)
}
