//go:build !windows

package ipc

import "testing"

// 非 Windows 平台明确不支持 daemon 代拉起 UI 宿主：返回错误而不是静默成功。
// Windows 侧的真机行为由 release 冒烟与壳集成验证覆盖（Linux CI 只编译）。
func TestEnsureUIHostUnsupportedOnNonWindows(t *testing.T) {
	if err := ensureUIHost(); err == nil {
		t.Fatal("非 Windows 平台 ensureUIHost 应返回错误")
	}
}
