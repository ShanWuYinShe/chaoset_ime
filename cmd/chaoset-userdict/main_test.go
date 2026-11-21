package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// seededUserDict 按 daemon/工具的存储格式写一份用户词典：
// words 键 = 「词\x00音节 空格分隔」，phrases 键相同（值多 word/sylls）。
type seededUserDict struct {
	Words   map[string]map[string]any `json:"words"`
	Phrases map[string]map[string]any `json:"phrases"`
	Bigrams map[string]map[string]any `json:"bigrams"`
}

func seedUserDict(t *testing.T, path string, words, phrases []string) {
	t.Helper()
	seed := seededUserDict{
		Words:   make(map[string]map[string]any),
		Phrases: make(map[string]map[string]any),
		Bigrams: make(map[string]map[string]any),
	}
	for _, w := range words {
		seed.Words[w] = map[string]any{"count": 3, "last_used": 1700000000}
	}
	for _, p := range phrases {
		key, sylls, _ := strings.Cut(p, "\x00")
		seed.Phrases[p] = map[string]any{
			"word": key, "sylls": sylls, "count": 2, "last_used": 1700000000,
		}
	}
	data, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		t.Fatalf("序列化种子用户词典失败: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写种子用户词典失败: %v", err)
	}
}

func loadUserDictJSON(t *testing.T, path string) seededUserDict {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读用户词典失败: %v", err)
	}
	var got seededUserDict
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("解析用户词典失败: %v", err)
	}
	return got
}

// TestRunLocalStatsClearExport：本地模式的 stats/clear/export 全链路。
func TestRunLocalStatsClearExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	seedUserDict(t, path, []string{"你好\x00ni hao"}, nil)

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
	after := loadUserDictJSON(t, path)
	if len(after.Words) != 0 || len(after.Phrases) != 0 || len(after.Bigrams) != 0 {
		t.Fatalf("clear 后应为空，实际 words=%d phrases=%d bigrams=%d",
			len(after.Words), len(after.Phrases), len(after.Bigrams))
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

// TestRunLocalDelete：本地模式 delete 词+音节 → 记录消失；重复删除不报错；
// 省略音节时按词文本删全部读音记录；其它词不受影响。
func TestRunLocalDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	seedUserDict(t, path,
		[]string{"今天\x00jin tian", "你好\x00ni hao"},
		[]string{"今天天气\x00jin tian tian qi"})

	if err := runLocal(path, "delete", cmdArgs{word: "今天", sylls: splitSyllables("jin tian")}); err != nil {
		t.Fatalf("delete 失败: %v", err)
	}
	if err := runLocal(path, "delete", cmdArgs{word: "今天"}); err != nil {
		t.Fatalf("重复 delete 不应报错: %v", err)
	}
	if err := runLocal(path, "delete", cmdArgs{word: "今天天气"}); err != nil {
		t.Fatalf("delete(省略音节) 失败: %v", err)
	}

	after := loadUserDictJSON(t, path)
	if _, ok := after.Words["今天\x00jin tian"]; ok {
		t.Fatal("删除后「今天」的调频记录仍在")
	}
	if _, ok := after.Phrases["今天天气\x00jin tian tian qi"]; ok {
		t.Fatal("按词文本删除后「今天天气」词组仍在")
	}
	if _, ok := after.Words["你好\x00ni hao"]; !ok {
		t.Fatal("删除「今天」不应影响「你好」的学习记录")
	}
}
