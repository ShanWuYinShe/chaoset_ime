//go:build !windows

package ipc

import "errors"

// ensureUIHost 仅 Windows 需要（单例 UI 宿主 chaoset-ui.exe）。其它平台没有
// 该进程模型，壳也不会调用该 IPC 方法；返回明确错误便于测试断言。
// settings 参数在非 Windows 上无意义（没有"让 UI 宿主直接弹设置窗"这条路径）。
func ensureUIHost(settings bool) error {
	_ = settings
	return errors.New("单例 UI 宿主仅 Windows 支持")
}
