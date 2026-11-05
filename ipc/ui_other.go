//go:build !windows

package ipc

import "errors"

// ensureUIHost 仅 Windows 需要（单例 UI 宿主 chaoset-ui.exe）。其它平台没有
// 该进程模型，壳也不会调用该 IPC 方法；返回明确错误便于测试断言。
func ensureUIHost() error {
	return errors.New("单例 UI 宿主仅 Windows 支持")
}
