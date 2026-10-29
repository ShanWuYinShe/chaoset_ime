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
	// corpus 传空：测试不依赖真实语料，且校正逻辑只影响单字词频分配。
	if err := run(jieba, phrase, single, "", "", out); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	return out
}

// runGenFull 同 runGen，但可传 extra 与 corpus（用于测词频校正路径）。
func runGenFull(t *testing.T, jieba, phrase, single, extra, corpus string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.tsv")
	if err := run(jieba, phrase, single, extra, corpus, out); err != nil {
		t.Fatalf("run 失败: %v", err)
	}
	return out
}

func readBody(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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

// TestMultiReadingSingleChars：多音字的**每个读音都要生成词条**。
//
// 2026-10-29 修复：pinyinOf 此前遇到第一个合法读音就 return，导致 3,358 个
// 多音字只保留 1 个读音——常用字直接打不出来（chang 无「长」、chong 无
// 「重」、de 无「地」、huo 无「和」，实测 11/15 缺失）。函数注释本就写着
// 「多音字每个读音一条」，属代码与注释不一致。
func TestMultiReadingSingleChars(t *testing.T) {
	jieba := writeTemp(t, "jieba.txt", `长 50000 n
`)
	single := writeTemp(t, "single.txt", `U+957F: zhǎng,cháng  # 长
`)
	body := readBody(t, runGen(t, jieba, writeTemp(t, "p.txt", ""), single))

	// 两个读音都必须出现（同词不同拼音是合法的两条词条）
	for _, want := range []string{"长	chang	", "长	zhang	"} {
		if !strings.Contains(body, want) {
			t.Fatalf("多音字「长」缺少读音条目 %q，实际输出：\n%s", want, body)
		}
	}
}

// TestMultiReadingSecondaryDownweighted：次读音词频必须低于主读音。
//
// 若所有读音同分，「没」的 me/mo 读音会把「怎没」顶成合法候选
// （2026-10-29 实测 zenme 第 2 位原本是「怎嚜」，同分后变成「怎没」）。
func TestMultiReadingSecondaryDownweighted(t *testing.T) {
	jieba := writeTemp(t, "jieba.txt", `没 50000 v
`)
	single := writeTemp(t, "single.txt", `U+6CA1: méi,mò,me  # 没
`)
	body := readBody(t, runGen(t, jieba, writeTemp(t, "p.txt", ""), single))

	freqs := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		f := strings.Split(line, "	")
		if len(f) >= 3 && f[0] == "没" {
			freqs[f[1]] = f[2]
		}
	}
	if len(freqs) != 3 {
		t.Fatalf("「没」应有 3 个读音条目，实际 %d：%v", len(freqs), freqs)
	}
	primary := freqs["mei"]
	for reading, f := range freqs {
		if reading == "mei" {
			continue
		}
		if f >= primary {
			t.Fatalf("次读音 %s 的词频 %s 应低于主读音 mei 的 %s", reading, f, primary)
		}
	}
}

// TestCorpusZeroRecordDownweight：语料零记录的单字压到最低档（保留可达性）。
func TestCorpusZeroRecordDownweight(t *testing.T) {
	jieba := writeTemp(t, "jieba.txt", `是 100000 v
㐀 9000 g
`)
	single := writeTemp(t, "single.txt", `U+662F: shì  # 是
U+3400: qiū  # 㐀
`)
	corpus := writeTemp(t, "corpus.txt", "是	50000\n")
	// 测试工作目录是包目录，覆盖 OpenCC 表路径（相对仓库根时要 ../）
	// 用绝对路径：测试进程的 cwd 不保证是包目录
	abs, err := filepath.Abs("../../engine/dp_data/STCharacters.txt")
	if err != nil {
		t.Fatal(err)
	}
	oldPath := stCharactersPath
	stCharactersPath = abs
	t.Cleanup(func() { stCharactersPath = oldPath })

	body := readBody(t, runGenFull(t, jieba, writeTemp(t, "p.txt", ""), single, "", corpus))

	for _, line := range strings.Split(body, "\n") {
		f := strings.Split(line, "	")
		if len(f) >= 3 && f[0] == "㐀" && f[2] != "1" {
			t.Fatalf("语料零记录的「㐀」词频应压到 1，实际 %s", f[2])
		}
	}
}
