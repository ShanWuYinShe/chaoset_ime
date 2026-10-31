package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chaoset.ime/ipc"
)

const daemonTestDict = "你好\tni hao\t99\n"

// newDaemonTestDir 用短路径：macOS Unix socket 路径上限 104 字节，t.TempDir()
// 目录名含完整测试名，长测试名会 bind: invalid argument。
func newDaemonTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "chaosetd")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func writeDict(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写词库失败: %v", err)
	}
	return path
}

// waitDaemon 轮询连接 socket，返回可用 client。
func waitDaemon(t *testing.T, sock string) *ipc.Client {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := ipc.Dial("unix", sock)
		if err == nil {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon 在 3s 内未就绪: %s", sock)
	return nil
}

// TestRunWithContextServesAndShutsDown：完整生命周期——启动、Ping/Input、
// 取消 context 后优雅退出、套接字清理。此前 run() 直接绑信号，无法在测试里
// 驱动启停，daemon 主流程覆盖率只有 12.8%。
func TestRunWithContextServesAndShutsDown(t *testing.T) {
	dir := newDaemonTestDir(t)
	sock := filepath.Join(dir, "s")
	dict := writeDict(t, dir, "dict.tsv", daemonTestDict)
	userPath := filepath.Join(dir, "user.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(ctx, sock, "", []string{dict}, userPath, false, false, "")
	}()

	client := waitDaemon(t, sock)
	defer client.Close()
	if err := client.Ping(); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
	comp, err := client.Input("nihao")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "你好" {
		t.Fatalf("候选不符预期: %+v", comp.Candidates)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runWithContext 退出错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runWithContext 未在 5s 内退出")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("退出后套接字应被清理，实际 err=%v", err)
	}
}

// TestRunWithContextRejectsBadConfig：启动期配置错误必须明确报错，不能
// 静默起一个打不出字的 daemon。
func TestRunWithContextRejectsBadConfig(t *testing.T) {
	dir := newDaemonTestDir(t)
	good := writeDict(t, dir, "good.tsv", daemonTestDict)
	empty := writeDict(t, dir, "empty.tsv", "")
	userPath := filepath.Join(dir, "user.json")

	t.Run("词库不存在", func(t *testing.T) {
		err := runWithContext(context.Background(), filepath.Join(dir, "s1"), "",
			[]string{filepath.Join(dir, "missing.tsv")}, userPath, false, false, "")
		if err == nil {
			t.Fatal("词库不存在时应报错")
		}
	})
	t.Run("词库为空", func(t *testing.T) {
		err := runWithContext(context.Background(), filepath.Join(dir, "s2"), "",
			[]string{empty}, userPath, false, false, "")
		if err == nil || !strings.Contains(err.Error(), "没有加载到任何词条") {
			t.Fatalf("空词库应报「没有加载到任何词条」，实际 %v", err)
		}
	})
	t.Run("TCP 端口非法", func(t *testing.T) {
		err := runWithContext(context.Background(), "", "127.0.0.1:99999",
			[]string{good}, userPath, false, false, "")
		if err == nil || !strings.Contains(err.Error(), "监听") {
			t.Fatalf("非法 TCP 端口应报监听错误，实际 %v", err)
		}
	})
}
