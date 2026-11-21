package dict

import (
	"bytes"
	"strings"
	"testing"
)

// covers Rime dict.yaml 常见写法：YAML 头、音调数字、声调符号、ü→v。
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

func findEntry(t *testing.T, entries []*Entry, word string) *Entry {
	t.Helper()
	for _, e := range entries {
		if e.Word == word {
			return e
		}
	}
	return nil
}

func joinSylls(e *Entry) string {
	if e == nil {
		return ""
	}
	return strings.Join(e.Syllables, " ")
}

func TestParseAutoRimeYAML(t *testing.T) {
	entries, skipped, err := ParseAuto(strings.NewReader(rimeYAML))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if skipped != 2 {
		t.Fatalf("应跳过 2 行无效词条，得到 %d", skipped)
	}
	if e := findEntry(t, entries, "你好"); joinSylls(e) != "ni hao" || e.Freq != 99 {
		t.Fatalf("ni3 hao3 应归一化且词频压缩到 99: %+v", e)
	}
	if e := findEntry(t, entries, "做"); joinSylls(e) != "zuo" {
		t.Fatalf("带声调符号的 zuò 应归一化: %+v", e)
	}
	if e := findEntry(t, entries, "绿"); joinSylls(e) != "lv" {
		t.Fatalf("lǜ4 应归一化为 lv: %+v", e)
	}
	if e := findEntry(t, entries, "略"); joinSylls(e) != "lve" || e.Freq != 90 {
		t.Fatalf("lüe4/lue4 应归一化为 lve 且去重取大: %+v", e)
	}
	if len(entries) != 4 {
		t.Fatalf("词条数应为 4，得到 %d", len(entries))
	}
}

func TestParseAutoTSV(t *testing.T) {
	entries, skipped, err := ParseAuto(strings.NewReader("两列词\tliang lie\n"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if skipped != 0 || len(entries) != 1 || entries[0].Freq != 20 {
		t.Fatalf("两列 TSV 解析异常: skipped=%d entries=%+v", skipped, entries)
	}
}

func TestWriteTSVRoundtrip(t *testing.T) {
	entries, _, err := ParseAuto(strings.NewReader(rimeYAML))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteTSV(&buf, entries); err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "你好\tni hao\t99") {
		t.Fatalf("导出内容不符预期:\n%s", out)
	}
	back, skipped, err := ParseAuto(strings.NewReader(out))
	if err != nil || skipped != 0 {
		t.Fatalf("导出的 TSV 应能被重新解析: err=%v skipped=%d", err, skipped)
	}
	if len(back) != len(entries) {
		t.Fatalf("往返词条数不一致: %d vs %d", len(back), len(entries))
	}
}

// 别人编辑的词典常把拼音连写：整串能切成合法音节时应自动切分。
func TestParseAutoRunOnPinyin(t *testing.T) {
	const src = "混沌输入法\thundun shurufa\t88\n好朋友\thaopengyou\t77\n坏词\tzzz\t10\n"
	entries, skipped, err := ParseAuto(strings.NewReader(src))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if skipped != 1 {
		t.Fatalf("应跳过 1 行，得到 %d", skipped)
	}
	if e := findEntry(t, entries, "混沌输入法"); joinSylls(e) != "hun dun shu ru fa" {
		t.Fatalf("连写拼音应自动切分: %+v", e)
	}
}

const cedictSample = `# CC-CEDICT 示例（许可证头注释会被跳过）
# License: CC-BY-SA 4.0
中國	中国	[Zhong1 guo2] /China/Middle Kingdom/
時間	时间	[shi2 jian1] /time/
女	女	[nv3] /female/
略	略	[lve4] /strategy/
 Liverpool	Liverpool	[Li1 wu2 pu3] /Liverpool/
OK	OK	[O K] /OK/
`

func TestParseCCCEDICT(t *testing.T) {
	entries, skipped, err := ParseAuto(strings.NewReader(cedictSample))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(entries) != 4 || skipped != 2 {
		t.Fatalf("entries=%d skipped=%d，期望 4/2", len(entries), skipped)
	}
	if e := findEntry(t, entries, "中国"); joinSylls(e) != "zhong guo" || e.Freq != cedictDefaultFreq {
		t.Fatalf("中国 词条异常: %+v", e)
	}
	if e := findEntry(t, entries, "时间"); joinSylls(e) != "shi jian" {
		t.Fatalf("时间 词条异常: %+v", e)
	}
	if e := findEntry(t, entries, "女"); joinSylls(e) != "nv" {
		t.Fatalf("女 u: 记法应归一为 nv: %+v", e)
	}
	if e := findEntry(t, entries, "略"); joinSylls(e) != "lve" {
		t.Fatalf("略 应归一为 lve: %+v", e)
	}
}

// TSV 内容不应被误判为 CC-CEDICT（回归保护）。
func TestParseAutoTSVUnaffected(t *testing.T) {
	entries, skipped, err := ParseAuto(strings.NewReader("你好\tni hao\t99\n"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(entries) != 1 || skipped != 0 {
		t.Fatalf("TSV 解析异常: entries=%d skipped=%d", len(entries), skipped)
	}
}

// 简繁转换表可加载（dictgen 用它做简体存在性判断）。
func TestToSimplified(t *testing.T) {
	cases := map[string]string{
		"頭髮":  "头发",
		"中國":  "中国",
		"abc": "abc",
	}
	for in, want := range cases {
		if got := ToSimplified(in); got != want {
			t.Errorf("ToSimplified(%q) = %q，期望 %q", in, got, want)
		}
	}
}
