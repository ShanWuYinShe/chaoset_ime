package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// 输入法的铁律：任何情况下都不能丢字。
//
// 回归背景（2026-10-26）：Select() 曾把 BumpWord/BumpBigram 的落盘错误
// 直接返回给调用方。于是磁盘满 / 目录只读 / 权限变更等任何落盘故障都会让
// 这次上屏彻底失败——壳已经把按键消费掉了，用户看到的是「按数字键毫无反应，
// 字也没进文档」（实测复现）。
//
// 本测试模拟「用户词典完全不可写」，断言：
//  1. 上屏仍然成功返回文本；
//  2. 组词状态被正确清空；
//  3. 学习数据在内存里仍然生效（Score 能看到加成）。
func TestSelectCommitsEvenWhenPersistenceFails(t *testing.T) {
	d := mustEvoDict(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("NewUserDict: %v", err)
	}
	u.SetDeferred(false) // 立即落盘模式，确保失败路径被触发

	s := NewSession(d, u)
	mustInput(t, s, "danwei")

	// 让落盘必然失败：把目录改成只读（macOS/Linux 上普通用户无法在其中建临时文件）
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("无法设置目录权限（可能是 root 运行）: %v", err)
	}
	defer os.Chmod(dir, 0o700)

	text, err := s.Select(0)
	if err != nil {
		t.Fatalf("落盘失败时上屏也不能失败，得到错误: %v", err)
	}
	if text == "" {
		t.Fatal("上屏文本不应为空——丢字是不可接受的")
	}
	if s.Compose().Input != "" {
		t.Fatalf("上屏后组词状态应清空，实际 input=%q", s.Compose().Input)
	}
	// 学习数据在内存里应已生效（即使没落盘成功）
	if got := u.Score(text, []string{"dan", "wei"}, ""); got == 0 {
		t.Fatalf("学习数据应在内存中生效，Score 得到 0")
	}
}

// 延迟落盘模式：bump 后不立即写盘，但 FlushIfDirty 必须把它落下去。
func TestDeferredModeFlushesOnDemand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("NewUserDict: %v", err)
	}
	u.SetDeferred(true)

	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord: %v", err)
	}
	// 延迟模式下此刻不应落盘
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("延迟模式下 bump 不应立即写盘")
	}
	wrote, err := u.FlushIfDirty()
	if err != nil {
		t.Fatalf("FlushIfDirty: %v", err)
	}
	if !wrote {
		t.Fatal("有脏数据时 FlushIfDirty 应返回 true")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("flush 之后文件应存在: %v", statErr)
	}
	// 无变更时不应该重复写
	if wrote2, _ := u.FlushIfDirty(); wrote2 {
		t.Fatal("无脏数据时 FlushIfDirty 不应写盘")
	}
}

// 延迟模式下重新加载应看到已 flush 的数据。
func TestDeferredModeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, _ := NewUserDict(path)
	u.SetDeferred(true)
	if err := u.BumpPhrase("单位", []string{"dan", "wei"}); err != nil {
		t.Fatalf("BumpPhrase: %v", err)
	}
	if _, err := u.FlushIfDirty(); err != nil {
		t.Fatalf("FlushIfDirty: %v", err)
	}

	u2, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	if got := u2.PhraseCount("单位", []string{"dan", "wei"}); got != 1 {
		t.Fatalf("重新加载后词组计数应为 1，得到 %d", got)
	}
}
