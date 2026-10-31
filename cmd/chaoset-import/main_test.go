package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaoset.ime/engine"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}

// readEntries 解析输出 TSV，按「词+音节」索引，方便断言词频。
func readEntries(t *testing.T, path string) map[string]int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读输出失败: %v", err)
	}
	d, _, err := engine.ParseDictAuto(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("解析输出失败: %v", err)
	}
	out := make(map[string]int)
	for _, e := range d.Entries() {
		out[e.Word+"\x00"+strings.Join(e.Syllables, " ")] = e.Freq
	}
	return out
}

// TestRunMergeKeepsLargerFreq：默认合并重复词条取较大词频。
func TestRunMergeKeepsLargerFreq(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "merged.tsv")
	base := filepath.Join(dir, "base.tsv")
	hot := filepath.Join(dir, "hot.tsv")
	writeFile(t, base, "你好\tni hao\t10\n")
	writeFile(t, hot, "你好\tni hao\t20\n你\tni\t30\n")
	if err := run(out, false, []string{hot}); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	got := readEntries(t, out)
	if got["你好\x00ni hao"] != 20 {
		t.Fatalf("重复词应取较大词频 20，实际 %d", got["你好\x00ni hao"])
	}
	if got["你\x00ni"] != 30 {
		t.Fatalf("新词词频应为 30，实际 %d", got["你\x00ni"])
	}
}

// TestRunReplaceOverridesLowerFreq：-replace 时导入词频覆盖已有值。
func TestRunReplaceOverridesLowerFreq(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "merged.tsv")
	base := filepath.Join(dir, "base.tsv")
	hot := filepath.Join(dir, "hot.tsv")
	writeFile(t, base, "你好\tni hao\t20\n")
	writeFile(t, hot, "你好\tni hao\t5\n")
	if err := run(out, true, []string{hot}); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	if got := readEntries(t, out)["你好\x00ni hao"]; got != 5 {
		t.Fatalf("-replace 应覆盖为 5，实际 %d", got)
	}
}

// TestRunMissingInputKeepsOldOutput：读取输入失败时旧输出文件必须原样保留
// （原子写只在全部输入成功解析后才 rename）。
func TestRunMissingInputKeepsOldOutput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "merged.tsv")
	writeFile(t, out, "旧词\tjiu ci\t42\n")
	old, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(out, false, []string{filepath.Join(dir, "missing.yaml")}); err == nil {
		t.Fatal("输入文件不存在时 run 应报错")
	}
	now, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(now) != string(old) {
		t.Fatalf("失败时旧词库不应被改动：old=%q now=%q", old, now)
	}
}

// TestRunCreatesOutputDir：输出目录不存在时应自动创建。
func TestRunCreatesOutputDir(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "nested", "merged.tsv")
	hot := filepath.Join(dir, "hot.tsv")
	writeFile(t, hot, "你好\tni hao\t10\n")
	if err := run(out, false, []string{hot}); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("输出文件应存在: %v", err)
	}
}
