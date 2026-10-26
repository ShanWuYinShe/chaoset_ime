//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// lockHandle 是单实例锁的句柄；release 幂等，可安全多次调用。
type lockHandle struct {
	f *os.File
}

func (l *lockHandle) release() {
	if l == nil || l.f == nil {
		return
	}
	// 关闭文件描述符即释放 flock（内核随进程退出也会自动释放）
	_ = l.f.Close()
	l.f = nil
}

// acquireSingleton 保证同一套接字路径只允许一个 chaosetd 实例。
//
// 为什么必须加这一层（2026-10-26 实测）：daemon 在 net.Listen 之前会
// os.Remove(socket) 清理「上次异常退出残留」的套接字文件，但这个删除
// **区分不出「残留文件」和「另一个正在运行的 daemon 的套接字」**。
// 于是第二个实例启动时会把活着的 daemon 的套接字 unlink 掉，再同样成功
// 监听同名路径——两个进程都不报错、都认为自己是唯一实例。后果：
//   - 活动监视器里出现多个 chaosetd；
//   - 旧进程的套接字已被 unlink，任何客户端都连不上它，却仍在跑；
//   - 两个进程各自持有 UserDict 内存态并交替落盘，学习数据互相覆盖。
//
// 锁文件放在套接字旁边（<socket>.lock）。flock 由内核在进程退出（含崩溃、
// 被 kill -9）时释放，所以残留的 .lock 文件不会阻塞下一次启动，不需要
// 额外的 PID 存活检查或过期清理。
func acquireSingleton(socket string) (*lockHandle, error) {
	lockPath := socket + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开锁文件 %s 失败: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("已有 chaosetd 在运行（%s 被占用），本次启动已放弃", lockPath)
		}
		return nil, fmt.Errorf("锁定 %s 失败: %w", lockPath, err)
	}
	return &lockHandle{f: f}, nil
}
