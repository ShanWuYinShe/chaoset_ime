package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPhrase = `经常: jīng cháng
经济: jīng jì
如果: rú guǒ
什么: shén me
时候: shí hòu
好人: hǎo rén
图像: tú xiàng
后天: hòu tiān
`

const testSingle = `U+7684: de,dí,dì  # 的
U+662F: shì  # 是
U+7ECF: jīng  # 经
U+597D: hǎo,hào  # 好
U+50CF: xiàng  # 像
U+4EC0: shén,shí  # 什
U+4E48: me  # 么
U+65F6: shí  # 时
U+5019: hòu  # 候
U+4E00: yī  # 一
U+626B: sǎo  # 扫
`

const testGSC = "的\n是\n经\n好\n像\n什\n么\n时\n候\n一\n扫\n"

const testNorm = "经常\tjing chang\t1\n如果\tru guo\t2\n什么时候\tshen me shi hou\t3\n好像\thao xiang\t4\n後天\thou tian\t5\n"

const testHSK = "1 经济\n2 如果\n一级词汇表\n"

const testWordfreq = "经常\t5.0\n经济\t4.5\n如果\t6.0\n好像\t3.5\n扫一扫\t2.0\n"

const testHanLP = "经常\tn\t100\n经济\tn\t80\n如果\tn\t60\n"

const testExtra = "真棒\tzhen bang\t400\n"

const testHot = "扫一扫\tsao yi sao\t1000\n"

const testCharFreqHeader = "frequency_rank,character,pinyin,definition,radical,radical_code,stroke_count,hsk_level,general_standard_num\n"

func charFreqFixture() string {
	var b strings.Builder
	b.WriteString(testCharFreqHeader)
	chars := []rune("的是经好像什么时候一")
	for i, ch := range chars {
		fmt.Fprintf(&b, "%d,%c,x,x,一,1.0,1,1,0000\n", i+1, ch)
	}
	return b.String()
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestOptions(t *testing.T) options {
	t.Helper()
	dir := t.TempDir()
	return options{
		freqMode:     "wf",
		normPath:     writeTemp(t, "norm.tsv", testNorm),
		hskPath:      writeTemp(t, "hsk.tsv", testHSK),
		wordfreqPath: writeTemp(t, "wordfreq.tsv", testWordfreq),
		pinyinPath:   writeTemp(t, "phrase.txt", testPhrase),
		singlePath:   writeTemp(t, "single.txt", testSingle),
		charfreqPath: writeTemp(t, "charfreq.csv", charFreqFixture()),
		gsc1:         writeTemp(t, "gsc1.txt", testGSC),
		extraPath:    writeTemp(t, "extra.tsv", testExtra),
		hotPath:      writeTemp(t, "hot.tsv", testHot),
		reviewOut:    filepath.Join(dir, "review.tsv"),
		outPath:      filepath.Join(dir, "out.tsv"),
		highfreqTop:  100,
		freqGamma:    1.0,
	}
}

func runGen(t *testing.T, o options) string {
	t.Helper()
	if err := run(o); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	return o.outPath
}

type parsedDict struct {
	entries map[string]map[string]int
	body    string
}

func parseDict(t *testing.T, path string) parsedDict {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d := parsedDict{entries: map[string]map[string]int{}, body: string(data)}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			t.Fatalf("输出行格式错误: %q", line)
		}
		var freq int
		if _, err := fmt.Sscanf(f[2], "%d", &freq); err != nil {
			t.Fatalf("词频不是整数: %q", line)
		}
		if d.entries[f[0]] == nil {
			d.entries[f[0]] = map[string]int{}
		}
		d.entries[f[0]][f[1]] = freq
	}
	return d
}

func (d parsedDict) has(word, sylls string) bool {
	m := d.entries[word]
	if m == nil {
		return false
	}
	_, ok := m[sylls]
	return ok
}

func TestGenBuildsCommonDictWithPinyinGapFilled(t *testing.T) {
	o := newTestOptions(t)
	d := parseDict(t, runGen(t, o))

	cases := []struct{ word, sylls string }{
		{"经常", "jing chang"},
		{"经济", "jing ji"},
		{"如果", "ru guo"},
		{"什么时候", "shen me shi hou"},
		{"好像", "hao xiang"},
		{"真棒", "zhen bang"},
		{"扫一扫", "sao yi sao"},
	}
	for _, c := range cases {
		if !d.has(c.word, c.sylls) {
			t.Errorf("词库缺少 %s / %s；实际该词条: %v", c.word, c.sylls, d.entries[c.word])
		}
	}
	if len(d.entries["後天"]) != 0 {
		t.Errorf("繁体变体「後天」不应入库: %v", d.entries["後天"])
	}
}

func TestGenWritesPinyinReviewList(t *testing.T) {
	o := newTestOptions(t)
	runGen(t, o)
	data, err := os.ReadFile(o.reviewOut)
	if err != nil {
		t.Fatalf("应输出人工校对清单: %v", err)
	}
	body := string(data)
	for _, want := range []string{"什么时候\tshen me shi hou\tsegment", "好像\thao xiang\tcharstat"} {
		if !strings.Contains(body, want) {
			t.Errorf("校对清单应包含 %q，实际:\n%s", want, body)
		}
	}
	if strings.Contains(body, "经常\t") {
		t.Errorf("phrase 直接命中的词不应进入校对清单:\n%s", body)
	}
}

