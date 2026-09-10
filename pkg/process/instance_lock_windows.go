//go:build windows

package process

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SingleInstanceLock 封装 Win32 命名互斥体单例锁
type SingleInstanceLock struct {
	handle windows.Handle
}

// AcquireInstanceLock 尝试获取本地 Worker 进程单例互斥体。
// 优先使用 Global\ 命名空间跨 Session 互斥（使后台 Windows Service 与交互式终端实例相互感知），
// 并赋予宽松的 DACL 以允许全权限上下文互通。若权限受限则自动回退至 Local\ 命名空间。
func AcquireInstanceLock(lockName string) (*SingleInstanceLock, error) {
	// 构造允许所有人 (World / Everyone) 访问的 DACL 安全描述符
	var sa *windows.SecurityAttributes
	if sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)"); err == nil {
		sa = &windows.SecurityAttributes{
			Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
			SecurityDescriptor: sd,
			InheritHandle:      0,
		}
	}

	// 1. 尝试 Global\ 跨会话互斥体
	globalName := "Global\\" + lockName
	if lock, err := tryCreateNamedMutex(globalName, sa); err == nil {
		return lock, nil
	} else if isAlreadyRunning(err) {
		return nil, fmt.Errorf("another cworker instance is already running on this system (Global mutex active)")
	}

	// 2. 若创建 Global 互斥体失败（通常因非管理员权限缺乏 SeCreateGlobalPrivilege），回退至 Local\
	localName := "Local\\" + lockName
	if lock, err := tryCreateNamedMutex(localName, sa); err == nil {
		return lock, nil
	} else if isAlreadyRunning(err) {
		return nil, fmt.Errorf("another cworker instance is already running in current session (Local mutex active)")
	} else {
		return nil, fmt.Errorf("failed to acquire instance mutex: %w", err)
	}
}

func tryCreateNamedMutex(fullName string, sa *windows.SecurityAttributes) (*SingleInstanceLock, error) {
	namePtr, err := windows.UTF16PtrFromString(fullName)
	if err != nil {
		return nil, err
	}

	// 调用 CreateMutexW 创建互斥体并立即尝试获取所有权 (bInitialOwner = true)
	hMutex, err := windows.CreateMutex(sa, true, namePtr)
	if err != nil {
		// 若返回 ERROR_ALREADY_EXISTS，系统虽然返回有效句柄，但需要立即关闭并阻断
		if err == syscall.Errno(windows.ERROR_ALREADY_EXISTS) {
			if hMutex != 0 {
				_ = windows.CloseHandle(hMutex)
			}
			return nil, syscall.Errno(windows.ERROR_ALREADY_EXISTS)
		}
		// 若返回拒绝访问，尝试以只读同步权限探查是否已有该互斥体运行
		if err == syscall.Errno(windows.ERROR_ACCESS_DENIED) {
			if hExisting, openErr := windows.OpenMutex(windows.SYNCHRONIZE, false, namePtr); openErr == nil {
				_ = windows.CloseHandle(hExisting)
				return nil, syscall.Errno(windows.ERROR_ALREADY_EXISTS)
			}
		}
		return nil, err
	}

	return &SingleInstanceLock{handle: hMutex}, nil
}

func isAlreadyRunning(err error) bool {
	return err == syscall.Errno(windows.ERROR_ALREADY_EXISTS)
}

// Release 释放命名互斥体并关闭句柄
func (l *SingleInstanceLock) Release() {
	if l.handle != 0 {
		_ = windows.ReleaseMutex(l.handle)
		_ = windows.CloseHandle(l.handle)
		l.handle = 0
	}
}
