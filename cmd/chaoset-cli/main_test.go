package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"chaoset.ime/engine"
	"chaoset.ime/ipc"
)

// TestBuildControllerPipeUnsupportedOnUnix：-pipe 仅 Windows 支持；Unix 上必须
// 明确报错，而不是静默回退到本地引擎。
func TestBuildControllerPipeUnsupportedOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 -pipe 走真实命名管道，需 daemon；本用例只测 Unix 报错")
	}
	if _, _, err := buildController("", true, nil, "", false, false, ""); err == nil {
		t.Fatal("Unix 上 -pipe 应报错")
	}
}

// TestBuildControllerUnixSocket 证明 -socket 路径能连上真实 IPC daemon 并完成
// 一次组词：用临时目录下的 Unix socket 起 ipc.Server，覆盖 dial 与协议链路，
// 防止以后又把 network 写死或漏传地址。
func TestBuildControllerUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket 用例只在 macOS/Linux 上跑")
	}
	dict, _, err := engine.LoadDictFilesLenient([]string{"../../data/dict_common.tsv"})
	if err != nil {
		t.Fatalf("加载词库失败: %v", err)
	}
	user, _, err := engine.NewUserDictLenient("")
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}
	ln, err := ipc.Listen(ipc.Endpoint{Network: "unix", Address: shortSocketPath(t)})
	if err != nil {
		t.Fatalf("监听 Unix socket 失败: %v", err)
	}
	srv := ipc.NewServer(ln, dict, user)
	go func() { _ = srv.Serve() }()
	defer srv.Close()

	ctrl, closeFn, err := buildController(ln.Addr().String(), false, nil, "", false, false, "")
	if err != nil {
		t.Fatalf("buildController(-socket) 失败: %v", err)
	}
	defer closeFn()

	comp, err := ctrl.Input("nihao", false)
	if err != nil {
		t.Fatalf("经 Unix socket 组词失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatalf("经 Unix socket 组词没有候选: %+v", comp)
	}
}

// shortSocketPath 生成短路径的套接字名：Unix socket 路径上限约 104 字节
// （macOS），t.TempDir 的目录名含完整测试名，长测试名会 bind: invalid argument。
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "chaoset-cli")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}