func TestGenSingleCharsFromGSC(t *testing.T) {
	o := newTestOptions(t)
	d := parseDict(t, runGen(t, o))

	if !d.has("的", "de") || !d.has("的", "di") {
		t.Fatalf("「的」应有两个读音 de/di，实际: %v", d.entries["的"])
	}
	if d.entries["的"]["di"] >= d.entries["的"]["de"] {
		t.Errorf("次读音 di 的词频应低于主读音 de: %v", d.entries["的"])
	}
	if len(d.entries["济"]) != 0 {
		t.Errorf("字表外的「济」不应作为单字入库: %v", d.entries["济"])
	}
	if !d.has("经济", "jing ji") {
		t.Errorf("「经济」多字词应入库")
	}
}

func TestGenFreqMappingAndFallback(t *testing.T) {
	o := newTestOptions(t)
	d := parseDict(t, runGen(t, o))

	if !(d.entries["如果"]["ru guo"] > d.entries["经常"]["jing chang"] &&
		d.entries["经常"]["jing chang"] > d.entries["经济"]["jing ji"]) {
		t.Errorf("词频应随 zipf 单调: 如果=%d 经常=%d 经济=%d",
			d.entries["如果"]["ru guo"], d.entries["经常"]["jing chang"], d.entries["经济"]["jing ji"])
	}
	if got := d.entries["什么时候"]["shen me shi hou"]; got != fallbackFreq(4) {
		t.Errorf("无词频来源的 4 字词应用长度先验 %d，实际 %d", fallbackFreq(4), got)
	}
	for word, m := range d.entries {
		for sylls, freq := range m {
			if freq < 1 || freq > 99 {
				t.Fatalf("词频 %d 超出 1..99（%s/%s）", freq, word, sylls)
			}
		}
	}
}

func TestGenManualWeightWinsWhenHigher(t *testing.T) {
	o := newTestOptions(t)
	d := parseDict(t, runGen(t, o))
	if got := d.entries["扫一扫"]["sao yi sao"]; got != manualFreq(1000) {
		t.Errorf("手工词频应取 manualFreq(1000)=%d，实际 %d", manualFreq(1000), got)
	}
}

func TestGenHanLPMode(t *testing.T) {
	o := newTestOptions(t)
	o.freqMode = "hanlp"
	o.hanlpPath = writeTemp(t, "hanlp.txt", testHanLP)
	d := parseDict(t, runGen(t, o))
	if got := d.entries["经常"]["jing chang"]; got != 99 {
		t.Errorf("HanLP 模式最高频词应得 99，实际 %d", got)
	}
	if !(d.entries["经常"]["jing chang"] > d.entries["经济"]["jing ji"] &&
		d.entries["经济"]["jing ji"] > d.entries["如果"]["ru guo"]) {
		t.Errorf("HanLP 词频应保持单调")
	}
	if !strings.Contains(d.body, "HanLP") || !strings.Contains(d.body, "Apache-2.0") {
		t.Errorf("HanLP 模式文件头应声明来源与 Apache-2.0 许可")
	}
}

func TestGenWFHeaderLicense(t *testing.T) {
	o := newTestOptions(t)
	d := parseDict(t, runGen(t, o))
	for _, want := range []string{"wordfreq", "CC BY-SA 4.0", "代码仍为 MIT", "Jun Da"} {
		if !strings.Contains(d.body, want) {
			t.Errorf("方案 A 文件头应包含 %q", want)
		}
	}
}

func TestGenIdempotent(t *testing.T) {
	o := newTestOptions(t)
	a, err := os.ReadFile(runGen(t, o))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(runGen(t, o))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("两次生成结果不一致（幂等性被破坏）")
	}
}

func TestParseHSKWordList(t *testing.T) {
	p := writeTemp(t, "hsk.txt", "1 爱\n2 爸爸｜爸\n3 白（形）\n一级词汇表\n")
	words, err := parseHSKWordList(p)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(words, ",")
	if got != "爸爸" {
		t.Errorf("HSK 解析结果应为 爸爸（爱/白 单字被过滤），实际 %q", got)
	}
}

func TestParseManualTableRejectsBadRows(t *testing.T) {
	cases := []struct{ name, content string }{
		{"bad-syllable", "好像\thao xiang2\t100\n"},
		{"count-mismatch", "好像\thao\t100\n"},
		{"non-cjk", "hello\tha lo\t100\n"},
		{"bad-weight", "好像\thao xiang\t0\n"},
		{"bad-columns", "好像\thao xiang\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeTemp(t, "manual.tsv", c.content)
			if _, err := parseManualTable(p); err == nil {
				t.Fatalf("应拒绝脏数据 %q", c.content)
			}
		})
	}
}

func TestCharRankToFreqMonotonic(t *testing.T) {
	total := 9000
	prev := 101
	for rank := 1; rank <= total; rank += 17 {
		f := charRankToFreq(rank, total)
		if f < 1 || f > 99 {
			t.Fatalf("rank=%d 得分 %d 超出 1..99", rank, f)
		}
		if f > prev {
			t.Fatalf("rank=%d 得分 %d 大于更低排名的 %d（应单调不增）", rank, f, prev)
		}
		prev = f
	}
	if got := charRankToFreq(1, total); got != 99 {
		t.Fatalf("rank=1 应得 99，实际 %d", got)
	}
	if got := charRankToFreq(total, total); got != 1 {
		t.Fatalf("rank=total 应得 1，实际 %d", got)
	}
}

func TestCJKWordOK(t *testing.T) {
	ok := []string{"的", "经常", "㐀", "〇", "中国经济"}
	bad := []string{"", "hello", "A股", "经常。", "一二三四五六七"}
	for _, w := range ok {
		if !cjkWordOK(w) {
			t.Errorf("%q 应被接受", w)
		}
	}
	for _, w := range bad {
		if cjkWordOK(w) {
			t.Errorf("%q 应被拒绝", w)
		}
	}
}
