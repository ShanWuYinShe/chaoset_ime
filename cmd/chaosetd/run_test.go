package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

// TestResolveUserDictPath：未传 -userdict 时落到平台用户目录；显式传入
// （含显式空串=不落盘）时保持原值。
func TestResolveUserDictPath(t *testing.T) {
	path, err := resolveUserDictPath("", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "userdict.json") {
		t.Fatalf("默认用户词典路径应以 userdict.json 结尾，实际 %q", path)
	}
	if got, err := resolveUserDictPath("", true); err != nil || got != "" {
		t.Fatalf("显式空串应保持不落盘，实际 %q err=%v", got, err)
	}
	if got, err := resolveUserDictPath("/tmp/x.json", true); err != nil || got != "/tmp/x.json" {
		t.Fatalf("显式路径应原样返回，实际 %q err=%v", got, err)
	}
}

// TestResolveEndpoint：命令行旗标到 IPC 端点的翻译。
func TestResolveEndpoint(t *testing.T) {
	ep, err := resolveEndpoint("chaoset.sock", false)
	if err != nil || ep.Network != "unix" || ep.Address != "chaoset.sock" {
		t.Fatalf("默认端点应为 unix:chaoset.sock，实际 %+v err=%v", ep, err)
	}
	// -pipe：端点是当前用户 SID 自动生成的命名管道名。Windows 上必然成功；
	// 其它平台 DefaultPipeName 报错，此时必须把错误原样上抛（不能静默退回
	// Unix socket，否则 Windows 用户会连到一个不存在的套接字）。
	ep, pipeErr := resolveEndpoint("", true)
	if pipeErr != nil {
		if runtime.GOOS == "windows" {
			t.Fatalf("-pipe 在 Windows 上应生成命名管道名，实际 %v", pipeErr)
		}
		if ep.Network == "unix" {
			t.Fatalf("-pipe 失败时不得退回 Unix 端点: %+v", ep)
		}
	} else if ep.Network != "pipe" || ep.Address == "" {
		t.Fatalf("-pipe 端点错误: %+v", ep)
	}
	// -pipe 与 -socket 互斥：同时给出必须报用法错误，不能静默按其中一个启动。
	if _, err := resolveEndpoint("/tmp/chaoset.sock", true); err == nil {
		t.Fatal("-pipe 与 -socket 同时指定应报错")
	}
}

// TestSocketPathFor：Unix socket 路径只在真正需要 Unix 传输时解析。
//
// 回归用例（v0.3.12 Windows 安装版打不出汉字）：Windows 上
// appdirs.SocketPath() 必定返回错误，而 daemon 在 -pipe 下也调用它并
// log.Fatal——进程在监听命名管道之前就退出。这里用「一定报错的
// defaultPath」模拟 Windows，断言 -pipe 不会触碰它。
func TestSocketPathFor(t *testing.T) {
	boom := func() (string, error) {
		return "", errors.New("Windows 使用命名管道，没有 Unix socket 路径")
	}

	// -pipe：完整端点，不得解析（旧实现在这里退出）。
	if got, err := socketPathFor("", true, boom); err != nil || got != "" {
		t.Fatalf("-pipe 不应解析 Unix socket 路径，实际 %q err=%v", got, err)
	}
	// 显式 -socket 优先，且不触碰默认值。
	if got, err := socketPathFor("/tmp/s", false, boom); err != nil || got != "/tmp/s" {
		t.Fatalf("显式 -socket 应原样返回，实际 %q err=%v", got, err)
	}
	// 默认解析路径的错误必须照常上抛（不吞错）。
	if _, err := socketPathFor("", false, boom); err == nil {
		t.Fatal("默认路径解析失败时必须返回错误")
	}
	// 没有默认解析器时不解析。
	if got, err := socketPathFor("", false, nil); err != nil || got != "" {
		t.Fatalf("无默认解析器时应返回空路径，实际 %q err=%v", got, err)
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
	comp, err := d.client.Input("nihao", false)
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

// TestRunWithContextCreatesSocketDir：套接字目录不存在时必须先建目录再拿锁。
//
// 回归用例（2026-11-04 实测）：单实例锁文件是 <socket>.lock，acquireSingleton
// 只 OpenFile 不建目录，而默认 socket 位于 $XDG_RUNTIME_DIR/chaoset（全新登录
// 会话里不存在）——旧顺序「先拿锁、后建目录」让 daemon 在启动期直接退出
// （打开锁文件 … no such file or directory）。这里用两级不存在的子目录模拟，
// 断言 daemon 能正常监听并在退出后清理。
func TestRunWithContextCreatesSocketDir(t *testing.T) {
	dir := newDaemonTestDir(t)
	// 两级都不存在：daemon 必须自己 MkdirAll。
	sock := filepath.Join(dir, "runtime", "chaoset", "s")
	dict := writeDict(t, dir, "dict.tsv", daemonTestDict)
	d := startDaemon(t, sock, dict, filepath.Join(dir, "user.json"))

	if err := d.client.Ping(); err != nil {
		t.Fatalf("Ping 失败: %v", err)
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

	if _, err := d.client.Input("nihao", false); err != nil {
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
	t.Run("套接字路径超长", func(t *testing.T) {
		// 超过内核 sockaddr_un.sun_path 上限（macOS 104 字节 / Linux 108 字节）
		// 的路径必然 bind 失败，用来覆盖「监听失败必须暴露」的分支——不能静默
		// 起一个谁也连不上的 daemon。目录先建好（单级名 120 字节，低于文件名
		// 255 字节上限），这样单实例锁能拿到，失败点确实落在 ipc.Listen 上。
		longDir := filepath.Join(dir, strings.Repeat("x", 120))
		if err := os.MkdirAll(longDir, 0o700); err != nil {
			t.Fatalf("创建长路径目录失败: %v", err)
		}
		err := runWithContext(context.Background(), ipc.Endpoint{Network: "unix", Address: filepath.Join(longDir, "s")},
			[]string{good}, userPath, false, false, "")
		if err == nil {
			t.Fatal("套接字路径超长时应报错")
		}
		if !strings.Contains(err.Error(), "监听") {
			t.Fatalf("bind 失败应报监听错误，实际 %v", err)
		}
	})
}
