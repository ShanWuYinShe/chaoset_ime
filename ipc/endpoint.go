package ipc

import (
	"bufio"
	"fmt"
	"net"
)

// Endpoint 描述 daemon 的本地 IPC 端点。传输与协议解耦：帧格式（4 字节大端
// 长度 + JSON）不变，只替换底层传输。本包只保留**客户端**侧：daemon 已由
// Rust crates/chaosetd 实现，服务端 Listen 随之删除。
//
//	Network = "unix"：Address 为 Unix domain socket 路径（macOS/Linux）
//	Network = "pipe"：Address 为 Windows 命名管道名（如 \\.\\pipe\\chaoset-<SID>）
type Endpoint struct {
	Network string
	Address string
}

// String 用于日志/错误信息，不参与协议。
func (e Endpoint) String() string {
	return e.Network + ":" + e.Address
}

// DialEndpoint 连接 endpoint 并返回可复用的 Client。
func DialEndpoint(e Endpoint) (*Client, error) {
	conn, err := dialEndpoint(e)
	if err != nil {
		return nil, fmt.Errorf("连接输入法 daemon 失败（%s）: %w", e, err)
	}
	return &Client{conn: conn, br: bufio.NewReader(conn)}, nil
}

func dialEndpoint(e Endpoint) (net.Conn, error) {
	switch e.Network {
	case "unix":
		return net.Dial("unix", e.Address)
	case "pipe":
		return dialPipe(e.Address)
	default:
		return nil, fmt.Errorf("不支持的 IPC 传输 %q（支持 unix/pipe）", e.Network)
	}
}
