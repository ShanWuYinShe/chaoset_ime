//go:build windows

package ipc

import (
	"fmt"
	"net"
	"os/user"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

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

// pipeSecurityDescriptor 生成命名管道 ACL：当前用户 + SYSTEM 完全控制，
// ALL APPLICATION PACKAGES 读写（开始菜单/搜索框等 AppContainer 宿主需要连
// daemon；与旧 TCP 回环方案相比，仍拒绝其他本地用户）。
func pipeSecurityDescriptor(sid string) string {
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;%s)(A;;GRGW;;;S-1-15-2-1)", sid)
}

func listenPipe(name string) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptor(sid),
	})
}

func dialPipe(name string) (net.Conn, error) {
	return winio.DialPipe(name, nil)
}
