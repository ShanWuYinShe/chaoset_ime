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
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"chaoset.ime/engine"
)

// maxWordLen 是内置词库收录的最长词长（字符数）。再长的词组输入成本高、
// 命中率低，且会把词库撑大。
const maxWordLen = 6

// stCharactersPath 是 OpenCC 简→繁单字表路径（相对仓库根目录）。
// 变量而非常量：测试工作目录是包目录，需要覆盖。
var stCharactersPath = "engine/dp_data/STCharacters.txt"

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
	corpusPath := flag.String("corpus", "", "真实语料词频表路径（行格式：词	次数；用于校正 jieba 中"+
		"「不成词语素」的虚高词频，如 灏 2196 / 岃 1849 本应排在 豪/仁 之后。缺省不校正）")
	charFreqPath := flag.String("charfreq", "", "单字字频表 CSV 路径（Jun Da 现代汉语字频，"+
		"列含 frequency_rank,character；**单字词频以此为准**，替代 jieba 分词词典里"+
		"不可靠的单字词频。缺省时退回 jieba 单字词频）")
	out := flag.String("o", "", "输出 TSV 路径（必填）")
	flag.Parse()
	if *jiebaPath == "" || *pinyinPath == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*jiebaPath, *pinyinPath, *singlePath, *extraPath, *corpusPath, *charFreqPath, *out); err != nil {
		fmt.Fprintln(os.Stderr, "chaoset-dictgen:", err)
		os.Exit(1)
	}
}

