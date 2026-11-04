//go:build windows

package ipc

import (
	"fmt"
	"log"
	"net"
	"os/user"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// 管道安全描述符由 pipe_sddl.go 提供（不带 build tag，便于在 Linux CI 上
// 直接断言 ACL 字符串契约——Windows 真机无法在 CI 里跑）。

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

func listenPipe(name string) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptor(sid),
	})
	if err == nil {
		return ln, nil
	}
	// 不能让完整性标签设置失败演变成「daemon 起不来」（用户侧就是打不出中文）。
	// 记录并降级；排障时按这条日志判断 AppContainer 场景为何不可用。
	log.Printf("警告: 设置命名管道完整性标签失败（%v），降级为仅 DACL；"+
		"开始菜单/搜索框等低完整性宿主可能无法写入", err)
	return winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptorNoIntegrity(sid),
	})
}

func dialPipe(name string) (net.Conn, error) {
	return winio.DialPipe(name, nil)
}
