package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 输入法可用性铁律：学习数据是增量优化，不能因为它让整个输入法起不来。
//
// 回归背景（2026-10-30 审计）：userdict.json 一旦损坏（断电、磁盘满、手工
// 编辑、旧版本残留），NewUserDict 返回解析错误 → daemon log.Fatal 退出 →
// 用户看到的是「输入法彻底打不出字」，且没有任何自愈路径。而在落盘路径上
// 项目早已确立相反的原则（见 TestSelectCommitsEvenWhenPersistenceFails）。
// NewUserDictLenient 把加载路径对齐到同一原则。
func TestNewUserDictLenientRecoversCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	corrupt := []byte("{ 这不是 JSON 正文，模拟断电写坏的文件 ")
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	u, warn, err := NewUserDictLenient(path)
	if err != nil {
		t.Fatalf("损坏文件不应让加载失败（否则 daemon 起不来）: %v", err)
	}
	if warn == "" {
		t.Fatal("应返回警告文本供调用方记日志")
	}
	if !strings.Contains(warn, "解析失败") {
		t.Fatalf("警告应说明解析失败: %q", warn)
	}
	// 以空词典继续：不残留任何半解析状态
	if got := u.Stats(); got.Words != 0 || got.Phrases != 0 || got.Bigrams != 0 {
		t.Fatalf("损坏恢复后应为空词典，实际 %+v", got)
	}

	// 损坏文件必须留备份（不静默销毁现场），且内容原样保留
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("应恰好生成一个 .corrupt-* 备份，实际 %v", matches)
	}
	backup, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("读取备份失败: %v", err)
	}
	if string(backup) != string(corrupt) {
		t.Fatal("备份内容应与原损坏文件一致")
	}
	// 原路径已被移走：后续 Save 不会覆盖损坏数据
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("原路径应已被移走，实际 err=%v", err)
	}

	// 恢复出的词典必须可正常使用：写入学习数据并落盘
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("恢复后应能记录学习数据: %v", err)
	}
	if err := u.Save(); err != nil {
		t.Fatalf("恢复后应能落盘: %v", err)
	}
	if u.Stats().Words == 0 {
		t.Fatal("恢复后学习数据应生效")
	}
}

// 严格语义保持不变：工具/测试需要能发现损坏（不能让所有调用方都静默降级）。
func TestNewUserDictStrictStillRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewUserDict(path); err == nil {
		t.Fatal("严格构造函数必须报告解析错误")
	}
}

// 仅解析失败可恢复；读取失败（权限、路径是目录）仍须报错——
// 改名解决不了这类问题，静默吞掉会让用户误以为数据还在。
func TestNewUserDictLenientKeepsIOErrors(t *testing.T) {
	dir := t.TempDir()
	// 把「用户词典路径」指向一个目录：读取会失败但不是 JSON 解析问题
	if err := os.Mkdir(filepath.Join(dir, "user.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, warn, err := NewUserDictLenient(filepath.Join(dir, "user.json"))
	if err == nil {
		t.Fatalf("读取失败必须返回错误，实际 warning=%q", warn)
	}
}

// 正常文件不受影响：lenient 只对损坏文件降级。
func TestNewUserDictLenientLoadsValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	src, err := NewUserDict(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.BumpWord("世界", []string{"shi", "jie"}); err != nil {
		t.Fatal(err)
	}
	if err := src.Save(); err != nil {
		t.Fatal(err)
	}

	u, warn, err := NewUserDictLenient(path)
	if err != nil {
		t.Fatalf("正常文件加载失败: %v", err)
	}
	if warn != "" {
		t.Fatalf("正常文件不应产生警告: %q", warn)
	}
	if u.Stats().Words == 0 {
		t.Fatal("正常文件的学习数据应被加载")
	}
}
