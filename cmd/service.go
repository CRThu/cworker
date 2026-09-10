package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"cworker/pkg/protocol"
	"cworker/pkg/worker"
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	svcPort       int
	svcWorkerName string
)

type serviceProgram struct {
	cfg    worker.Config
	w      *worker.Worker
	cancel context.CancelFunc
}

func (p *serviceProgram) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	w, err := worker.NewWorker(p.cfg)
	if err != nil {
		return err
	}
	p.w = w

	go func() {
		_ = p.w.Start(ctx)
	}()
	return nil
}

func (p *serviceProgram) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

func getDeployedExePath() (string, string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	binDir := filepath.Join(homeDir, protocol.DefaultDataDirName, "bin")
	targetExe := filepath.Join(binDir, "cw.exe")
	return binDir, targetExe
}

func getServiceConfig(targetExe string) *service.Config {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, protocol.DefaultDataDirName)

	args := []string{"worker", "--data-dir", dataDir}
	if svcPort != 0 {
		args = append(args, "--port", fmt.Sprintf("%d", svcPort))
	}
	if svcWorkerName != "" {
		args = append(args, "--name", svcWorkerName)
	}

	return &service.Config{
		Name:        "cworker",
		DisplayName: "cworker - Carrot Worker Distributed Agent",
		Description: "cworker 去中心化任务与原生进程治理服务",
		Executable:  targetExe,
		Arguments:   args,
	}
}

var (
	modUser32               = syscall.NewLazyDLL("user32.dll")
	procSendMessageTimeoutW = modUser32.NewProc("SendMessageTimeoutW")
)

const (
	hwndBroadcast   = 0xffff
	wmSettingChange = 0x001A
	smtoAbortIfHung = 0x0002
)

// notifyEnvironmentChange 广播 WM_SETTINGCHANGE 通知系统环境变量已更新
func notifyEnvironmentChange() {
	envPtr, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = procSendMessageTimeoutW.Call(
		uintptr(hwndBroadcast),
		uintptr(wmSettingChange),
		0,
		uintptr(unsafe.Pointer(envPtr)),
		uintptr(smtoAbortIfHung),
		5000,
		uintptr(unsafe.Pointer(&result)),
	)
}

// deployBinaryToUserDir 将当前 cw.exe 复制到 ~/.cworker/bin/cw.exe 以解耦开发编译与常驻运行
func deployBinaryToUserDir() (string, string, error) {
	binDir, targetExe := getDeployedExePath()
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", "", fmt.Errorf("create bin dir '%s' failed: %w", binDir, err)
	}

	currentExe, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("cannot get current executable path: %w", err)
	}

	// 若当前执行文件已经是目标部署副本，无需自我拷贝
	if strings.EqualFold(filepath.Clean(currentExe), filepath.Clean(targetExe)) {
		return binDir, targetExe, nil
	}

	srcFile, err := os.Open(currentExe)
	if err != nil {
		return "", "", fmt.Errorf("open current executable '%s' failed: %w", currentExe, err)
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(targetExe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", "", fmt.Errorf("deploy binary to '%s' failed (is service currently running? execute 'cw service stop' first): %w", targetExe, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return "", "", fmt.Errorf("copy executable content failed: %w", err)
	}

	return binDir, targetExe, nil
}

// addDirToUserPath 在当前用户的 HKCU\Environment\Path 中登记目标目录并广播生效
func addDirToUserPath(dir string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment failed: %w", err)
	}
	defer k.Close()

	val, valType, err := k.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("read Path failed: %w", err)
	}

	normTarget := strings.TrimRight(strings.ToLower(filepath.Clean(dir)), "\\/")
	paths := strings.Split(val, ";")
	for _, p := range paths {
		if strings.TrimRight(strings.ToLower(filepath.Clean(strings.TrimSpace(p))), "\\/") == normTarget {
			return nil // 已经在 PATH 中，保持原样
		}
	}

	newPath := val
	if newPath != "" && !strings.HasSuffix(newPath, ";") {
		newPath += ";"
	}
	newPath += dir

	if valType == registry.SZ {
		err = k.SetStringValue("Path", newPath)
	} else {
		err = k.SetExpandStringValue("Path", newPath)
	}
	if err != nil {
		return fmt.Errorf("write Path failed: %w", err)
	}

	notifyEnvironmentChange()
	return nil
}

