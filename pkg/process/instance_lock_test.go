//go:build windows

package process

import (
	"testing"
)

func TestAcquireInstanceLock(t *testing.T) {
	lockName := "test_cworker_single_instance_lock"

	// 第一次获取应该成功
	lock1, err := AcquireInstanceLock(lockName)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer lock1.Release()

	// 第二次获取相同 lockName 应该失败（被单例检测拦截）
	lock2, err := AcquireInstanceLock(lockName)
	if err == nil {
		lock2.Release()
		t.Fatal("expected second acquire to fail, but it succeeded")
	}

	// 释放第一次的锁
	lock1.Release()

	// 释放后重新获取应该成功
	lock3, err := AcquireInstanceLock(lockName)
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	lock3.Release()
}
