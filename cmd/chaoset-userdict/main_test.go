package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaoset.ime/engine"
)

// TestPathsJSON：-print-paths 输出当前平台的 userdict/imported_dict 路径。
func TestPathsJSON(t *testing.T) {
	text, err := pathsJSON()
	if err != nil {
		t.Fatal(err)
	}
	var paths map[string]string
	if err := json.Unmarshal([]byte(text), &paths); err != nil {
		t.Fatalf("pathsJSON 不是合法 JSON: %v (%s)", err, text)
	}
	if !strings.HasSuffix(paths["userdict"], "userdict.json") {
		t.Fatalf("userdict 路径错误: %q", paths["userdict"])
	}
	if !strings.HasSuffix(paths["imported_dict"], "dict.tsv") {
		t.Fatalf("imported_dict 路径错误: %q", paths["imported_dict"])
	}
	if !strings.HasSuffix(paths["config"], "config.json") {
		t.Fatalf("config 路径错误: %q", paths["config"])
	}
}

// TestRunLocalStatsClearExport：本地模式的 stats/clear/export 全链路。
func TestRunLocalStatsClearExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	u, err := engine.NewUserDict(path)
	if err != nil {
		t.Fatalf("加载用户词典失败: %v", err)
	}
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	if err := u.Save(); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	if err := runLocal(path, "stats", cmdArgs{}); err != nil {
		t.Fatalf("stats 失败: %v", err)
	}
	out := filepath.Join(dir, "export.tsv")
	if err := runLocal(path, "export", cmdArgs{out: out}); err != nil {
		t.Fatalf("export 失败: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读导出文件失败: %v", err)
	}
	if !strings.Contains(string(data), "你好") {
		t.Fatalf("导出内容应含「你好」，实际 %q", data)
	}

	if err := runLocal(path, "clear", cmdArgs{}); err != nil {
		t.Fatalf("clear 失败: %v", err)
	}
	u2, err := engine.NewUserDict(path)
	if err != nil {
		t.Fatalf("重载用户词典失败: %v", err)
	}
	if st := u2.Stats(); st.Words != 0 || st.Phrases != 0 || st.Bigrams != 0 {
		t.Fatalf("clear 后统计应全 0，实际 %+v", st)
	}
}

// TestSplitSyllables：delete 子命令的音节参数接受 space / ' 两种分隔。
func TestSplitSyllables(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"jin tian":     "jin tian",
		"jin'tian":     "jin tian",
		" jin  'tian ": "jin tian",
	}
	for in, want := range cases {
		got := strings.Join(splitSyllables(in), " ")
		if got != want {
			t.Errorf("splitSyllables(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestRunLocalDelete：本地模式 delete 词 + 音节 → 记录消失、重载后仍是删除态；
// 重复删除不报错（幂等，输出「未找到」）；省略音节时按词文本删全部读音记录。
func TestRunLocalDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	u, err := engine.NewUserDict(path)
	if err != nil {
		t.Fatalf("加载用户词典失败: %v", err)
	}
	if err := u.BumpWord("今天", []string{"jin", "tian"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	if err := u.BumpPhrase("今天天气", []string{"jin", "tian", "tian", "qi"}); err != nil {
		t.Fatalf("BumpPhrase 失败: %v", err)
	}
	// 另一个词的学习记录不得被误删。
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	if err := u.Save(); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	if err := runLocal(path, "delete", cmdArgs{word: "今天", sylls: splitSyllables("jin tian")}); err != nil {
		t.Fatalf("delete 失败: %v", err)
	}
	if err := runLocal(path, "delete", cmdArgs{word: "今天"}); err != nil {
		t.Fatalf("重复 delete 不应报错: %v", err)
	}
	// 省略音节：按词文本删（今天天气 不在本次删除范围内，仍存在）
	if err := runLocal(path, "delete", cmdArgs{word: "今天天气"}); err != nil {
		t.Fatalf("delete(省略音节) 失败: %v", err)
	}

	reloaded, err := engine.NewUserDict(path)
	if err != nil {
		t.Fatalf("重载用户词典失败: %v", err)
	}
	if got := reloaded.Score("今天", []string{"jin", "tian"}, ""); got != 0 {
		t.Fatalf("重载后「今天」仍有调频分 %d（删除未落盘？）", got)
	}
	if got := reloaded.PhraseCount("今天天气", []string{"jin", "tian", "tian", "qi"}); got != 0 {
		t.Fatalf("重载后「今天天气」词组计数 %d，期望 0", got)
	}
	if got := reloaded.Score("你好", []string{"ni", "hao"}, ""); got == 0 {
		t.Fatal("删除「今天」不应影响「你好」的学习记录")
	}
}

// TestWriteOutAtomicReplaces：覆盖已有导出文件时整体替换，不留临时文件。
func TestWriteOutAtomicReplaces(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "export.tsv")
	if err := os.WriteFile(out, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOut(out, "新内容"); err != nil {
		t.Fatalf("writeOut 失败: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "新内容" {
		t.Fatalf("导出文件内容 = %q，期望 新内容", data)
	}
	assertNoTempFiles(t, dir)
}

// TestWriteOutRenameFailureCleansTemp：目标不可替换时报错，旧目标不变且
// 临时文件必须清理（不留下 .chaoset-userdict-*.tmp 垃圾）。
func TestWriteOutRenameFailureCleansTemp(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "outdir")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeOut(out, "内容"); err == nil {
		t.Fatal("目标是目录时 writeOut 应报错")
	}
	assertNoTempFiles(t, dir)
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".chaoset-userdict-") {
			t.Fatalf("不应残留临时文件: %s", entry.Name())
		}
	}
}
