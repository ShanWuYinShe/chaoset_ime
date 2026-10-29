package ipc

import (
	"bufio"
	"net"
	"testing"
	"time"
)

// 回归背景（2026-10-30 审计）：ipc.Client 的 call 没有任何 deadline，
// 而 Ping 的文档契约是「daemon 未就绪时调用方应降级放行按键，而不是
// 等待」——没有 deadline 时这个契约是假的：daemon 进程还在、socket 也
// 活着但不回包，调用方会永久阻塞。
//
// 本测试用 net.Pipe 模拟「只接受连接、永不回包」的 daemon。
func TestPingTimesOutWhenDaemonSilent(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close() // 服务端只持有连接，不读不写
	c := &Client{conn: clientConn, br: bufio.NewReader(clientConn)}

	start := time.Now()
	err := c.Ping()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("daemon 不回包时 Ping 必须超时报错，不能永久阻塞")
	}
	// 上界：远小于用户可感知卡顿的极限，且绝不能无限等待
	if elapsed > 2*time.Second {
		t.Fatalf("Ping 应受 %v 截止时间约束，实际阻塞 %v", pingTimeout, elapsed)
	}
	// 下界：必须真的等过截止时间（而不是立刻返回连接错误）
	if elapsed < pingTimeout/2 {
		t.Fatalf("Ping 应等满截止时间才失败，实际仅 %v", elapsed)
	}

	// 超时后连接必须被关闭：流里可能残留半个帧，继续用会串包
	if _, werr := clientConn.Write([]byte{0}); werr == nil {
		t.Fatal("Ping 超时后应关闭连接，避免在错位的流上继续收发")
	}
}

// 正常路径不受影响：daemon 立刻回包时 Ping 必须成功，且不残留 deadline。
func TestPingSucceedsAndClearsDeadline(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	c := &Client{conn: clientConn, br: bufio.NewReader(clientConn)}

	// 极简服务端：读一个帧，回 {"id":1,"result":"pong"}
	go func() {
		br := bufio.NewReader(serverConn)
		var req Request
		if err := readMessage(br, &req); err != nil {
			return
		}
		_ = writeMessage(serverConn, Response{ID: req.ID, Result: []byte("\"pong\"")})
	}()

	if err := c.Ping(); err != nil {
		t.Fatalf("正常 Ping 应成功: %v", err)
	}
	// deadline 必须已清除：否则下一次调用会在同一截止时间上立刻超时。
	// 这里用一个明确超过 pingTimeout 的 sleep 之后再调用验证。
	time.Sleep(pingTimeout + 100*time.Millisecond)
	go func() {
		br := bufio.NewReader(serverConn)
		var req Request
		if err := readMessage(br, &req); err != nil {
			return
		}
		_ = writeMessage(serverConn, Response{ID: req.ID, Result: []byte("\"pong\"")})
	}()
	if err := c.Ping(); err != nil {
		t.Fatalf("Ping 不应残留 deadline（第二次调用失败）: %v", err)
	}
}
