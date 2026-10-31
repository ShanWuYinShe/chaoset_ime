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
	if err := run(out, "", false, false, []string{hot}); err != nil {
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
	if err := run(out, "", false, true, []string{hot}); err != nil {
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
	if err := run(out, "", false, false, []string{filepath.Join(dir, "missing.yaml")}); err == nil {
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

// TestRunSeedAndBackup：-seed 先并入底表，-backup 在写目标前备份旧词库。
func TestRunSeedAndBackup(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "merged.tsv")
	seed := filepath.Join(dir, "seed.tsv")
	hot := filepath.Join(dir, "hot.tsv")
	writeFile(t, seed, "基础\tji chu\t50\n")
	writeFile(t, out, "旧词\tjiu ci\t10\n")
	writeFile(t, hot, "新词\txin ci\t20\n")
	if err := run(out, seed, true, false, []string{hot}); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	got := readEntries(t, out)
	if got["基础\x00ji chu"] != 50 || got["旧词\x00jiu ci"] != 10 || got["新词\x00xin ci"] != 20 {
		t.Fatalf("seed/旧词/新词都应保留，实际 %v", got)
	}
	baks, err := filepath.Glob(out + ".bak.*")
	if err != nil {
		t.Fatal(err)
	}
	if len(baks) != 1 {
		t.Fatalf("应生成 1 个旧词库备份，实际 %v", baks)
	}
	old, err := os.ReadFile(baks[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(old), "旧词") {
		t.Fatalf("备份应包含旧词库内容，实际 %q", old)
	}
}

// TestRunSeedMissingFails：种子词库不存在时明确报错，不能静默少一层词库。
func TestRunSeedMissingFails(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "merged.tsv")
	hot := filepath.Join(dir, "hot.tsv")
	writeFile(t, hot, "新词\txin ci\t20\n")
	if err := run(out, filepath.Join(dir, "missing-seed.tsv"), false, false, []string{hot}); err == nil {
		t.Fatal("种子词库不存在时 run 应报错")
	}
}

// TestRunBackupOnlyWhenTargetExists：首次导入没有旧词库，不应产生空备份。
func TestRunBackupOnlyWhenTargetExists(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "merged.tsv")
	hot := filepath.Join(dir, "hot.tsv")
	writeFile(t, hot, "新词\txin ci\t20\n")
	if err := run(out, "", true, false, []string{hot}); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	baks, err := filepath.Glob(out + ".bak.*")
	if err != nil {
		t.Fatal(err)
	}
	if len(baks) != 0 {
		t.Fatalf("首次导入不应生成备份，实际 %v", baks)
	}
}

// TestRunCreatesOutputDir：输出目录不存在时应自动创建。
func TestRunCreatesOutputDir(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "nested", "merged.tsv")
	hot := filepath.Join(dir, "hot.tsv")
	writeFile(t, hot, "你好\tni hao\t10\n")
	if err := run(out, "", false, false, []string{hot}); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("输出文件应存在: %v", err)
	}
}
