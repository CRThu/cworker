//go:build windows

package process

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// JobObject 封装 Windows 内核级作业对象句柄与状态控制
type JobObject struct {
	mu     sync.Mutex
	handle windows.Handle
	name   string
	closed bool
}

// CreateJobObject 创建并初始化一个具有安全退出限制的 Windows 作业对象。
// 设定 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 保证主服务一旦关闭，内核强制终止其下所有子进程。
func CreateJobObject(name string) (*JobObject, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("invalid job name utf16: %w", err)
	}

	hJob, err := windows.CreateJobObject(nil, namePtr)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject failed: %w", err)
	}

	// 配置限制：开启 KILL_ON_JOB_CLOSE
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE

	ret, _, callErr := windows.NewLazyDLL("kernel32.dll").NewProc("SetInformationJobObject").Call(
		uintptr(hJob),
		uintptr(windows.JobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Sizeof(info)),
	)
	if ret == 0 {
		windows.CloseHandle(hJob)
		return nil, fmt.Errorf("SetInformationJobObject failed: %v", callErr)
	}

	return &JobObject{
		handle: hJob,
		name:   name,
	}, nil
}

// AssignProcess 将目标进程的 Windows 句柄绑定进该作业对象
func (j *JobObject) AssignProcess(hProcess windows.Handle) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return fmt.Errorf("job object %s is closed", j.name)
	}

	if err := windows.AssignProcessToJobObject(j.handle, hProcess); err != nil {
		return fmt.Errorf("AssignProcessToJobObject failed: %w", err)
	}
	return nil
}

// Terminate 强制终结整个作业对象中的全部关联进程与子孙进程，物理零残留
func (j *JobObject) Terminate(exitCode uint32) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return nil
	}

	if err := windows.TerminateJobObject(j.handle, exitCode); err != nil {
		return fmt.Errorf("TerminateJobObject failed: %w", err)
	}
	return nil
}

// Win32 JOBOBJECT_BASIC_ACCOUNTING_INFORMATION 内存结构对齐
type jobObjectBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

const jobObjectBasicAccountingInformationClass = 1

// QueryMetrics 单次内核系统调用采集作业对象内整棵进程树的 CPU 耗时与峰值内存
func (j *JobObject) QueryMetrics() (cpuMs int64, memoryMB uint64, activeProcesses int, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return 0, 0, 0, fmt.Errorf("job object closed")
	}

	// 1. 查询基本会计信息 (包含 TotalKernelTime, TotalUserTime, ActiveProcesses)
	var basicInfo jobObjectBasicAccountingInformation
	var retLen uint32
	err = windows.QueryInformationJobObject(
		j.handle,
		jobObjectBasicAccountingInformationClass,
		uintptr(unsafe.Pointer(&basicInfo)),
		uint32(unsafe.Sizeof(basicInfo)),
		&retLen,
	)
	if err == nil {
		// 100-nanoseconds 转化为毫秒
		totalTicks := basicInfo.TotalUserTime + basicInfo.TotalKernelTime
		cpuMs = totalTicks / 10000
		activeProcesses = int(basicInfo.ActiveProcesses)
	}

	// 2. 查询扩展限制信息获取 PeakJobMemoryUsed
	var extInfo windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	err = windows.QueryInformationJobObject(
		j.handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&extInfo)),
		uint32(unsafe.Sizeof(extInfo)),
		&retLen,
	)
	if err == nil {
		memoryMB = uint64(extInfo.PeakJobMemoryUsed / (1024 * 1024))
	}

	return cpuMs, memoryMB, activeProcesses, nil
}

// Close 安全释放作业对象句柄
func (j *JobObject) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return nil
	}
	j.closed = true
	return windows.CloseHandle(j.handle)
}
