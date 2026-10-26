//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAcquireSingletonRejectsSecond 证明同一个套接字路径只能有一个实例拿到锁。
//
// 回归背景（2026-10-26）：daemon 曾在 net.Listen 前 os.Remove(socket)，
// 第二个实例会把正在运行的 daemon 的套接字 unlink 掉再自行监听，
// **两个进程都不报错**，表现为活动监视器里多个 chaosetd、学习数据互相覆盖。
func TestAcquireSingletonRejectsSecond(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "chaoset.sock")

	first, err := acquireSingleton(sock)
	if err != nil {
		t.Fatalf("第一个实例应能拿到锁，却失败: %v", err)
	}
	defer first.release()

	second, err := acquireSingleton(sock)
	if err == nil {
		second.release()
		t.Fatal("第二个实例也必须被拒绝，实际却拿到了锁")
	}
	if _, ok := err.(*os.PathError); ok {
		t.Fatalf("错误应说明「已有实例在运行」，实际是路径错误: %v", err)
	}
	t.Logf("第二个实例被正确拒绝: %v", err)
}

// TestAcquireSingletonReleased 证明释放后能重新获取——锁随进程退出/释放
// 自动生效，残留的 .lock 文件不会永久阻塞下次启动。
func TestAcquireSingletonReleased(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "chaoset.sock")

	first, err := acquireSingleton(sock)
	if err != nil {
		t.Fatalf("首次获取锁失败: %v", err)
	}
	first.release()

	again, err := acquireSingleton(sock)
	if err != nil {
		t.Fatalf("释放后应能重新获取锁，却失败: %v", err)
	}
	again.release()
}

// TestAcquireSingletonLeafFile 确认锁文件落在套接字旁边，路径可预测，
// 便于安装脚本与排障时定位。
func TestAcquireSingletonLeafFile(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "chaoset.sock")

	h, err := acquireSingleton(sock)
	if err != nil {
		t.Fatalf("获取锁失败: %v", err)
	}
	defer h.release()

	if _, err := os.Stat(sock + ".lock"); err != nil {
		t.Fatalf("应在 %s.lock 创建锁文件: %v", sock, err)
	}
}
