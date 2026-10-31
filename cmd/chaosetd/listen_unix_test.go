//go:build unix

package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"chaoset.ime/ipc"
)

// TestEndpointUnixRestrictsPermission 证明 Unix 套接字权限被收紧到 0600。
//
// 套接字默认权限来自 umask（常见 0755），同机其它用户可连接 daemon 收发
// 组词/上屏数据（等于键盘输入泄露）。2026-10-31 审计 P2 修复后这里断言
// 0600，防止以后有人把 chmod 删掉。
//
// 用 os.MkdirTemp 而不是 t.TempDir：Unix socket 路径上限约 104 字节，
// t.TempDir 在 macOS 上可能很长（/var/folders/...），会误报路径过长。
func TestEndpointUnixRestrictsPermission(t *testing.T) {
	dir, err := os.MkdirTemp("", "chaoset-sock")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "chaoset.sock")
	ln, err := ipc.Listen(ipc.Endpoint{Network: "unix", Address: path})
	if err != nil {
		t.Fatalf("ipc.Listen 失败: %v", err)
	}
	defer ln.Close()
	if _, ok := ln.(*net.UnixListener); !ok {
		t.Fatalf("期望 *net.UnixListener，得到 %T", ln)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 套接字失败: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("套接字权限 = %#o，期望 0600", got)
	}
}
