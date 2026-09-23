package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// 验证 cw rm 在删除递归目录期间若被 Context 取消（模拟 Ctrl+C），优雅中断并返回退出码 130
func TestCmd_Rm_ContextCancel_ReturnsExitCode130(t *testing.T) {
	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "rm_target_tree")
	_ = os.MkdirAll(targetDir, 0755)

	// 创建 30 个小文件
	for i := 1; i <= 30; i++ {
		_ = os.WriteFile(filepath.Join(targetDir, fmt.Sprintf("file_%02d.txt", i)), []byte("to be deleted"), 0644)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 执行前立即取消，确保稳定触发中断验证

	testCmd := &cobra.Command{
		Use:  rmCmd.Use,
		RunE: rmCmd.RunE,
	}
	testCmd.SetContext(ctx)
	testCmd.Flags().AddFlagSet(rmCmd.Flags())
	testCmd.Flags().Set("recursive", "true")
	testCmd.Flags().Set("yes", "true")

	err := testCmd.RunE(testCmd, []string{targetDir})
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}

	// 断言退出码为 130
	if ec, ok := err.(*ExitError); ok {
		if ec.Code != 130 {
			t.Fatalf("expected exit code 130 on cancellation, got %d", ec.Code)
		}
	} else {
		t.Fatalf("expected *ExitError, got %T: %v", err, err)
	}
}
