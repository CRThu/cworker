//go:build windows

package process

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/windows/registry"
)

var (
	cachedOSVersion string
	cachedOSOnce    sync.Once
)

// GetWindowsOSVersion 获取格式化的 Windows 宿主操作系统版本 (例如 "Windows 10 22H2", "Windows 11 23H2")
// 微秒级注册表直读，热路径带内存缓存，零子进程启动开销
func GetWindowsOSVersion() string {
	cachedOSOnce.Do(func() {
		cachedOSVersion = detectWindowsOSVersion()
	})
	return cachedOSVersion
}

func detectWindowsOSVersion() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "Windows"
	}
	defer k.Close()

	productName, _, _ := k.GetStringValue("ProductName")
	displayVersion, _, _ := k.GetStringValue("DisplayVersion")
	releaseId, _, _ := k.GetStringValue("ReleaseId")
	buildNumberStr, _, _ := k.GetStringValue("CurrentBuildNumber")
	if buildNumberStr == "" {
		buildNumberStr, _, _ = k.GetStringValue("CurrentBuild")
	}

	buildNum, _ := strconv.Atoi(buildNumberStr)
	return FormatWindowsVersion(productName, displayVersion, releaseId, buildNum)
}

// FormatWindowsVersion 根据注册表元数据纯逻辑格式化 Windows 系统版本 (纯函数，便于高覆盖测试)
func FormatWindowsVersion(productName, displayVersion, releaseId string, buildNumber int) string {
	base := "Windows"

	if strings.Contains(productName, "Server") {
		base = "Windows Server"
		for _, yr := range []string{"2025", "2022", "2019", "2016", "2012 R2", "2012"} {
			if strings.Contains(productName, yr) {
				base = "Windows Server " + yr
				break
			}
		}
	} else if buildNumber >= 22000 || strings.Contains(productName, "Windows 11") {
		base = "Windows 11"
	} else if buildNumber >= 10240 || strings.Contains(productName, "Windows 10") {
		base = "Windows 10"
	} else if strings.TrimSpace(productName) != "" {
		base = strings.TrimSpace(productName)
	}

	ver := strings.TrimSpace(displayVersion)
	if ver == "" {
		ver = strings.TrimSpace(releaseId)
	}

	if ver != "" {
		return base + " " + ver
	}
	if buildNumber > 0 {
		return fmt.Sprintf("%s (Build %d)", base, buildNumber)
	}
	return base
}
