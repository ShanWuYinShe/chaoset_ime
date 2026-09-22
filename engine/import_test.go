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
