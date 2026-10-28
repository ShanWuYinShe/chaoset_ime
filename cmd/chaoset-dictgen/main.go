// chaoset-dictgen 从开放数据生成内置常用词库（data/dict_common.tsv）。
//
// 数据来源（均为 MIT 许可，生成结果需保留署名，见输出文件头）：
//   - 词频：jieba 词频表 dict.txt（https://github.com/fxsjy/jieba）
//   - 词语拼音：phrase-pinyin-data large_pinyin.txt
//     （https://github.com/mozillazg/phrase-pinyin-data）
//   - 单字拼音：pinyin-data pinyin.txt（同上仓库，多音字取第一个读音）
//
// 用法：
//
//	chaoset-dictgen -jieba dict.txt -pinyin large_pinyin.txt \
//	    -single pinyin.txt -o data/dict_common.tsv
//
// 生成规则：
//   - 只收纯 CJK 汉字的词（基本区/扩展 A/兼容区/〇），长度 1..6；
//   - 拼音经 engine.NormalizePinyin 归一化并逐音节校验，非法音节整词丢弃
//     （与词库导入同一套规则，防止脏数据进词库）；
//   - 词频按 ln(f)/ln(max) 对数压缩到 1..99：静态词频只是基础分，
//     用户学习权重（每次上屏 500）远高于整个区间，压缩不损失调频效果；
//   - 输出按「词频降序、同频按词排序」，同一输入重复运行逐字节一致。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"chaoset.ime/engine"
)

// maxWordLen 是内置词库收录的最长词长（字符数）。再长的词组输入成本高、
// 命中率低，且会把词库撑大。
const maxWordLen = 6

// entry 是待写入的一条词库记录。
type entry struct {
	word  string
	sylls []string
	freq  int
}

func main() {
	jiebaPath := flag.String("jieba", "", "jieba 词频表 dict.txt 路径（必填）")
	pinyinPath := flag.String("pinyin", "", "phrase-pinyin-data large_pinyin.txt 路径（必填）")
	singlePath := flag.String("single", "", "pinyin-data pinyin.txt 路径（单字拼音；缺省时跳过无词组拼音的单字）")
	extraPath := flag.String("extra", "", "补充词表 TSV 路径（词\t拼音\t词频 1..99；补录词频表漏收的常用词组，如真好/难得）")
	out := flag.String("o", "", "输出 TSV 路径（必填）")
	flag.Parse()
	if *jiebaPath == "" || *pinyinPath == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*jiebaPath, *pinyinPath, *singlePath, *extraPath, *out); err != nil {
		fmt.Fprintln(os.Stderr, "chaoset-dictgen:", err)
		os.Exit(1)
	}
}

