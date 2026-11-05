//go:build windows

package ipc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// ensureUIHost 由 daemon 代 TIP 拉起同目录的单例 UI 宿主（chaoset-ui.exe）。
//
// 为什么需要 daemon 兜底：AppContainer 宿主（Windows 开始菜单/搜索）里的 TIP
// 不能 CreateProcess（子进程会继承 AC 身份），而设置窗必须由中等完整性进程
// 创建；daemon 运行在同一用户的普通完整性上下文，由它发起就绕开了 AC 限制。
// 只启动安装目录里固定的 exe，不接受调用方给的任意路径/参数。
func ensureUIHost() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位 daemon 路径失败: %w", err)
	}
	uiPath := filepath.Join(filepath.Dir(exePath), "chaoset-ui.exe")
	if _, statErr := os.Stat(uiPath); statErr != nil {
		return fmt.Errorf("找不到 chaoset-ui.exe: %w", statErr)
	}
	cmd := exec.Command(uiPath)
	// DETACHED_PROCESS：UI 宿主不继承 daemon 的控制台/进程组，也不随 daemon
	// 的作业对象（若存在）一起被杀；正常退出由 UI 宿主自己的消息循环处理。
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 chaoset-ui.exe 失败: %w", err)
	}
	// 不等待退出：UI 宿主是常驻进程，daemon 只负责发起。
	_ = cmd.Process.Release()
	return nil
}
