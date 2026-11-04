//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// acquireSingleton 在 Windows 上用会话级命名互斥体保证单实例。
//
// 不让互斥依赖端点：安装器 Run 项、开机自启动与设置窗各自拉起 daemon 时
// 参数可能不同（管道名、词库路径），只按端点判断会留下两个进程；会话级
// 命名互斥体与参数无关。
// `Local\` 命名空间按登录会话隔离，Fast User Switching 下每个用户仍有
// 自己的 daemon，不会跨用户误杀。
const daemonMutexName = `Local\ChaosetDaemon`

type lockHandle struct {
	h windows.Handle
}

func (l *lockHandle) release() {
	if l == nil || l.h == 0 {
		return
	}
	_ = windows.CloseHandle(l.h)
	l.h = 0
}

func acquireSingleton(_ string) (*lockHandle, error) {
	h, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(daemonMutexName))
	if err != nil {
		if err == windows.ERROR_ALREADY_EXISTS {
			if h != 0 {
				_ = windows.CloseHandle(h)
			}
			return nil, fmt.Errorf("已有 chaosetd 在运行（单实例互斥体 %s 被占用），本次启动已放弃", daemonMutexName)
		}
		return nil, fmt.Errorf("创建单实例互斥体失败: %w", err)
	}
	return &lockHandle{h: h}, nil
}
