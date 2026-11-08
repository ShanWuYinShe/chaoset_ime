package ipc

// shutdown_test.go —— Server.Close 连接生命周期与 Client 默认超时回归
// （2026-11-08 审计 GO-09）。

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chaoset.ime/engine"
)

// TestClientCallTimeout：daemon 卡死（能读请求但不回包）时普通调用必须有上限，
// 不能无限阻塞（此前只有 Ping 有 250ms 上限）。
func TestClientCallTimeout(t *testing.T) {
	old := defaultCallTimeout
	defaultCallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { defaultCallTimeout = old })

	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	c := &Client{conn: clientConn, br: bufio.NewReader(clientConn)}
	defer c.Close()

	serverRead := make(chan struct{})
	go func() {
		defer close(serverRead)
		var req Request
		_ = readMessage(serverConn, &req) // 读走请求，但永不回包
	}()

	start := time.Now()
	if _, err := c.Input("a", false); err == nil {
		t.Fatal("daemon 不回包时期望返回超时错误")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("默认超时未生效：调用阻塞了 %v", elapsed)
	}
	<-serverRead
}

// TestServerCloseDrainsAndClosesConnections：Close 必须主动关闭已接受连接、
// 等 handler 退出再保存（旧实现只关 listener，Save 后仍有会话可能写学习数据），
// 且可重复调用。
func TestServerCloseDrainsAndClosesConnections(t *testing.T) {
	// macOS sockaddr_un 上限 104 字节：t.TempDir() 路径太长会 bind EINVAL，
	// 用短前缀的临时目录（与其他 ipc 测试一致）。
	dir, err := os.MkdirTemp("", "ipc")
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	// user 传 nil：本测试只验证连接生命周期；Save 顺序由退出流程与实现注释保证。
	srv := NewServer(ln, dict, nil)
	go func() { _ = srv.Serve() }()

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer client.Close()
	if _, err := client.Input("a", false); err != nil {
		t.Fatalf("关闭前正常请求失败: %v", err)
	}

	closed := make(chan error, 1)
	go func() { closed <- srv.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close 未在 5s 内完成（handler 未被唤醒/退出？）")
	}

	if _, err := client.Input("a", false); err == nil {
		t.Fatal("Close 之后旧连接应已被服务端关闭")
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close 应幂等，第二次返回: %v", err)
	}
}
