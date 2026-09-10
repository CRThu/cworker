//go:build windows

package process

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	kernel32                 = windows.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")

	sysMu          sync.Mutex
	lastIdleTime   int64
	lastKernelTime int64
	lastUserTime   int64
	lastSampleTime time.Time
	lastCPUPercent float64
)

// GetSystemMemory 获取系统整机物理内存总量与空闲量 (MB)
func GetSystemMemory() (freeMB, totalMB uint64, err error) {
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))

	ret, _, callErr := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if ret == 0 {
		return 0, 0, callErr
	}

	freeMB = ms.AvailPhys / (1024 * 1024)
	totalMB = ms.TotalPhys / (1024 * 1024)
	return freeMB, totalMB, nil
}

// GetSystemCPUPercent 获取系统整体瞬时 CPU 负载百分比
func GetSystemCPUPercent() float64 {
	sysMu.Lock()
	defer sysMu.Unlock()

	var idle, kernel, user windows.Filetime
	ret, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret == 0 {
		return 0.0
	}

	currentIdle := int64(idle.HighDateTime)<<32 | int64(idle.LowDateTime)
	currentKernel := int64(kernel.HighDateTime)<<32 | int64(kernel.LowDateTime)
	currentUser := int64(user.HighDateTime)<<32 | int64(user.LowDateTime)

	if lastSampleTime.IsZero() {
		lastIdleTime = currentIdle
		lastKernelTime = currentKernel
		lastUserTime = currentUser
		lastSampleTime = time.Now()
		return 0.0
	}

	deltaIdle := currentIdle - lastIdleTime
	deltaKernel := currentKernel - lastKernelTime
	deltaUser := currentUser - lastUserTime

	total := deltaKernel + deltaUser
	if total <= 0 {
		return lastCPUPercent
	}

	// 在 Windows 下，KernelTime 已经包含了 IdleTime，故实际繁忙时间为 total - deltaIdle
	busy := total - deltaIdle
	percent := float64(busy) / float64(total) * 100.0
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}

	lastIdleTime = currentIdle
	lastKernelTime = currentKernel
	lastUserTime = currentUser
	lastSampleTime = time.Now()
	lastCPUPercent = percent

	return percent
}
