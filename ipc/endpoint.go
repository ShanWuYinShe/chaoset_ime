package ipc

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
)

// Endpoint 描述 daemon 的本地 IPC 端点。传输与协议解耦：帧格式（4 字节大端
// 长度 + JSON）不变，只替换底层传输。
//
//	Network = "unix"：Address 为 Unix domain socket 路径（macOS/Linux）
//	Network = "pipe"：Address 为 Windows 命名管道名（如 \\.\\pipe\\chaoset-<SID>）
//	Network = "tcp" ：Address 为 127.0.0.1:port（仅兼容旧壳/测试，逐步淘汰）
type Endpoint struct {
	Network string
	Address string
}

// String 用于日志/错误信息，不参与协议。
func (e Endpoint) String() string {
	return e.Network + ":" + e.Address
}

// Listen 在 endpoint 上监听。Unix socket/TCP 走 net.Listen；Windows 命名管道
// 走 go-winio（ACL 只允许当前用户 + SYSTEM + AppContainer 包）。
func Listen(e Endpoint) (net.Listener, error) {
	switch e.Network {
	case "unix":
		ln, err := net.Listen("unix", e.Address)
		if err != nil {
			return nil, err
		}
		// 套接字默认权限来自 umask（常见 0755），同机其它用户可连接 daemon
		// 旁听键盘输入；显式收紧到 0600。chmod 失败不阻断启动（个别文件系统
		// 不支持），只记警告。
		if chmodErr := os.Chmod(e.Address, 0o600); chmodErr != nil {
			log.Printf("警告: 设置套接字权限失败（%s）: %v", e.Address, chmodErr)
		}
		return ln, nil
	case "tcp":
		return net.Listen("tcp", e.Address)
	case "pipe":
		return listenPipe(e.Address)
	default:
		return nil, fmt.Errorf("不支持的 IPC 传输 %q（支持 unix/pipe/tcp）", e.Network)
	}
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
	case "unix", "tcp":
		return net.Dial(e.Network, e.Address)
	case "pipe":
		return dialPipe(e.Address)
	default:
		return nil, fmt.Errorf("不支持的 IPC 传输 %q（支持 unix/pipe/tcp）", e.Network)
	}
}
