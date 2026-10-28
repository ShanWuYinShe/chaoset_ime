package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testJieba = `的 318825 d
是 796991 v
经常 7253 d
经济 48718 n
经 21042 n
jingchang 12 n
hello 500 n
， 1000 x
`

const testPhrasePinyin = `# comment
经常: jīng cháng
经济: jīng jì
京畿: jīng jī
`

const testSinglePinyin = `# comment
U+7684: de,dí,dì  # 的
U+662F: shì  # 是
U+7ECF: jīng  # 经
`

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runGen(t *testing.T, jieba, phrase, single string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.tsv")
	if err := run(jieba, phrase, single, "", out); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	return out
}

// 只收纯 CJK 词；非汉字词（jingchang、hello、标点）被跳过。
func TestGenFiltersNonCJK(t *testing.T) {
	out := runGen(t,
		writeTemp(t, "jieba.txt", testJieba),
		writeTemp(t, "pinyin.txt", testPhrasePinyin),
		writeTemp(t, "single.txt", testSinglePinyin))
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, banned := range []string{"jingchang\t", "hello\t", "，\t"} {
		if strings.Contains(body, banned) {
			t.Fatalf("输出不应包含非汉字词 %q", banned)
		}
	}
	for _, want := range []string{"经常\t", "经济\t", "经\t", "的\t", "是\t"} {
		if !strings.Contains(body, want) {
			t.Fatalf("输出缺少词条 %q", want)
		}
	}
}

// 拼音归一化：声调符号去掉、音节逐条校验。
func TestGenNormalizesPinyin(t *testing.T) {
	out := runGen(t,
		writeTemp(t, "jieba.txt", testJieba),
		writeTemp(t, "pinyin.txt", testPhrasePinyin),
		writeTemp(t, "single.txt", testSinglePinyin))
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "经常\tjing chang\t") {
		t.Fatalf("「经常」拼音应为 jing chang（去声调），输出:\n%s", data)
	}
}

// 词频对数压缩：落在 1..99，且高频词得分不低于低频词。
func TestGenFreqScaling(t *testing.T) {
	out := runGen(t,
		writeTemp(t, "jieba.txt", testJieba),
		writeTemp(t, "pinyin.txt", testPhrasePinyin),
		writeTemp(t, "single.txt", testSinglePinyin))
	data, _ := os.ReadFile(out)
	freqs := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			t.Fatalf("行格式错误: %q", line)
		}
		var n int
		for _, r := range f[2] {
			n = n*10 + int(r-'0')
		}
		if n < 1 || n > 99 {
			t.Fatalf("词频 %d 超出 1..99（词 %q）", n, f[0])
		}
		freqs[f[0]] = n
	}
	if freqs["是"] != 99 {
		t.Errorf("最高频词应得 99，得到 %d", freqs["是"])
	}
	// 原始词频：是 796991 > 经 21042 > 经常 7253 → 压缩后必须保持同一顺序
	if !(freqs["是"] > freqs["经"] && freqs["经"] > freqs["经常"]) {
		t.Errorf("词频应随原始词频单调: 是=%d 经=%d 经常=%d", freqs["是"], freqs["经"], freqs["经常"])
	}
}

// 幂等：同一输入重复运行输出逐字节一致。
func TestGenIdempotent(t *testing.T) {
	jieba := writeTemp(t, "jieba.txt", testJieba)
	phrase := writeTemp(t, "pinyin.txt", testPhrasePinyin)
	single := writeTemp(t, "single.txt", testSinglePinyin)
	a, _ := os.ReadFile(runGen(t, jieba, phrase, single))
	b, _ := os.ReadFile(runGen(t, jieba, phrase, single))
	if string(a) != string(b) {
		t.Fatal("两次生成结果不一致（幂等性被破坏）")
	}
}

// 无词组拼音的非单字词被丢弃（不能凭猜测补拼音）。
func TestGenDropsWordWithoutPinyin(t *testing.T) {
	jieba := writeTemp(t, "jieba.txt", "未知词 100 n\n经常 7253 d\n")
	out := runGen(t, jieba, writeTemp(t, "pinyin.txt", testPhrasePinyin), writeTemp(t, "single.txt", testSinglePinyin))
	data, _ := os.ReadFile(out)
	if strings.Contains(string(data), "未知词") {
		t.Fatal("没有拼音数据的词不应入库")
	}
	if !strings.Contains(string(data), "经常") {
		t.Fatal("有拼音数据的词应入库")
	}
}

// 拼音音节数与字数不符时丢弃（防止错位拼音进词库）。
func TestGenDropsSyllableCountMismatch(t *testing.T) {
	jieba := writeTemp(t, "jieba.txt", "经常 7253 d\n")
	phrase := writeTemp(t, "pinyin.txt", "经常: jīng\n") // 只有一个音节
	out := runGen(t, jieba, phrase, writeTemp(t, "single.txt", testSinglePinyin))
	data, _ := os.ReadFile(out)
	if strings.Contains(string(data), "经常") {
		t.Fatal("音节数与字数不符的词应被丢弃")
	}
}

// cjkWordOK 只接受 CJK 汉字且长度受限。
func TestCJKWordOK(t *testing.T) {
	ok := []string{"的", "经常", "㐀", "〇", "abcdef"[:0] + "中国经济"}
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
