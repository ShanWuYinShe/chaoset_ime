package main

import (
	"net"
	"testing"

	"chaoset.ime/engine"
	"chaoset.ime/ipc"
)

// TestBuildControllerTCP 证明 -tcp 路径能连上真实 IPC daemon 并完成一次组词。
//
// Windows 上没有 Unix 套接字，CLI 只能走 TCP（2026-10-31 新增）；这里用真实
// ipc.Server + TCP 监听覆盖 dial 与协议链路，防止以后又把 network 写死成 unix。
func TestBuildControllerTCP(t *testing.T) {
	dict, _, err := engine.LoadDictFilesLenient([]string{"../../data/dict_common.tsv"})
	if err != nil {
		t.Fatalf("加载词库失败: %v", err)
	}
	user, _, err := engine.NewUserDictLenient("")
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听 TCP 失败: %v", err)
	}
	srv := ipc.NewServer(ln, dict, user)
	go func() { _ = srv.Serve() }()
	defer srv.Close()

	ctrl, closeFn, err := buildController("", ln.Addr().String(), nil, "", false, false, "")
	if err != nil {
		t.Fatalf("buildController(-tcp) 失败: %v", err)
	}
	defer closeFn()

	comp, err := ctrl.Input("nihao")
	if err != nil {
		t.Fatalf("经 TCP 组词失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatalf("经 TCP 组词没有候选: %+v", comp)
	}
}
