package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 覆盖 Rime dict.yaml 常见写法：YAML 头、音调数字、声调符号、ü→v。
const rimeYAML = `# Rime dictionary
---
name: test
version: "1"
sort: original
...
你好	ni3 hao3	50000
做	zuò	300
绿	lǜ4	100
略	lüe4	90
略	lue4	88
坏词	zzz	10
只有词
`

func TestParseDictAutoRimeYAML(t *testing.T) {
	d, skipped, err := ParseDictAuto(strings.NewReader(rimeYAML))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if skipped != 2 {
		t.Fatalf("应跳过 2 行无效词条，得到 %d", skipped)
	}
	if got := d.Query([]string{"ni", "hao"}); len(got) != 1 || got[0].Word != "你好" || got[0].Freq != 99 {
		t.Fatalf("ni3 hao3 应归一化且词频压缩到 99: %+v", got)
	}
	if got := d.Query([]string{"zuo"}); len(got) != 1 || got[0].Word != "做" {
		t.Fatalf("带声调符号的 zuò 应归一化: %+v", got)
	}
	if got := d.Query([]string{"lv"}); len(got) != 1 || got[0].Word != "绿" {
		t.Fatalf("lǜ4 应归一化为 lv: %+v", got)
	}
	if got := d.Query([]string{"lve"}); len(got) != 1 || got[0].Word != "略" || got[0].Freq != 90 {
		t.Fatalf("lüe4/lue4 应归一化为 lve 且去重取大: %+v", got)
	}
	if d.Len() != 4 {
		t.Fatalf("词条数应为 4，得到 %d", d.Len())
	}
}

func TestParseDictAutoTSV(t *testing.T) {
	d, skipped, err := ParseDictAuto(strings.NewReader("两列词	liang lie\n"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("两列 TSV 应可解析，skipped=%d", skipped)
	}
	if got := d.Query([]string{"liang", "lie"}); len(got) != 1 || got[0].Freq != 20 {
		t.Fatalf("缺省词频应为 20: %+v", got)
	}
}

func TestWriteTSVRoundtrip(t *testing.T) {
	d, _, err := ParseDictAuto(strings.NewReader(rimeYAML))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteTSV(&buf, d.Entries()); err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "你好\tni hao\t99") {
		t.Fatalf("导出内容不符预期:\n%s", out)
	}
	d2, err := ParseDict(strings.NewReader(out))
	if err != nil {
		t.Fatalf("导出的 TSV 应能被严格解析: %v", err)
	}
	if d2.Len() != d.Len() {
		t.Fatalf("往返词条数不一致: %d vs %d", d2.Len(), d.Len())
	}
}

// 别人编辑的词典常把拼音连写（hundun、shurufa）：整串能切成合法音节时应自动切分。
func TestParseDictAutoRunOnPinyin(t *testing.T) {
	const src = "混沌输入法\thundun shurufa\t88\n好朋友\thaopengyou\t77\n坏词\tzzz\t10\n"
	d, skipped, err := ParseDictAuto(strings.NewReader(src))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if skipped != 1 {
		t.Fatalf("只应跳过 zzz 一行，得到 %d", skipped)
	}
	if got := d.Query([]string{"hun", "dun", "shu", "ru", "fa"}); len(got) != 1 || got[0].Word != "混沌输入法" {
		t.Fatalf("hundun shurufa 应切成 hun dun shu ru fa: %+v", got)
	}
	if got := d.Query([]string{"hao", "peng", "you"}); len(got) != 1 || got[0].Word != "好朋友" {
		t.Fatalf("haopengyou 应切成 hao peng you: %+v", got)
	}
}

func TestLoadDictFiles(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.tsv")
	p2 := filepath.Join(dir, "b.tsv")
	os.WriteFile(p1, []byte("你好\tni hao\t50\n"), 0o644)
	os.WriteFile(p2, []byte("你好\tni hao\t80\n再见\tzai jian\t60\n"), 0o644)
	d, err := LoadDictFiles([]string{p1, p2})
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if d.Len() != 2 {
		t.Fatalf("应合并为 2 条，得到 %d", d.Len())
	}
	if got := d.Query([]string{"ni", "hao"}); len(got) != 1 || got[0].Freq != 80 {
		t.Fatalf("重复词条应取较大词频: %+v", got)
	}
	if _, err := LoadDictFiles([]string{filepath.Join(dir, "missing.tsv")}); err == nil {
		t.Fatal("缺失文件应报错")
	}
}

// TestWriteTSVDeterministicForMultiReading：同词多拼音时输出顺序必须确定。
//
// 2026-10-29：多音字全读音引入后，同一词条有了多条（词相同、拼音不同、
// 词频相同）记录。原比较器「词频降序 → 同频按词排序」此时**不是全序**——
// 「着 zhao」与「着 zhe」compare 恒为 false，sort 结果依赖输入顺序，
// 导致 CLI 脚本与 GUI --selftest 两条导入路径产出不同的 dict.tsv
// （等价性测试 B 段失败）。补音节为第三级排序键后必须确定。
func TestWriteTSVDeterministicForMultiReading(t *testing.T) {
	// 同一批词条，两种输入顺序（模拟两条导入路径拿到记录的先后不同）
	mk := func(reversed bool) string {
		entries := []*Entry{
			{Word: "着", Syllables: []string{"zhao"}, Freq: 78},
			{Word: "着", Syllables: []string{"zhe"}, Freq: 78},
			{Word: "着", Syllables: []string{"zhuo"}, Freq: 78},
		}
		if reversed {
			entries[0], entries[2] = entries[2], entries[0]
		}
		var buf bytes.Buffer
		if err := WriteTSV(&buf, entries); err != nil {
			t.Fatalf("导出失败: %v", err)
		}
		return buf.String()
	}
	a, b := mk(false), mk(true)
	if a != b {
		t.Fatalf("同一批词条的不同输入顺序应产出相同 TSV（否则两条导入路径不一致）：\n顺序A:\n%s\n顺序B:\n%s", a, b)
	}
	// 且必须是按音节升序（zhao < zhe < zhuo）
	idxZhao := strings.Index(a, "着	zhao")
	idxZhe := strings.Index(a, "着	zhe")
	idxZhuo := strings.Index(a, "着	zhuo")
	if !(idxZhao < idxZhe && idxZhe < idxZhuo) {
		t.Fatalf("同词同频应按音节升序排列，实际位置 zhao=%d zhe=%d zhuo=%d", idxZhao, idxZhe, idxZhuo)
	}
}
