package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chaoset.ime/engine"
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
		c, err := ipc.DialEndpoint(ipc.Endpoint{Network: "unix", Address: sock})
		if err == nil {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon 在 3s 内未就绪: %s", sock)
	return nil
}

// runningDaemon 是一次测试内启动的 daemon 句柄；stop 负责取消 context 并
// 等待 runWithContext 真正退出，避免测试结束后 goroutine 还在写临时目录。
type runningDaemon struct {
	client *ipc.Client
	cancel context.CancelFunc
	errCh  <-chan error
}

func startDaemon(t *testing.T, sock, dict, userPath string) *runningDaemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(ctx, ipc.Endpoint{Network: "unix", Address: sock}, []string{dict}, userPath, false, false, "")
	}()
	client := waitDaemon(t, sock)
	t.Cleanup(func() { _ = client.Close() })
	return &runningDaemon{client: client, cancel: cancel, errCh: errCh}
}

func (d *runningDaemon) stop(t *testing.T) {
	t.Helper()
	d.cancel()
	select {
	case err := <-d.errCh:
		if err != nil {
			t.Fatalf("runWithContext 退出错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runWithContext 未在 5s 内退出")
	}
}

// TestResolveEndpoint：命令行旗标到 IPC 端点的翻译；-pipe 与 -tcp 互斥。
func TestResolveEndpoint(t *testing.T) {
	ep, err := resolveEndpoint("chaoset.sock", "", false)
	if err != nil || ep.Network != "unix" || ep.Address != "chaoset.sock" {
		t.Fatalf("默认端点应为 unix:chaoset.sock，实际 %+v err=%v", ep, err)
	}
	ep, err = resolveEndpoint("", "127.0.0.1:7949", false)
	if err != nil || ep.Network != "tcp" {
		t.Fatalf("-tcp 端点错误: %+v err=%v", ep, err)
	}
	if _, err := resolveEndpoint("", "127.0.0.1:7949", true); err == nil {
		t.Fatal("-pipe 与 -tcp 同时指定应报错")
	}
}

// TestRunWithContextServesAndShutsDown：完整生命周期——启动、Ping/Input、
// 取消 context 后优雅退出、套接字清理。此前 run() 直接绑信号，无法在测试里
// 驱动启停，daemon 主流程覆盖率只有 12.8%。
func TestRunWithContextServesAndShutsDown(t *testing.T) {
	dir := newDaemonTestDir(t)
	sock := filepath.Join(dir, "s")
	dict := writeDict(t, dir, "dict.tsv", daemonTestDict)
	d := startDaemon(t, sock, dict, filepath.Join(dir, "user.json"))

	if err := d.client.Ping(); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
	comp, err := d.client.Input("nihao")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "你好" {
		t.Fatalf("候选不符预期: %+v", comp.Candidates)
	}

	d.stop(t)
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("退出后套接字应被清理，实际 err=%v", err)
	}
}

// TestRunWithContextPersistsLearningOnShutdown：优雅退出必须把上屏学习数据
// 落盘（srv.Close → user.Save）。这是「用户关掉输入法/重启电脑后词频还在」
// 的直接保证。
func TestRunWithContextPersistsLearningOnShutdown(t *testing.T) {
	dir := newDaemonTestDir(t)
	sock := filepath.Join(dir, "s")
	dict := writeDict(t, dir, "dict.tsv", daemonTestDict)
	userPath := filepath.Join(dir, "user.json")
	d := startDaemon(t, sock, dict, userPath)

	if _, err := d.client.Input("nihao"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if _, err := d.client.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	d.stop(t)

	u, err := engine.NewUserDict(userPath)
	if err != nil {
		t.Fatalf("重载用户词典失败: %v", err)
	}
	if got := u.Score("你好", []string{"ni", "hao"}, ""); got == 0 {
		t.Fatalf("退出后学习数据应已落盘，实际 Score=0")
	}
}

// TestRunWithContextRejectsSecondInstance：同一套接字上第二个 daemon 必须在
// 拿到锁时明确失败，不能 unlink 掉正在运行的实例的套接字。
func TestRunWithContextRejectsSecondInstance(t *testing.T) {
	dir := newDaemonTestDir(t)
	sock := filepath.Join(dir, "s")
	dict := writeDict(t, dir, "dict.tsv", daemonTestDict)
	d := startDaemon(t, sock, dict, filepath.Join(dir, "user.json"))

	err := runWithContext(context.Background(), ipc.Endpoint{Network: "unix", Address: sock}, []string{dict},
		filepath.Join(dir, "user2.json"), false, false, "")
	if err == nil || !strings.Contains(err.Error(), "已有 chaosetd 在运行") {
		t.Fatalf("第二个实例应被单实例锁拒绝，实际 %v", err)
	}
	d.stop(t)
}

// TestRunWithContextRejectsBadConfig：启动期配置错误必须明确报错，不能
// 静默起一个打不出字的 daemon。
func TestRunWithContextRejectsBadConfig(t *testing.T) {
	dir := newDaemonTestDir(t)
	good := writeDict(t, dir, "good.tsv", daemonTestDict)
	empty := writeDict(t, dir, "empty.tsv", "")
	userPath := filepath.Join(dir, "user.json")

	t.Run("词库不存在", func(t *testing.T) {
		err := runWithContext(context.Background(), ipc.Endpoint{Network: "unix", Address: filepath.Join(dir, "s1")},
			[]string{filepath.Join(dir, "missing.tsv")}, userPath, false, false, "")
		if err == nil {
			t.Fatal("词库不存在时应报错")
		}
	})
	t.Run("词库为空", func(t *testing.T) {
		err := runWithContext(context.Background(), ipc.Endpoint{Network: "unix", Address: filepath.Join(dir, "s2")},
			[]string{empty}, userPath, false, false, "")
		if err == nil || !strings.Contains(err.Error(), "没有加载到任何词条") {
			t.Fatalf("空词库应报「没有加载到任何词条」，实际 %v", err)
		}
	})
	t.Run("TCP 端口非法", func(t *testing.T) {
		err := runWithContext(context.Background(), ipc.Endpoint{Network: "tcp", Address: "127.0.0.1:99999"},
			[]string{good}, userPath, false, false, "")
		if err == nil || !strings.Contains(err.Error(), "监听") {
			t.Fatalf("非法 TCP 端口应报监听错误，实际 %v", err)
		}
	})
}