func run(jiebaPath, pinyinPath, singlePath, extraPath, outPath string) error {
	rawFreqs, err := parseJieba(jiebaPath)
	if err != nil {
		return err
	}
	phrasePinyin, err := parsePhrasePinyin(pinyinPath)
	if err != nil {
		return err
	}
	singlePinyin := map[rune][]string{}
	if singlePath != "" {
		singlePinyin, err = parseSinglePinyin(singlePath)
		if err != nil {
			return err
		}
	}

	// 词频压缩基准：取输入里的最大词频，保证最高频词映射到 99。
	maxFreq := 0
	for _, wf := range rawFreqs {
		if wf.freq > maxFreq {
			maxFreq = wf.freq
		}
	}
	if maxFreq <= 0 {
		return fmt.Errorf("词频表 %s 没有有效词频", jiebaPath)
	}

	// 补充词表（-extra）：词频表漏收的常用词组（如「真好」——口语高频
	// 组合，jieba 词频表未收录，导致组句时「真+好」双单字拼合候选被
	// 碎片过滤误杀）。格式与输出一致：词\t拼音(空格分隔)\t词频(原始计数)。
	// 与 jieba 源按 (词,拼音) 去重：补充条目优先（同名保留较大词频）。
	type extraEntry struct {
		word  string
		sylls []string
		freq  int
	}
	var extras []extraEntry
	if extraPath != "" {
		data, err := os.ReadFile(extraPath)
		if err != nil {
			return fmt.Errorf("打开补充词表 %s 失败: %w", extraPath, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) != 3 {
				return fmt.Errorf("补充词表 %s 格式非法（应为 词\t拼音\t词频）: %q", extraPath, line)
			}
			freq := 0
			fmt.Sscanf(parts[2], "%d", &freq)
			if freq <= 0 {
				return fmt.Errorf("补充词表 %s 词频非法: %q", extraPath, line)
			}
			extras = append(extras, extraEntry{
				word:  parts[0],
				sylls: strings.Fields(parts[1]),
				freq:  freq,
			})
		}
	}
	// extra 词频参与 maxFreq 压缩基准（与主源同一量纲——extra 用原始计数）
	for _, e := range extras {
		if e.freq > maxFreq {
			maxFreq = e.freq
		}
	}

	// 繁体剔除的判定集合：主源 + extra 补充词都纳入（extra 是简体手工
	// 词条，正常不会引入繁体，但集合覆盖它们保持判定一致）
	simplifiedExists := make(map[string]bool, len(rawFreqs)+len(extras))
	for _, wf := range rawFreqs {
		if cjkWordOK(wf.word) {
			simplifiedExists[engine.ToSimplified(wf.word)] = true
		}
	}
	for _, e := range extras {
		simplifiedExists[engine.ToSimplified(e.word)] = true
	}

	entries := make([]entry, 0, len(rawFreqs))
	skipped := 0
	tradSkipped := 0
	for _, wf := range rawFreqs {
		word, freq := wf.word, wf.freq
		if !cjkWordOK(word) {
			skipped++
			continue
		}
		if sw := engine.ToSimplified(word); sw != word {
			if simplifiedExists[sw] {
				tradSkipped++
				continue
			}
		}
		sylls := pinyinOf(word, phrasePinyin, singlePinyin)
		if sylls == nil {
			skipped++
			continue
		}
		entries = append(entries, entry{word: word, sylls: sylls, freq: scaleFreq(freq, maxFreq)})
	}

	// 补充词条追加（-extra 手工维护表）：同样过 CJK 与音节合法性检查
	//（非法音节必须拦截——AGENTS 红线），非法条目计入 skipped 不阻断。
	// 补充词的音节由维护者手写（不查拼音表），逐音节 IsSyllable 校验。
	// 与主源 (词,拼音) 重复的条目跳过（避免输出 TSV 出现重复行）。
	mainSource := make(map[string]bool, len(entries))
	for _, e := range entries {
		mainSource[e.word+"\x00"+strings.Join(e.sylls, " ")] = true
	}
	for _, e := range extras {
		if !cjkWordOK(e.word) {
			skipped++
			continue
		}
		bad := false
		for _, syl := range e.sylls {
			if !engine.IsSyllable(syl) {
				bad = true
				break
			}
		}
		if bad {
			skipped++
			continue
		}
		if mainSource[e.word+"\x00"+strings.Join(e.sylls, " ")] {
			skipped++ // 主源已有该词条（避免输出重复行）
			continue
		}
		entries = append(entries, entry{word: e.word, sylls: e.sylls, freq: scaleFreq(e.freq, maxFreq)})
	}

	// 确定性排序：词频降序、同频按词、再按音节（同词多音的稳定次序）
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.freq != b.freq {
			return a.freq > b.freq
		}
		if a.word != b.word {
			return a.word < b.word
		}
		return strings.Join(a.sylls, " ") < strings.Join(b.sylls, " ")
	})

	if err := writeTSV(outPath, entries); err != nil {
		return err
	}
	fmt.Printf("已写入 %s（%d 条，跳过 %d 条：非汉字/无拼音/非法音节，其中繁体变体 %d 条）\n", outPath, len(entries), skipped, tradSkipped)
	return nil
}

// wordFreq 是 jieba 词频表的一行。
type wordFreq struct {
	word string
	freq int
}

// parseJieba 解析 jieba dict.txt：每行「词 词频 [词性...]」，空格分隔。
// 词频非数字或缺失的行跳过（jieba 表里有少量纯符号行）。
func parseJieba(path string) ([]wordFreq, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开词频表 %s 失败: %w", path, err)
	}
	defer f.Close()
	var out []wordFreq
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		freq := 0
		for _, r := range fields[1] {
			if r < '0' || r > '9' {
				freq = -1
				break
			}
			freq = freq*10 + int(r-'0')
		}
		if freq <= 0 {
			continue
		}
		out = append(out, wordFreq{word: fields[0], freq: freq})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取词频表 %s 失败: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("词频表 %s 没有可用条目", path)
	}
	return out, nil
}

// parsePhrasePinyin 解析 large_pinyin.txt：每行「词: pīn yīn」。
// 拼音原样保留（含声调符号），归一化交给 engine.NormalizePinyin。
func parsePhrasePinyin(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开词语拼音表 %s 失败: %w", path, err)
	}
	defer f.Close()
	out := make(map[string]string, 400000)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		word, pinyin, ok := strings.Cut(line, ": ")
		if !ok || word == "" || pinyin == "" {
			continue
		}
		out[word] = pinyin
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取词语拼音表 %s 失败: %w", path, err)
	}
	return out, nil
}

