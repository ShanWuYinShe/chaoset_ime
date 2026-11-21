// chaoset-cli 是终端测试台：在终端里模拟击键流程，验证 daemon 的 IPC 链路，
// 不涉及任何图形界面。
//
// 本地内嵌引擎已随 Go 引擎退役删除：必须用 -socket 指定 Unix 套接字、
// 或 -pipe 走 Windows 命名管道（按当前用户 SID 自动命名）连接 chaosetd。
package main

import (
	"os"

	"chaoset.ime/internal/tools/client"
	"chaoset.ime/ipc"
)

func main() {
	os.Exit(client.Main(os.Args[1:]))
}

func buildController(socket string, usePipe bool) (ipc.Controller, func(), error) {
	return client.BuildController(socket, usePipe)
}

func printComposition(comp ipc.Composition) {
	client.PrintComposition(comp, os.Stdout)
}
