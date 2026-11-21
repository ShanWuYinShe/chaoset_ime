//go:build windows

package ipc

import (
	"fmt"
	"net"
	"os/user"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// daemon 已由 Rust 实现（crates/chaosetd）：命名管道服务端与 ACL 在 Rust 侧，
// 本文件只负责**客户端**按同一规则算名字并拨号（guests 无需知道 ACL）。

// pipePrefix 是命名管道的固定前缀；后缀为当前用户 SID，保证同一用户的所有
// 壳进程能算出同一个名字，不同用户互不干扰。
const pipePrefix = `\\.\pipe\chaoset-`

// currentUserSID 返回当前进程用户 SID（如 S-1-5-21-...）。
func currentUserSID() (string, error) {
	token := windows.GetCurrentProcessToken()
	tu, err := token.GetTokenUser()
	if err == nil && tu != nil && tu.User.Sid != nil {
		return tu.User.Sid.String(), nil
	}
	// 极少数受限环境拿不到 token，退回 os/user（Windows 上 Uid 即 SID）。
	u, uerr := user.Current()
	if uerr != nil {
		return "", fmt.Errorf("获取当前用户 SID 失败: %v（token: %v）", uerr, err)
	}
	return u.Uid, nil
}

// DefaultPipeName 返回当前用户的命名管道名。Windows 壳（C++）用同样规则
// 计算，无需端口文件或发现机制。
func DefaultPipeName() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return pipePrefix + sid, nil
}

func dialPipe(name string) (net.Conn, error) {
	return winio.DialPipe(name, nil)
}