// parseSinglePinyin 解析 pinyin-data pinyin.txt：每行「U+XXXX: pīn,yīn  # 字」。
// 多音字保留全部读音（按出现顺序），供单字多音词条使用。
func parseSinglePinyin(path string) (map[rune][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开单字拼音表 %s 失败: %w", path, err)
	}
	defer f.Close()
	out := make(map[rune][]string, 21000)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		code, rest, ok := strings.Cut(line, ": ")
		if !ok || !strings.HasPrefix(code, "U+") {
			continue
		}
		// 去掉行尾注释
		if i := strings.IndexByte(rest, '#'); i >= 0 {
			rest = rest[:i]
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			continue
		}
		cp := 0
		for _, c := range code[2:] {
			var v int
			switch {
			case c >= '0' && c <= '9':
				v = int(c - '0')
			case c >= 'A' && c <= 'F':
				v = int(c-'A') + 10
			case c >= 'a' && c <= 'f':
				v = int(c-'a') + 10
			default:
				cp = -1
			}
			if cp < 0 {
				break
			}
			cp = cp*16 + v
		}
		if cp < 0 || cp > 0x10FFFF {
			continue
		}
		var readings []string
		for _, r := range strings.Split(rest, ",") {
			if r = strings.TrimSpace(r); r != "" {
				readings = append(readings, r)
			}
		}
		if len(readings) > 0 {
			out[rune(cp)] = readings
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取单字拼音表 %s 失败: %w", path, err)
	}
	return out, nil
}

// pinyinOf 返回词的音节切片：优先词组拼音表，单字回退单字拼音表
// （多音字每个读音一条，按读音顺序）。都无法归一化时返回 nil。
func pinyinOf(word string, phrase map[string]string, single map[rune][]string) []string {
	if py, ok := phrase[word]; ok {
		if sylls := engine.NormalizePinyin(py); sylls != nil && len(sylls) == len([]rune(word)) {
			return sylls
		}
	}
	runes := []rune(word)
	if len(runes) != 1 {
		return nil
	}
	readings, ok := single[runes[0]]
	if !ok {
		return nil
	}
	for _, r := range readings {
		if sylls := engine.NormalizePinyin(r); len(sylls) == 1 {
			return sylls
		}
	}
	return nil
}

// cjkWordOK 判断词是否只含 CJK 汉字（基本区、扩展 A、兼容表意区、〇），
// 且长度在 1..maxWordLen 之间。
func cjkWordOK(word string) bool {
	runes := []rune(word)
	if len(runes) == 0 || len(runes) > maxWordLen {
		return false
	}
	for _, r := range runes {
		switch {
		case r >= 0x3400 && r <= 0x4DBF: // CJK 扩展 A
		case r >= 0x4E00 && r <= 0x9FFF: // CJK 基本区
		case r >= 0xF900 && r <= 0xFAFF: // CJK 兼容表意区
		case r == 0x3007: // 〇
		default:
			return false
		}
	}
	return true
}

// scaleFreq 把原始词频按对数压缩到 1..99：最高频词得 99，最低得 1。
// 对数压缩保证常用词的相对次序不变，同时把生僻词压到低分档，
// 避免它们和常用词同分后按码点排序挤掉常用字。
func scaleFreq(freq, maxFreq int) int {
	if freq <= 0 {
		return 1
	}
	if freq >= maxFreq {
		return 99
	}
	v := 1 + 98*math.Log(float64(freq))/math.Log(float64(maxFreq))
	n := int(math.Round(v))
	if n < 1 {
		n = 1
	}
	if n > 99 {
		n = 99
	}
	return n
}

// writeTSV 写出三列 TSV，文件头记录来源与许可以及再生成命令。
func writeTSV(path string, entries []entry) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 1<<16)
	header := []string{
		"# chaoset 内置常用词库（由 cmd/chaoset-dictgen 生成，禁止手工编辑）",
		"# 格式：词 \\t 拼音（空格分隔，无声调，ü 以 v 输入） \\t 词频（1..99）",
		"#",
		"# 数据来源（均为 MIT 许可）：",
		"#   词频   jieba dict.txt          https://github.com/fxsjy/jieba",
		"#   拼音   phrase-pinyin-data      https://github.com/mozillazg/phrase-pinyin-data",
		"#   单字   pinyin-data             https://github.com/mozillazg/pinyin-data",
		"#",
		"# 再生成（在仓库根目录）：",
		"#   go run ./cmd/chaoset-dictgen -jieba dict.txt \\",
		"#     -pinyin large_pinyin.txt -single pinyin.txt -o data/dict_common.tsv",
		"# 同一输入重复运行输出逐字节一致（已排序，不含时间戳）。",
	}
	for _, line := range header {
		if _, err := fmt.Fprintln(bw, line); err != nil {
			return fmt.Errorf("写入词库头失败: %w", err)
		}
	}
	for _, e := range entries {
		if _, err := fmt.Fprintf(bw, "%s\t%s\t%d\n", e.word, strings.Join(e.sylls, " "), e.freq); err != nil {
			return fmt.Errorf("写入词库条目失败: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("写出词库失败: %w", err)
	}
	return nil
}
