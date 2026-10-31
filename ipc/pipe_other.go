//go:build !windows

package ipc

import (
	"fmt"
	"net"
)

// DefaultPipeName 在非 Windows 平台不可用（Unix 用 socket 路径）。
func DefaultPipeName() (string, error) {
	return "", fmt.Errorf("命名管道仅 Windows 支持")
}

func listenPipe(string) (net.Listener, error) {
	return nil, fmt.Errorf("命名管道仅 Windows 支持")
}

func dialPipe(string) (net.Conn, error) {
	return nil, fmt.Errorf("命名管道仅 Windows 支持")
}
