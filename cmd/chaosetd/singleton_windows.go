//go:build windows

package main

// acquireSingleton 在 Windows 上是空操作。
//
// 与 Unix 不同，Windows 壳经 TCP 回环连 daemon（-tcp 127.0.0.1:7949），
// 端口绑定本身由系统保证互斥：第二个实例 net.Listen 会直接拿到
// WSAEADDRINUSE 并退出，不存在 Unix 下「先 unlink 再监听、两个进程都
// 成功」的问题。因此这里不需要额外的锁文件。
//
// 若将来改用命名管道（会话隔离更干净，README 已列为待办），届时在此
// 实现独占创建即可，调用方无需改动。
type lockHandle struct {
	f struct{}
}

func (l *lockHandle) release() {}

func acquireSingleton(socket string) (*lockHandle, error) {
	return &lockHandle{}, nil
}