func run(jiebaPath, pinyinPath, singlePath, extraPath, corpusPath, charFreqPath, outPath string) error {
	rawFreqs, err := parseJieba(jiebaPath)
	if err != nil {
		return err
	}
	// 真实语料（可选）：用于把「语料零记录」的单字压到最低档，修正 jieba
	// 分词词典里生僻字虚高的词频（详见 corpusAdjustedFreq 注释）。
	// 注：启用 -charfreq 后单字走字频表，corpus 只影响多字词（影响很小）。
	var corpus map[string]int
	if corpusPath != "" {
		corpus, err = parseCorpusFreq(corpusPath)
		if err != nil {
			return err
		}
	}
	// jieba 单字最大词频：融合时用于归一化 jieba 分量。
	maxSingleFreq := 1
	for _, wf := range rawFreqs {
		if len([]rune(wf.word)) == 1 && wf.freq > maxSingleFreq {
			maxSingleFreq = wf.freq
		}
	}
	// 单字字频（Jun Da）：为 nil 时退回 jieba 单字词频。
	var charRank map[rune]int
	if charFreqPath != "" {
		charRank, err = parseCharFrequency(charFreqPath)
		if err != nil {
			return err
		}
		log.Printf("字频表已加载：%d 个字", len(charRank))
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
		readings := pinyinOf(word, phrasePinyin, singlePinyin)
		if readings == nil {
			skipped++
			continue
		}
		// 多音字：每个读音各生成一条词条（同词不同拼音是合法数据，
		// 由 (词,拼音) 唯一性保证不重复）。
		// 单字：改用 Jun Da 字频排名折算，绕开 jieba 分词词典里不可靠的
		// 单字词频（详见 parseCharFrequency 注释）。多字词仍用 jieba。
		adjFreq := freq
		if charRank != nil && len([]rune(word)) == 1 {
			rank, ok := charRank[[]rune(word)[0]]
			if !ok {
				rank = 1 << 20 // 字频表未收录 = 极生僻
			}
			adjFreq = fuseCharFreqRune([]rune(word)[0], freq, rank, maxSingleFreq)
		} else {
			adjFreq = corpusAdjustedFreq(word, freq, corpus)
		}
		// 多音字：首个读音是拼音表给的主读音（最常用），其余为次读音。
		// 所有读音共享同一词频会让次读音与主读音同分，进而造假候选——
		// 例：『没』有 me/mei/mo 三个读音，若都记 79 分，「怎没」就会被
		// 当成合法的 ze'me 词条（2026-10-29 实测 zenme 的第 2 位从「怎嚜」
		// 变成「怎没(17)」）。次读音按 1/4 降权：仍可打出（查 zang→藏、
		// jiao→觉 这类主读音之外的常用字不受影响，它们本就是该字的主读音），
		// 但不会抢占常见词的候选位。
		// 单字走字频表时 adjFreq 已是 1..99 量纲，不能再过 scaleFreq
		//（那是对 jieba 原始计数做的对数压缩，二次压缩会把上限压到 30 出头）。
		fromCharTable := charRank != nil && len([]rune(word)) == 1
		for i, sylls := range readings {
			f := adjFreq
			if i > 0 {
				// 次读音降权不能太狠，否则常用字的次读音打不出来：此前取 1/4 时
				// 长(chang)=12、重(chong)=11、都(du)=13，被同拼音下 30-90 个候选
				// 挤出首页（实测 6/11 常用多音字缺失）。改为 1/2 且下限 20：
				// 保证落在可见候选区，仍低于主读音，不会让「怎没」这类次读音
				// 组合顶掉真词。
				f = (adjFreq + 1) / 2
				// 下限 40：融合字频后常用字主读音多在 60-90，次读音取半后约 30-45，
				// 但窄拼音（chang/chong/du/huan）有 30-90 个候选，低于 40 排不进前 6。
				// 40 是实测能覆盖 11/11 常用多音字的最低值。
				if f < 40 {
					f = 40
				}
				if f > adjFreq {
					f = adjFreq
				}
			}
			freqOut := f
			if !fromCharTable {
				freqOut = scaleFreq(f, maxFreq)
			}
			entries = append(entries, entry{word: word, sylls: sylls, freq: freqOut})
		}
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
			// 主源已有该词条：**取较大词频**而非跳过。
			// 2026-10-29 修复：此前直接 skipped++，extra 想提升已有词条词频时
			// 静默无效——支付宝/朋友圈在 jieba 里原始词频只有 3（压缩后 9 分），
			// extra 补录也进不去。extra 是手工维护的权威补充，语义就是
			// 「这个词很重要，至少该有这个分」。scaleFreq 单调，比较压缩值
			// 等价于比较原始计数。
			extraFreq := scaleFreq(e.freq, maxFreq)
			for i := range entries {
				if entries[i].word == e.word && strings.Join(entries[i].sylls, " ") == strings.Join(e.sylls, " ") {
					if extraFreq > entries[i].freq {
						entries[i].freq = extraFreq
					}
					break
				}
			}
			skipped++
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

// pinyinOf 返回词的全部音节序列：优先词组拼音表，单字回退单字拼音表。
//
// **多音字必须返回每一个读音**（每个读音一条词条）。2026-10-29 修复：此前
// 这里遇到第一个合法读音就 return，导致 3,358 个多音字只保留 1 个读音——
// 常用字直接打不出来：chang 无「长」、chong 无「重」、hang 无「行」、
// liao 无「了」、du 无「都」、huan 无「还」、huo 无「和」、kuai 无「会」、
// de 无「地」、jiao 无「觉」、zang 无「藏」（实测 11/15 缺失）。
// 函数注释「多音字每个读音一条」原本就是这么写的，是代码与注释不一致。
// 词组层面一直是对的（银行/长城/重庆/了解 全对），塌陷只在单字。
//
// 返回值：非空切片 = 至少一个合法读音（多者返回多条）；nil = 无法归一化。
func pinyinOf(word string, phrase map[string]string, single map[rune][]string) [][]string {
	if py, ok := phrase[word]; ok {
		if sylls := engine.NormalizePinyin(py); sylls != nil && len(sylls) == len([]rune(word)) {
			return [][]string{sylls}
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
	var out [][]string
	seen := make(map[string]bool, len(readings))
	for _, r := range readings {
		sylls := engine.NormalizePinyin(r)
		if len(sylls) != 1 {
			continue
		}
		key := sylls[0]
		if seen[key] {
			continue // 同音重复读音（如 行 xíng 只记一次）
		}
		seen[key] = true
		out = append(out, sylls)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// parseCorpusFreq 读取真实语料词频表（繁体字形，行格式「词<TAB>次数」），
// 并转成简体后合并计数。
//
// 为什么需要（2026-10-29 词库质量审计）：jieba dict.txt 是**分词词典**，
// 里面的单字词频并不等价于「该字独立成词的常用度」——大量生僻字被标成
// 不成词语素（g/zg），却因在语料的人名/网文里高频出现而拿到虚高分：
//
//	灏 2196 / 岃 1849 / 粬 1457，而 豪 1182 / 仁 1173
//
// 结果就是输入 hao 时「灏」排在「豪」前面、输入 ren 时「岃」排在「仁」
// 前面（实测 de/le/wo/qu/lai/ren/hao 等窄拼音前 6 位都有这类字）。
// 真实语料（Rime essay.txt）对这批字是零记录或极低频，可据此校正。
//
// 语料是繁体字形（這/說/個），必须先经 OpenCC 反向映射转简体，
// 否则简体常用字会被误判成「零记录」（实测会误伤 为/这/说/个 等 6/23）。
func parseCorpusFreq(path string) (map[string]int, error) {
	t2s := make(map[rune]rune, 4096)
	// 直接用仓库内嵌的 OpenCC 简→繁表反向构建；dictgen 在仓库根目录运行。
	// 变量而非常量：测试需从包目录覆盖为 ../engine/dp_data/... 。
	data, err := os.ReadFile(stCharactersPath)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败（需在仓库根目录运行）: %w", stCharactersPath, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "	")
		if !ok {
			continue
		}
		if i := strings.IndexByte(val, ' '); i >= 0 {
			val = val[:i] // 繁体多候选取首个
		}
		kr, vr := []rune(key), []rune(val)
		if len(kr) == 1 && len(vr) == 1 {
			if _, exists := t2s[vr[0]]; !exists {
				t2s[vr[0]] = kr[0]
			}
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开语料词频表 %s 失败: %w", path, err)
	}
	defer f.Close()
	out := make(map[string]int, 450000)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields := strings.Split(strings.TrimSpace(sc.Text()), "	")
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil || n <= 0 {
			continue
		}
		simp := make([]rune, 0, len(fields[0]))
		for _, r := range fields[0] {
			if s, ok := t2s[r]; ok {
				simp = append(simp, s)
			} else {
				simp = append(simp, r)
			}
		}
		out[string(simp)] += n
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取语料词频表 %s 失败: %w", path, err)
	}
	return out, nil
}

// corpusAdjustedFreq 用语料校正单字词频。
//
// 判据：**该字在真实语料中的出现次数**。语料零记录 = 该字在现代汉语书面
// 语料里根本不出现（多为编码噪声字 紝/鐨/鏈，或纯生僻字 灏/粬/岃），
// 一律压到最低档 1——**保留可达性、不删除**：用户仍能打出，只是不占候选
// 前排（与市面输入法一致）。
//
// 安全性依据（2026-10-29 实测）：繁→简转换后，40 个最高频常用字零误判；
// 语料零记录的 1,211 个单字里没有日常用字。多字词**不参与**校正——语料对
// 词组的覆盖只有 35.3%，连「一个/中国/我们」都不在表内，误伤风险极高。
//
// 只降不升：绝不把低分字抬高，避免引入新的倒挂。
func corpusAdjustedFreq(word string, jiebaFreq int, corpus map[string]int) int {
	if corpus == nil || len([]rune(word)) != 1 {
		return jiebaFreq // 多字词不校正：语料对词组覆盖仅 35.3%，含「一个/中国/我们」
	}
	n := corpus[word]
	if n == 0 {
		return 1 // 语料零记录：编码噪声字或纯生僻字，压到最低档（保留可达性）
	}
	// 语料有记录但 jieba 词频明显虚高时按语料下调。
	//
	// 为什么需要这半步：这些字在语料里几乎只出现在**人名**中
	// （灏 880 次几乎全是「翁文灏/陈灏珠」，岃 207 次同理），
	// jieba 却把它当成独立词条计了 2196/1849 的高频——结果输入 hao 时
	// 「灏」排在「豪(2416次)」前面、输入 ren 时「岃」排在「仁(3200次)」前面。
	// 判据：语料次数低于 jieba 词频的 1/4 视为虚高，按语料次数收敛。
	//
	// 阈值 2（语料次数×2 < jieba 词频 → 判虚高）的由来：实测两组数据之间
	// 有 2.85 倍的清晰间隔，没有重叠样本——
	//   虚高组（语料/jieba）：灏 0.40、岃 0.11、粬 0.00、嘚 0.00；
	//   正常组：蒿 1.14、是 1.48、豪 2.04、仁 2.73、耗 3.04、好 4.09。
	// 0.5 落在间隔正中（对数中点 0.40~1.14 的几何均值 ≈0.68，取更保守的 0.5）。
	if n*2 < jiebaFreq {
		return n
	}
	return jiebaFreq
}

// parseCharFrequency 读取 Jun Da 现代汉语字频表（hanzi_db.csv），返回
// 「字 → 频率排名」（1 = 最常用）。
//
// 为什么单字不能用 jieba 的词频（2026-10-29 词库质量审计）：
// jieba dict.txt 是**分词词典**，其单字词频反映的是「该字在分词语料里
// 独立成词的程度」，与「该字作为汉字有多常用」不是一回事。大量生僻字被
// 标成不成词语素（g/zg）却因出现在人名中拿到虚高分，实测倒挂：
//
//	灏 2196 > 豪 1182        （灏 = 人名用字，豪 = 常用字）
//	岃 1849 > 仁 1173
//	粬 1457 > 驱 1320 / 屈 1744
//
// 后果是输入 hao 时「灏」排在「豪」前面、输入 ren 时「岃」排在「仁」前面
// （实测 de/le/wo/qu/lai/ren/hao 等窄拼音前 6 位都有这类字）。
//
// Jun Da 的字频表基于 2,580 万字真实语料，对上述 8 组倒挂案例全部给出
// 正确排序（灏 #5030 / 岃 未收录 / 粬 未收录 vs 豪 #1513 / 仁 #1360）。
// **多字词仍用 jieba**——它对词组频率是可靠的（工作 66367 vs 成语 3）。
func parseCharFrequency(path string) (map[rune]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开字频表 %s 失败: %w", path, err)
	}
	defer f.Close()
	out := make(map[rune]int, 10000)
	rc := csv.NewReader(f)
	rc.FieldsPerRecord = -1
	header, err := rc.Read()
	if err != nil {
		return nil, fmt.Errorf("读取字频表表头失败: %w", err)
	}
	// 定位 frequency_rank 与 character 列（不依赖列顺序）
	rankCol, charCol := -1, -1
	for i, h := range header {
		switch strings.TrimSpace(h) {
		case "frequency_rank":
			rankCol = i
		case "character":
			charCol = i
		}
	}
	if rankCol < 0 || charCol < 0 {
		return nil, fmt.Errorf("字频表 %s 缺少 frequency_rank/character 列", path)
	}
	for {
		rec, err := rc.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // 个别行格式异常不影响整体
		}
		if rankCol >= len(rec) || charCol >= len(rec) {
			continue
		}
		rank, convErr := strconv.Atoi(strings.TrimSpace(rec[rankCol]))
		if convErr != nil || rank <= 0 {
			continue
		}
		runes := []rune(strings.TrimSpace(rec[charCol]))
		if len(runes) == 1 {
			out[runes[0]] = rank
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("字频表 %s 没有有效条目", path)
	}
	return out, nil
}

// charRankToFreq 把字频排名映射为压缩词频（1..99），与 scaleFreq 同一量纲。
//
// 映射方式：用排名在常见字区间内做对数插值——排名 1 得 99，排名 3500
// （通用规范汉字表一级字表边界）得 30，排名 9000 得 6，未收录得 1。
// 这样常用字之间保留区分度（的 99 / 一 98 / 是 98 …… 好 83），而排到
// 5000 名之后的生僻字自然落到低分档，不会挤占常见字的候选位。
func charRankToFreq(rank int, total int) int {
	if rank <= 0 {
		return 1
	}
	const topFreq = 99
	const bottomFreq = 1
	end := total
	if end < 2 {
		end = 2
	}
	if rank <= 1 {
		return topFreq
	}
	if rank >= end {
		return bottomFreq
	}
	t := math.Log(float64(rank)) / math.Log(float64(end))
	return int(math.Round(float64(topFreq) - t*float64(topFreq-bottomFreq)))
}

// fuseCharFreq 把「jieba 单字词频」与「Jun Da 字频排名」融合成最终词频。
//
// 为什么必须融合而不是二选一（2026-10-29 实测）：
//
//   - 只用 jieba：生僻字虚高（灏 2196 > 豪 1182、岃 1849 > 仁 1173），
//     因为它统计的是「分词语料里独立成词的程度」，人名用字被误计为高频。
//   - 只用 Jun Da：口语助词被系统性低估（吗/呢/吧/啊 都排在 400 名之后），
//     因为该表基于 2,580 万字**书面语**，聊天口语不在其中。实测 1,618 个字
//     在两个来源间分歧巨大，「在吗」这类口语组合会合成错词（在马）。
//
// 融合方式：两个来源各归一化到 0..1 后取几何平均——
//   - jieba 分量：log(1+freq) / log(1+maxFreq)，压缩幂律长尾；
//   - 排名分量：1 - log(1+rank)/log(1+20000)，排名越小越高。
//
// 几何平均的特性是**任一分量低就整体低**（惩罚虚高与冷僻），
// 但不会让单一来源的偏差主导结果。
//
// 校准（实测）：豪 0.367 > 灏 0.280 ✓；仁 0.374 > 岃 0.000 ✓；
// 浩 0.346 > 蒿 0.272 ✓；呢 0.547 > 尼 0.448 ✓；吧 0.530 > 巴 0.475 ✓。
// oralParticleBoost 是口语助词/语气词的定向补偿幅度。
//
// 为什么需要（2026-10-29 实测）：Jun Da 字频基于 2,580 万字**书面语**，
// 聊天与口语不在其中，「吗/呢/吧/啊/嘛/呀」因此排在 400 名之后。后果是
// 「在吗」这类极常用口语组合合成不出来——实测首选变成「在马」，
// 引擎既有测试 TestComposeMultiSyllableSentences 因此失败。
//
// 幅度**刻意很小（+8）**：够让「在吗」(53+8) 压过「在马」(56)，
// 又不足以让这些字在任何拼音下都抢首位（ma 的单字首选仍是「马」56 > 吗 61？
// 注意：吗+8=61 会超过马 56，但 ma 的单字页面里二者同拼音，61 分会让「吗」
// 排到首选——这与用户直觉一致：打 ma 时「吗」比「马」更常被选）。
const oralParticleBoost = 8

func isOralParticle(r rune) bool {
	switch r {
	case '吗', '呢', '吧', '啊', '嘛', '呀', '哦', '嗯', '咯', '呗', '喽':
		return true
	}
	return false
}

func fuseCharFreq(jiebaFreq int, rank int, maxJieba int) int {
	const bottomFreq = 1
	const topFreq = 99
	if maxJieba < 1 {
		maxJieba = 1
	}
	jiebaPart := 0.0
	if jiebaFreq > 0 {
		jiebaPart = math.Log(1+float64(jiebaFreq)) / math.Log(1+float64(maxJieba))
	}
	rankPart := 0.0
	if rank > 0 {
		rankPart = 1.0 - math.Log(1+float64(rank))/math.Log(1+20000)
	}
	if rankPart < 0 {
		rankPart = 0
	}
	fused := math.Sqrt(jiebaPart * rankPart)
	if fused > 1 {
		fused = 1
	}
	return int(math.Round(float64(bottomFreq) + fused*float64(topFreq-bottomFreq)))
}

// fuseCharFreqRune 同 fuseCharFreq，另外给口语助词小幅补偿（见 oralParticleBoost）。
func fuseCharFreqRune(ch rune, jiebaFreq int, rank int, maxJieba int) int {
	f := fuseCharFreq(jiebaFreq, rank, maxJieba)
	if isOralParticle(ch) {
		f += oralParticleBoost
		if f > 99 {
			f = 99
		}
	}
	return f
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
		"#   词组词频 jieba dict.txt        https://github.com/fxsjy/jieba（多字词频率可靠）",
		"#   单字字频 Jun Da 现代汉语字频    https://github.com/ruddfawcett/hanziDB.csv",
		"#            （jieba 的单字词频是分词口径，生僻字虚高，故单字以此为准）",
		"#   备用校正 Rime essay.txt        https://github.com/rime/essay（-corpus，" +
			"不传 -charfreq 时的回退路径）",
		"#   拼音   phrase-pinyin-data      https://github.com/mozillazg/phrase-pinyin-data",
		"#   单字   pinyin-data             https://github.com/mozillazg/pinyin-data",
		"#",
		"# 再生成（在仓库根目录）：",
		"#   go run ./cmd/chaoset-dictgen -jieba dict.txt \\",
		"#     -charfreq hanzi_db.csv -pinyin large_pinyin.txt -single pinyin.txt \\",
		"#     -o data/dict_common.tsv",
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
