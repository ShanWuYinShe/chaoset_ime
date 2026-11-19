// chaoset-cli 是阶段 0 的终端测试台：在终端里模拟击键流程，
// 验证引擎与 IPC 链路，不涉及任何图形界面。
// 默认本地内嵌引擎；-socket 指定 Unix 套接字、-pipe 走 Windows 命名管道
// （按当前用户 SID 自动命名）连接 chaosetd。
package main

import (
	"os"

	"chaoset.ime/engine"
	"chaoset.ime/internal/tools/client"
)

func main() {
	os.Exit(client.Main(os.Args[1:]))
}

func buildController(socket string, usePipe bool, dicts []string, userPath string, fuzzy bool, trad bool, sp string) (engine.Controller, func(), error) {
	return client.BuildController(socket, usePipe, dicts, userPath, fuzzy, trad, sp)
}

func printComposition(comp engine.Composition) {
	client.PrintComposition(comp, os.Stdout)
}