// removeDirFromUserPath 卸载时从 HKCU\Environment\Path 移除目标目录并广播复原
func removeDirFromUserPath(dir string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	val, valType, err := k.GetStringValue("Path")
	if err != nil {
		return err
	}

	normTarget := strings.TrimRight(strings.ToLower(filepath.Clean(dir)), "\\/")
	paths := strings.Split(val, ";")
	var newPaths []string
	changed := false
	for _, p := range paths {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if strings.TrimRight(strings.ToLower(filepath.Clean(trimmed)), "\\/") == normTarget {
			changed = true
			continue
		}
		newPaths = append(newPaths, trimmed)
	}

	if !changed {
		return nil
	}

	newPath := strings.Join(newPaths, ";")
	if valType == registry.SZ {
		err = k.SetStringValue("Path", newPath)
	} else {
		err = k.SetExpandStringValue("Path", newPath)
	}
	if err != nil {
		return err
	}

	notifyEnvironmentChange()
	return nil
}

// configureFirewallRule 自动调用 netsh 配置放行规则
func configureFirewallRule(exePath string) error {
	_ = exec.Command("netsh.exe", "advfirewall", "firewall", "delete", "rule", "name=cworker").Run()

	cmd := exec.Command("netsh.exe", "advfirewall", "firewall", "add", "rule",
		"name=cworker",
		"dir=in",
		"action=allow",
		fmt.Sprintf("program=%s", exePath),
		"enable=yes",
		"description=cworker - Distributed Agent Service",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("firewall rule add failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeFirewallRule 卸载服务时清理防火墙入站规则
func removeFirewallRule() {
	_ = exec.Command("netsh.exe", "advfirewall", "firewall", "delete", "rule", "name=cworker").Run()
}

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "管理 Windows 系统常驻服务 (默认一键将当前机器作为 Worker 托管为 Windows Service)",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "部署并注册为 Windows 系统服务 (含副本隔离、SCM 自愈、防火墙自动放行与 PATH 注册)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1. 自动解耦并复制二进制到 ~/.cworker/bin/cw.exe
		binDir, targetExe, err := deployBinaryToUserDir()
		if err != nil {
			return err
		}
		fmt.Printf("[OK] 1. Binary deployed: %s\n", targetExe)

		// 2. 构造服务配置并安装
		svcCfg := getServiceConfig(targetExe)
		prg := &serviceProgram{}
		s, err := service.New(prg, svcCfg)
		if err != nil {
			return err
		}

		if err := s.Install(); err != nil {
			return fmt.Errorf("service install failed (please run as Administrator): %w", err)
		}
		fmt.Printf("[OK] 2. Windows Service '%s' registered.\n", svcCfg.Name)

		// 3. 配置 SCM 故障自愈策略：崩溃后 1000ms 自动重启
		scCmd := exec.Command("sc.exe", "failure", svcCfg.Name, "reset=", "86400", "actions=", "restart/1000/restart/1000/restart/1000")
		if out, err := scCmd.CombinedOutput(); err != nil {
			fmt.Printf("[WARN] Note: could not configure sc failure actions: %v (%s)\n", err, string(out))
		} else {
			fmt.Println("[OK] 3. Windows SCM failure recovery configured (1000ms auto-restart).")
		}

		// 4. 自动配置 Windows Defender 防火墙入站放行
		if err := configureFirewallRule(targetExe); err != nil {
			fmt.Printf("[WARN] Note: could not add firewall rule automatically: %v\n", err)
		} else {
			fmt.Println("[OK] 4. Windows Firewall inbound rule 'cworker' created.")
		}

		// 5. 自动注册到用户 PATH 环境变量
		if err := addDirToUserPath(binDir); err != nil {
			fmt.Printf("[WARN] Note: could not append to User PATH: %v\n", err)
		} else {
			fmt.Printf("[OK] 5. User PATH registered with '%s' (effective immediately).\n", binDir)
		}

		// 6. 自动预置或读取 Token 并打印连接提示
		homeDir, _ := os.UserHomeDir()
		dataDir := filepath.Join(homeDir, protocol.DefaultDataDirName)
		tokenStr, err := worker.LoadOrCreateToken(dataDir)
		if err != nil {
			return fmt.Errorf("load or create token failed: %w", err)
		}

		nodeName := svcWorkerName
		if nodeName == "" {
			nodeName, _ = os.Hostname()
		}

		fmt.Println("\n==========================================================")
		fmt.Printf(" [DEPLOY OK] Worker Service '%s' Installed Successfully!\n", svcCfg.Name)
		fmt.Printf(" - Executable:     %s\n", targetExe)
		fmt.Printf(" - Port:           %d\n", svcPort)
		fmt.Printf(" - Security Token: %s\n", tokenStr)
		fmt.Println("==========================================================")
		fmt.Println("\nStart service now with: cw service start")
		fmt.Println("\nTo connect this node from another machine, run on that machine:")
		fmt.Printf("  cw node add %s --token %s\n", nodeName, tokenStr)
		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "卸载 Windows 系统服务并逆向复原防火墙与环境变量",
	RunE: func(cmd *cobra.Command, args []string) error {
		binDir, targetExe := getDeployedExePath()

		s, err := service.New(&serviceProgram{}, getServiceConfig(targetExe))
		if err != nil {
			return err
		}
		_ = s.Stop()
		if err := s.Uninstall(); err != nil {
			return fmt.Errorf("service uninstall failed: %w", err)
		}
		fmt.Println("[OK] 1. Windows Service uninstalled.")

		// 清理防火墙规则
		removeFirewallRule()
		fmt.Println("[OK] 2. Windows Firewall rule 'cworker' removed.")

		// 逆向复原用户 PATH 环境变量
		if err := removeDirFromUserPath(binDir); err == nil {
			fmt.Println("[OK] 3. User PATH cleaned.")
		}

		return nil
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "启动 Windows 系统服务 (带单例与运行状态判断)",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, targetExe := getDeployedExePath()

		s, err := service.New(&serviceProgram{}, getServiceConfig(targetExe))
		if err != nil {
			return err
		}

		st, err := s.Status()
		if err == nil && st == service.StatusRunning {
			fmt.Println("[INFO] Service 'cworker' is already RUNNING.")
			return nil
		}

		if err := s.Start(); err != nil {
			return fmt.Errorf("start service failed: %w", err)
		}
		fmt.Println("[OK] Windows Service started successfully.")
		return nil
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "停止 Windows 系统服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, targetExe := getDeployedExePath()

		s, err := service.New(&serviceProgram{}, getServiceConfig(targetExe))
		if err != nil {
			return err
		}

		st, err := s.Status()
		if err == nil && st == service.StatusStopped {
			fmt.Println("[INFO] Service 'cworker' is already STOPPED.")
			return nil
		}

		if err := s.Stop(); err != nil {
			return fmt.Errorf("stop service failed: %w", err)
		}
		fmt.Println("[OK] Windows Service stopped.")
		return nil
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看 Windows 系统服务状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, targetExe := getDeployedExePath()

		s, err := service.New(&serviceProgram{}, getServiceConfig(targetExe))
		if err != nil {
			return err
		}
		st, err := s.Status()
		if err != nil {
			return err
		}
		switch st {
		case service.StatusRunning:
			fmt.Println("Service is: RUNNING")
		case service.StatusStopped:
			fmt.Println("Service is: STOPPED")
		default:
			fmt.Println("Service is: UNKNOWN")
		}
		return nil
	},
}

func init() {
	serviceInstallCmd.Flags().IntVarP(&svcPort, "port", "p", protocol.DefaultPort, "Worker 监听端口")
	serviceInstallCmd.Flags().StringVar(&svcWorkerName, "name", "", "节点自定义名称 (若为空则自动取本机计算机名)")

	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)
	serviceCmd.AddCommand(serviceStartCmd)
	serviceCmd.AddCommand(serviceStopCmd)
	serviceCmd.AddCommand(serviceStatusCmd)
	RootCmd.AddCommand(serviceCmd)
}
