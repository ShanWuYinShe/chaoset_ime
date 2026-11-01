// chaoset-dictgen 从开放数据生成内置常用词库（data/dict_common.tsv）。
//
// 词表（「常用性门槛」流水线，2026-11-01 词库源调研后重构）：
//
//	《现代汉语常用词表》 ∪ HSK 3.0 词表 ∪ 所选词频源的高频用法 top N
//	∪ data/dict_hot.tsv（现代/热词，随版本维护） ∪ data/dict_extra.tsv（口语补充）
//	∩ phrase-pinyin-data 拼音链路。
//	词表里不在 phrase-pinyin-data 的词**不再整词丢弃**：按
//	「词内已知词组拼音 → HanLP 拼音词典 → 大词表字读音统计 → pinyin-data 首读音」
//	逐级补拼音，并把补出来的词输出到人工校对清单（-review-out）。
//
// 词频（两种可选，等最终数据许可决策后二选一）：
//
//	-freq wf    wordfreq zh 词频（代码 Apache-2.0；**数据 CC BY-SA 4.0**，需署名）
//	-freq hanlp HanLP CoreNatureDictionary 词频（Apache-2.0，full+mini 取 max）
//	词频统一压缩到 1..99；数据只有 1..99 是有意为之：用户调频一次上屏就 +500，
//	静态词频只是基础分（见 README「内置词库与再生成」）。
//
// 单字：通用规范汉字表 8105 字（一级/二级/三级）∩ Jun Da 现代汉语字频排名。
//
//	Jun Da 字表原作者声明仅限研究/教学、商业使用需书面许可（见 README 许可风险）；
//	换用 Unihan kHanyuPinlu/kFrequency 后可平滑替换 -charfreq 格式。
//
// 输出：确定性排序（词频降序、同频按词、再按音节），同一输入重复运行逐字节一致。
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
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"chaoset.ime/engine"
)

const (
	maxWordLen = 6
	// manualFreqScale 是手工词表权重的「同量纲参考上限」：手工词表里的权重
	// 按 ln(w)/ln(1e6) 压缩到 1..99，使人工补录词落在常用词中高区间
	// （微信/支付宝约 55-60 分），不会盖过 的/是/我们 这种核心高频词。
	manualFreqScale = 1e6
	// oralParticleBoost：口语助词/语气词在书面语字频里被低估的定向补偿。
	oralParticleBoost = 8
	// zipfMin/zipfSpan：wordfreq zipf 值约 1..8，映射到 0..1 归一化区间。
	zipfMin  = 1.0
	zipfSpan = 6.5
)

// options 是生成流程的全部输入。
type options struct {
	freqMode string

	normPath     string
	hskPath      string
	wordfreqPath string
	hanlpPath    string
	hanlpMini    string
	thuclDir     string

	pinyinPath      string
	singlePath      string
	charfreqPath    string
	hanlpPinyinPath string

	gsc1 string
	gsc2 string
	gsc3 string

	extraPath string
	hotPath   string
	reviewOut string
	outPath   string

	highfreqTop  int
	highfreqConf float64
	readingFloor float64
	freqGamma    float64
}

// entry 是待写入的一条词库记录。
type entry struct {
	word  string
	sylls []string
	freq  int
}

// manualRow 是手工补充词表（dict_extra / dict_hot）的一行。
type manualRow struct {
	word   string
	sylls  []string
	weight int
}

// pinyinMethod 记录一个多字词的拼音来源，用于人工校对清单。
type pinyinMethod string

const (
	methodPhrase   pinyinMethod = "phrase"
	methodManual   pinyinMethod = "manual"
	methodSegment  pinyinMethod = "segment"
	methodHanLP    pinyinMethod = "hanlp"
	methodCharStat pinyinMethod = "charstat"
	methodFirst    pinyinMethod = "first"
)

func main() {
	var o options
	flag.StringVar(&o.freqMode, "freq", "wf", "词频源模式：wf（wordfreq，数据 CC BY-SA 4.0）或 hanlp（Apache-2.0）")
	flag.StringVar(&o.normPath, "norm", "", "《现代汉语常用词表》TSV 路径（每行 词\\t拼音\\t排名；至少首列为词）")
	flag.StringVar(&o.hskPath, "hsk", "", "HSK 3.0 词表路径（每行 编号 词，或每行一个词）")
	flag.StringVar(&o.wordfreqPath, "wordfreq", "", "wordfreq 中文词频 TSV（词\\t zipf 值；-freq wf 必填）")
	flag.StringVar(&o.hanlpPath, "hanlp", "", "HanLP CoreNatureDictionary.txt（-freq hanlp 必填）")
	flag.StringVar(&o.hanlpMini, "hanlpmini", "", "HanLP CoreNatureDictionary.mini.txt（可选，与 full 取 max）")
	flag.StringVar(&o.thuclDir, "thucl", "", "THUOCL 领域词表目录（可选；主词频源没有的词用它兜底）")
	flag.StringVar(&o.pinyinPath, "pinyin", "", "phrase-pinyin-data large_pinyin.txt（必填）")
	flag.StringVar(&o.singlePath, "single", "", "pinyin-data pinyin.txt（必填）")
	flag.StringVar(&o.charfreqPath, "charfreq", "", "Jun Da 现代汉语字频 CSV（必填）")
	flag.StringVar(&o.hanlpPinyinPath, "hanlppinyin", "", "HanLP 拼音词典 pinyin.txt（可选，补拼音/校对用）")
	flag.StringVar(&o.gsc1, "gsc1", "", "通用规范汉字表一级字表 3500（必填）")
	flag.StringVar(&o.gsc2, "gsc2", "", "通用规范汉字表二级字表 3000（可选，建议提供）")
	flag.StringVar(&o.gsc3, "gsc3", "", "通用规范汉字表三级字表 1605（可选，建议提供）")
	flag.StringVar(&o.extraPath, "extra", "", "手工口语补充词表 data/dict_extra.tsv（必填）")
	flag.StringVar(&o.hotPath, "hot", "", "现代/热词补充词表 data/dict_hot.tsv（必填）")
	flag.StringVar(&o.reviewOut, "review-out", "", "补拼音人工校对清单输出路径（可选）")
	flag.StringVar(&o.outPath, "o", "", "输出 TSV 路径（必填）")
	flag.IntVar(&o.highfreqTop, "highfreqtop", 20000, "词频源里按词频取前 N 个词进入词表（高频用法）")
	flag.Float64Var(&o.highfreqConf, "highfreqconf", 0.9, "高频用法词只用字读音统计补拼音时的最低置信度（低于此值不收录）")
	flag.Float64Var(&o.freqGamma, "freqgamma", 1.15, "词频归一化后的幂次（>1 拉开高频差距；默认 1.15 由固定词表扫描定型，见 README）")
	flag.Float64Var(&o.readingFloor, "readingfloor", 0.3, "单字多音字读音占比折算的系数下限（share=1 时仍为满词频；0.3 由固定词表扫描定型，极罕见读音保留 30% 基础词频）")
	flag.Parse()
	if err := validateOptions(o); err != nil {
		fmt.Fprintln(os.Stderr, "chaoset-dictgen:", err)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "chaoset-dictgen:", err)
		os.Exit(1)
	}
}

func validateOptions(o options) error {
	if o.outPath == "" {
		return fmt.Errorf("-o 必填")
	}
	if o.freqMode != "wf" && o.freqMode != "hanlp" {
		return fmt.Errorf("-freq 只支持 wf 或 hanlp，实际 %q", o.freqMode)
	}
	if o.pinyinPath == "" || o.singlePath == "" || o.charfreqPath == "" {
		return fmt.Errorf("-pinyin / -single / -charfreq 必填")
	}
	if o.normPath == "" || o.hskPath == "" {
		return fmt.Errorf("-norm / -hsk 必填（常用性门槛词表）")
	}
	if o.extraPath == "" || o.hotPath == "" {
		return fmt.Errorf("-extra / -hot 必填（手工补充词表）")
	}
	if o.gsc1 == "" {
		return fmt.Errorf("-gsc1 必填（通用规范汉字表一级字表）")
	}
	if o.freqMode == "wf" && o.wordfreqPath == "" {
		return fmt.Errorf("-freq wf 需要 -wordfreq")
	}
	if o.freqMode == "hanlp" && o.hanlpPath == "" {
		return fmt.Errorf("-freq hanlp 需要 -hanlp")
	}
	if o.freqGamma <= 0 {
		return fmt.Errorf("-freqgamma 必须为正数")
	}
	if o.highfreqTop < 0 {
		return fmt.Errorf("-highfreqtop 不能为负")
	}
	if o.highfreqConf < 0 || o.highfreqConf > 1 {
		return fmt.Errorf("-highfreqconf 必须在 0..1 之间")
	}
	if o.readingFloor < 0 || o.readingFloor > 1 {
		return fmt.Errorf("-readingfloor 必须在 0..1 之间")
	}
	return nil
}

func run(o options) error {
	phrase, charStats, err := parsePhrasePinyin(o.pinyinPath)
	if err != nil {
		return err
	}
	log.Printf("词语拼音表：%d 个词", len(phrase))

	single, err := parseSinglePinyin(o.singlePath)
	if err != nil {
		return err
	}
	charRank, rankTotal, err := parseCharFrequency(o.charfreqPath)
	if err != nil {
		return err
	}
	log.Printf("单字读音：%d 个字；字频表：%d 个字", len(single), len(charRank))

	gscChars, err := parseGSCChars(o.gsc1, o.gsc2, o.gsc3)
	if err != nil {
		return err
	}
	log.Printf("通用规范汉字表：%d 个字", len(gscChars))

	var (
		mainFreq map[string]float64
		mainMax  float64
		highfreq []string
	)
	switch o.freqMode {
	case "wf":
		wf, maxZipf, err := parseWordfreq(o.wordfreqPath)
		if err != nil {
			return err
		}
		mainFreq = make(map[string]float64, len(wf))
		for w, z := range wf {
			mainFreq[w] = z
		}
		mainMax = maxZipf
		highfreq = topWordsByFreq(mainFreq, o.highfreqTop)
		log.Printf("词频源 wordfreq：%d 个词，高频用法取前 %d", len(wf), len(highfreq))
	case "hanlp":
		han, maxFreq, err := parseHanLPFreq(o.hanlpPath, o.hanlpMini)
		if err != nil {
			return err
		}
		mainFreq = make(map[string]float64, len(han))
		for w, f := range han {
			mainFreq[w] = float64(f)
		}
		mainMax = float64(maxFreq)
		highfreq = topWordsByFreq(mainFreq, o.highfreqTop)
		log.Printf("词频源 HanLP：%d 个词，高频用法取前 %d", len(han), len(highfreq))
	}

	// 词表真实性校验集合：高频用法词必须能在这里找到才算「真词」。
	// - hanlp 模式：HanLP 词典本身就是词频源，全部词天然可信；
	// - wf 模式：wordfreq 的 top 词表里混有大量分词噪声（这是/这我/一给 等），
	//   必须用 -hanlp/-hanlpmini（Apache-2.0 词表）交叉验证；
	// - THUOCL（词表）与 phrase-pinyin-data 在 filterKnownWords 里一并做校验。
	validDict := make(map[string]bool, 160000)
	if o.freqMode == "hanlp" {
		for w := range mainFreq {
			validDict[w] = true
		}
	}
	if o.freqMode == "wf" && o.hanlpPath != "" {
		han, _, verr := parseHanLPFreq(o.hanlpPath, o.hanlpMini)
		if verr != nil {
			return verr
		}
		for w := range han {
			validDict[w] = true
		}
		log.Printf("高频用词校验词典（HanLP）：%d 个词", len(validDict))
	}

	var thuocl map[string]int
	var thuoclMax int
	if o.thuclDir != "" {
		thuocl, thuoclMax, err = parseTHUOCL(o.thuclDir)
		if err != nil {
			return err
		}
		log.Printf("THUOCL：%d 个词", len(thuocl))
	}

	// 高频用法词表只保留「被词表源证明是词」的条目：wordfreq 的 top 词表里混有
	// 分词噪声（如「一给」「上还」），直接入库会造出并不存在的词。用
	// phrase-pinyin-data 与 THUOCL（两者都是词表）做真实性交叉验证。
	highfreq = filterKnownWords(highfreq, phrase, thuocl, validDict)
	log.Printf("高频用法词表（词表交叉验证后）：%d 个", len(highfreq))

	var hanlpPinyin map[string][]string
	if o.hanlpPinyinPath != "" {
		hanlpPinyin, err = parseHanLPPinyin(o.hanlpPinyinPath)
		if err != nil {
			return err
		}
		log.Printf("HanLP 拼音词典：%d 个词", len(hanlpPinyin))
	}

	hot, err := parseManualTable(o.hotPath)
	if err != nil {
		return err
	}
	extra, err := parseManualTable(o.extraPath)
	if err != nil {
		return err
	}
	log.Printf("手工补充词表：hot %d 条 / extra %d 条", len(hot), len(extra))
	manualAll := append(append([]manualRow(nil), hot...), extra...)

	normWords, err := parseWordListColumn(o.normPath)
	if err != nil {
		return err
	}
	hskWords, err := parseHSKWordList(o.hskPath)
	if err != nil {
		return err
	}
	allowed := make(map[string]string, len(normWords)+len(hskWords)+len(highfreq))
	for _, w := range normWords {
		allowed[w] = "norm"
	}
	for _, w := range hskWords {
		allowed[w] = "hsk"
	}
	for _, w := range highfreq {
		if _, ok := allowed[w]; !ok {
			allowed[w] = "highfreq"
		}
	}
	for _, r := range manualAll {
		allowed[r.word] = "manual"
	}

	simplifiedExists := make(map[string]bool, len(phrase))
	for w := range phrase {
		if cjkWordOK(w) {
			simplifiedExists[engine.ToSimplified(w)] = true
		}
	}

	manualPhrase := make(map[string][]string, len(manualAll))
	for _, r := range manualAll {
		manualPhrase[r.word] = r.sylls
	}
	resolver := &pinyinResolver{
		phrase:      phrase,
		manual:      manualPhrase,
		charStats:   charStats,
		hanlpPinyin: hanlpPinyin,
		single:      single,
	}

	multiWords := make([]string, 0, len(allowed))
	for w := range allowed {
		multiWords = append(multiWords, w)
	}
	sort.Strings(multiWords)

	entries := make([]entry, 0, len(multiWords)+len(gscChars))
	methodCount := map[pinyinMethod]int{}
	skipped := 0
	lowConfFiltered := 0
	var reviewItems []reviewItem
	for _, word := range multiWords {
		runes := []rune(word)
		if len(runes) < 2 || !cjkWordOK(word) {
			continue
		}
		origin := allowed[word]
		// 繁体变体剔除只对「高频用法」来源生效：规范常用词/HSK/手工词表本身
		// 都是简体词，且 engine.ToSimplified 是 OpenCC 简→繁表的反向映射，对
		// 「像/象」这类简繁同形字会误判（实测 好像→好象，导致常用词被误删）。
		if origin == "highfreq" {
			if sw := engine.ToSimplified(word); sw != word && simplifiedExists[sw] {
				continue
			}
		}
		res := resolver.resolve(word)
		if res.sylls == nil {
			skipped++
			continue
		}
		// 高频用法词表里的词若只能靠字读音统计拼、且多音字置信度低，很可能是
		// 分词噪声（如「一给」）——不收录以免造词；规范常用词/HSK/手工词表里的
		// 词不受此限制（它们本身就是常用词，必须可达）。
		if origin == "highfreq" && res.method == methodCharStat && res.conf < o.highfreqConf {
			lowConfFiltered++
			continue
		}
		if res.method != methodPhrase && res.method != methodManual {
			reviewItems = append(reviewItems, reviewItem{
				word: word, sylls: res.sylls, method: res.method,
				confidence: res.conf, alternatives: res.alts,
			})
		}
		methodCount[res.method]++
		freq, ok := freqOf(word, o, mainFreq, mainMax, thuocl, thuoclMax)
		if !ok {
			freq = fallbackFreq(len(runes))
			if freq == 0 {
				skipped++
				continue
			}
		}
		entries = append(entries, entry{word: word, sylls: res.sylls, freq: freq})
	}

	singleEntries := 0
	chars := make([]rune, 0, len(gscChars))
	for ch := range gscChars {
		chars = append(chars, ch)
	}
	sort.Slice(chars, func(i, j int) bool { return chars[i] < chars[j] })
	for _, ch := range chars {
		readings := single[ch]
		if len(readings) == 0 && hanlpPinyin != nil {
			// pinyin-data 对个别字（如「嗯」的 ń/ň 变体）没有能归一化成合法音节的
			// 读音，退回 HanLP 拼音词典；再没有就只能跳过，避免造非法音节。
			readings = hanlpPinyin[string(ch)]
		}
		if len(readings) == 0 {
			continue
		}
		base := charRankToFreq(charRank[ch], rankTotal)
		if isOralParticle(ch) {
			base += oralParticleBoost
			if base > 99 {
				base = 99
			}
		}
		// 多音字按「该读音在词条拼音里出现的比例」折算词频，而不是简单把次读音
		// 减半：单字总字频高不代表每个读音都常用。例：率 有三个读音
		// （lv/shuai/lve），只按字频会让 lve 与「略」同分抢首位；按读音占比
		// 折算后 lve 读音自然降低（数据来自 large_pinyin 词条的字↔音节对齐）。
		counts := charStats[ch]
		total := 0
		for _, n := range counts {
			total += n
		}
		seen := make(map[string]bool, len(readings))
		for i, syl := range readings {
			if seen[syl] {
				continue
			}
			seen[syl] = true
			share := 1.0
			if total > 0 {
				share = float64(counts[syl]) / float64(total)
			} else if i > 0 {
				share = 0
			}
			fr := int(math.Round(float64(base) * (o.readingFloor + (1-o.readingFloor)*share)))
			if fr < 1 {
				fr = 1
			}
			if fr > base {
				fr = base
			}
			entries = append(entries, entry{word: string(ch), sylls: []string{syl}, freq: fr})
			singleEntries++
		}
	}

	manualAdded := 0
	index := make(map[string]int, len(entries))
	for i := range entries {
		index[entries[i].word+"\x00"+strings.Join(entries[i].sylls, " ")] = i
	}
	for _, r := range manualAll {
		fr := manualFreq(r.weight)
		key := r.word + "\x00" + strings.Join(r.sylls, " ")
		if i, ok := index[key]; ok {
			if fr > entries[i].freq {
				entries[i].freq = fr
			}
			continue
		}
		entries = append(entries, entry{word: r.word, sylls: r.sylls, freq: fr})
		index[key] = len(entries) - 1
		manualAdded++
	}

	if o.reviewOut != "" {
		if err := writeReview(o.reviewOut, reviewItems, o.freqMode); err != nil {
			return err
		}
	}

	sortEntries(entries)
	if err := writeTSV(o.outPath, entries, o); err != nil {
		return err
	}

	multi := len(entries) - singleEntries
	fmt.Printf("模式=%s 写入 %s：%d 条（多字词 %d / 单字 %d；手工补充追加 %d；无法补拼音丢弃 %d；"+
		"低置信高频词过滤 %d；拼音来源 phrase=%d manual=%d segment=%d hanlp=%d charstat=%d first=%d；"+
		"人工校对清单 %d 条）\n",
		o.freqMode, o.outPath, len(entries), multi, singleEntries, manualAdded, skipped, lowConfFiltered,
		methodCount[methodPhrase], methodCount[methodManual], methodCount[methodSegment],
		methodCount[methodHanLP], methodCount[methodCharStat], methodCount[methodFirst], len(reviewItems))

	return nil
}

// resolveResult 是一次多字词拼音解析结果。
type resolveResult struct {
	sylls  []string
	method pinyinMethod
	conf   float64
	alts   string
}

// reviewItem 是人工校对清单的一行。
type reviewItem struct {
	word         string
	sylls        []string
	method       pinyinMethod
	confidence   float64
	alternatives string
}

// pinyinResolver 补拼音链路：词表拼音 → 词内已知词组 → HanLP 拼音词典 →
// 大词表字读音统计 → pinyin-data 首读音。
type pinyinResolver struct {
	phrase      map[string][]string
	manual      map[string][]string
	charStats   map[rune]map[string]int
	hanlpPinyin map[string][]string
	single      map[rune][]string
}

func (r *pinyinResolver) resolve(word string) resolveResult {
	// 手工补充词表的拼音是人工核对过的，优先级最高：它同时是 data_contract_test
	// 里「手工词条必须按原拼音合并进内置词库」断言的前提（phrase 表对个别词
	// 可能给出不同读音，会让手工词条对不上）。
	if r.manual != nil {
		if sylls, ok := r.manual[word]; ok && len(sylls) == len([]rune(word)) {
			return resolveResult{sylls: sylls, method: methodManual, conf: 1.0}
		}
	}
	if sylls, ok := r.phrase[word]; ok && len(sylls) == len([]rune(word)) {
		return resolveResult{sylls: sylls, method: methodPhrase, conf: 1.0}
	}
	if segSylls := r.segment(word); segSylls != nil {
		return resolveResult{sylls: segSylls, method: methodSegment, conf: 1.0}
	}
	if r.hanlpPinyin != nil {
		if sylls, ok := r.hanlpPinyin[word]; ok && len(sylls) == len([]rune(word)) {
			return resolveResult{sylls: sylls, method: methodHanLP, conf: 1.0}
		}
	}
	sylls, conf, alts, ok := r.composeFromCharStats(word)
	if ok {
		return resolveResult{sylls: sylls, method: methodCharStat, conf: conf, alts: alts}
	}
	sylls, ok = r.firstReadings(word)
	if ok {
		return resolveResult{sylls: sylls, method: methodFirst}
	}
	return resolveResult{}
}

// segment 用 phrase 中的多字词做「最少分段」DP 切分；无解返回 nil。
func (r *pinyinResolver) segment(word string) []string {
	runes := []rune(word)
	n := len(runes)
	if n < 2 {
		return nil
	}
	const inf = 1 << 30
	best := make([]int, n+1)
	choice := make([]int, n+1)
	for i := range best {
		best[i] = inf
	}
	best[n] = 0
	for i := n - 1; i >= 0; i-- {
		for l := maxWordLen; l >= 2; l-- {
			if i+l > n {
				continue
			}
			seg := string(runes[i : i+l])
			sylls, ok := r.phrase[seg]
			if !ok || len(sylls) != l {
				continue
			}
			if best[i+l] == inf {
				continue
			}
			if 1+best[i+l] < best[i] {
				best[i] = 1 + best[i+l]
				choice[i] = l
			}
		}
	}
	if best[0] == inf {
		return nil
	}
	var out []string
	for i := 0; i < n; {
		l := choice[i]
		out = append(out, r.phrase[string(runes[i:i+l])]...)
		i += l
	}
	return out
}

// composeFromCharStats 用词条内每个字在大词表里最常见的读音合成拼音。
func (r *pinyinResolver) composeFromCharStats(word string) ([]string, float64, string, bool) {
	runes := []rune(word)
	out := make([]string, 0, len(runes))
	minConf := 1.0
	var alts []string
	for _, ch := range runes {
		stats := r.charStats[ch]
		if len(stats) == 0 {
			readings := r.single[ch]
			if len(readings) == 0 {
				return nil, 0, "", false
			}
			out = append(out, readings[0])
			minConf = 0
			continue
		}
		total := 0
		for _, n := range stats {
			total += n
		}
		bestSyl, bestN := "", -1
		for syl, n := range stats {
			if n > bestN || (n == bestN && syl < bestSyl) {
				bestSyl, bestN = syl, n
			}
		}
		conf := float64(bestN) / float64(total)
		if conf < minConf {
			minConf = conf
		}
		out = append(out, bestSyl)
		if len(stats) > 1 {
			var cand []string
			for syl, n := range stats {
				cand = append(cand, fmt.Sprintf("%s(%d)", syl, n))
			}
			sort.Strings(cand)
			alts = append(alts, string(ch)+":"+strings.Join(cand, "/"))
		}
	}
	return out, minConf, strings.Join(alts, "; "), true
}

// firstReadings 用 pinyin-data 首读音兜底。
func (r *pinyinResolver) firstReadings(word string) ([]string, bool) {
	runes := []rune(word)
	out := make([]string, 0, len(runes))
	for _, ch := range runes {
		readings := r.single[ch]
		if len(readings) == 0 {
			return nil, false
		}
		out = append(out, readings[0])
	}
	return out, true
}

// freqOf 计算一个多字词的 1..99 词频：主词频源 → THUOCL 兜底 → 长度先验。
func freqOf(word string, o options, mainFreq map[string]float64, mainMax float64, thuocl map[string]int, thuoclMax int) (int, bool) {
	var norm float64
	has := false
	if f, ok := mainFreq[word]; ok && f > 0 {
		switch o.freqMode {
		case "wf":
			norm = (f - zipfMin) / zipfSpan
		case "hanlp":
			if mainMax > 1 {
				norm = math.Log1p(f) / math.Log1p(mainMax)
			} else {
				norm = 1
			}
		}
		has = true
	}
	if norm < 0 {
		norm = 0
	}
	if norm > 1 {
		norm = 1
	}
	norm = math.Pow(norm, o.freqGamma)
	freq := 1
	if has {
		freq = 1 + int(math.Round(98*norm))
	} else if thuocl != nil {
		if df, ok := thuocl[word]; ok && df > 0 && thuoclMax > 1 {
			tn := math.Log1p(float64(df)) / math.Log1p(float64(thuoclMax))
			freq = 1 + int(math.Round(98*math.Pow(tn, o.freqGamma)))
			has = true
		}
	}
	if !has {
		return 0, false
	}
	if freq < 1 {
		freq = 1
	}
	if freq > 99 {
		freq = 99
	}
	return freq, true
}

// fallbackFreq 无任何词频来源的词按词长给兜底分。
func fallbackFreq(n int) int {
	switch n {
	case 2:
		return 26
	case 3:
		return 14
	case 4:
		return 9
	case 5:
		return 6
	case 6:
		return 4
	}
	return 0
}

// manualFreq 手工词表权重 → 1..99（ln(w)/ln(1e6)，见 manualFreqScale 注释）。
func manualFreq(weight int) int {
	if weight <= 0 {
		return 1
	}
	f := 1 + int(math.Round(98*math.Log(float64(weight))/math.Log(manualFreqScale)))
	if f < 1 {
		f = 1
	}
	if f > 99 {
		f = 99
	}
	return f
}

// filterKnownWords 只保留被词表源证明是词的条目：phrase-pinyin-data /
// THUOCL / validDict（HanLP 词典）任意一处收录即视为真词。目的是把 wordfreq
// top 词表里的分词噪声（这是/这我/一给 等）挡在词库外。
func filterKnownWords(words []string, phrase map[string][]string, thuocl map[string]int, validDict map[string]bool) []string {
	if len(words) == 0 {
		return nil
	}
	out := make([]string, 0, len(words))
	for _, w := range words {
		if _, ok := phrase[w]; ok {
			out = append(out, w)
			continue
		}
		if thuocl != nil {
			if _, ok := thuocl[w]; ok {
				out = append(out, w)
				continue
			}
		}
		if validDict != nil && validDict[w] {
			out = append(out, w)
		}
	}
	return out
}

func topWordsByFreq(freq map[string]float64, n int) []string {
	if n <= 0 {
		return nil
	}
	type wf struct {
		word string
		val  float64
	}
	all := make([]wf, 0, len(freq))
	for w, f := range freq {
		runes := []rune(w)
		if len(runes) < 2 || !cjkWordOK(w) {
			continue
		}
		all = append(all, wf{word: w, val: f})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].val != all[j].val {
			return all[i].val > all[j].val
		}
		return all[i].word < all[j].word
	})
	if len(all) > n {
		all = all[:n]
	}
	out := make([]string, len(all))
	for i := range all {
		out[i] = all[i].word
	}
	return out
}

func sortEntries(entries []entry) {
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
}

// charRankToFreq 把字频排名映射为 1..99（排名 1 得 99，末尾得 1）。
func charRankToFreq(rank, total int) int {
	if rank <= 0 {
		return 1
	}
	if rank <= 1 {
		return 99
	}
	if total < 2 {
		total = 2
	}
	if rank >= total {
		return 1
	}
	t := math.Log(float64(rank)) / math.Log(float64(total))
	return int(math.Round(99 - t*98))
}

const oralParticleChars = "吗呢吧啊嘛呀哦嗯咯呗喽"

func isOralParticle(r rune) bool {
	return strings.ContainsRune(oralParticleChars, r)
}

func cjkWordOK(word string) bool {
	runes := []rune(word)
	if len(runes) == 0 || len(runes) > maxWordLen {
		return false
	}
	for _, r := range runes {
		switch {
		case r >= 0x3400 && r <= 0x4DBF:
		case r >= 0x4E00 && r <= 0x9FFF:
		case r >= 0xF900 && r <= 0xFAFF:
		case r == 0x3007:
		default:
			return false
		}
	}
	return true
}

// ---------- 输入文件解析 ----------

// parsePhrasePinyin 解析 large_pinyin.txt（词: pīn yīn）：返回
// 词→归一化音节（同词多行取最后一行，与旧生成器一致）与大词表字读音统计。
func parsePhrasePinyin(path string) (map[string][]string, map[rune]map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("打开词语拼音表 %s 失败: %w", path, err)
	}
	defer f.Close()
	phrase := make(map[string][]string, 420000)
	charStats := make(map[rune]map[string]int, 12000)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		word, py, ok := strings.Cut(line, ": ")
		if !ok || word == "" || py == "" {
			continue
		}
		if i := strings.IndexByte(py, '#'); i >= 0 {
			py = py[:i]
		}
		sylls := engine.NormalizePinyin(py)
		if len(sylls) != len([]rune(word)) {
			continue
		}
		valid := true
		for _, s := range sylls {
			if !engine.IsSyllable(s) {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		phrase[word] = sylls
		for i, ch := range []rune(word) {
			m := charStats[ch]
			if m == nil {
				m = map[string]int{}
				charStats[ch] = m
			}
			m[sylls[i]]++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("读取词语拼音表 %s 失败: %w", path, err)
	}
	if len(phrase) == 0 {
		return nil, nil, fmt.Errorf("词语拼音表 %s 没有可用条目", path)
	}
	return phrase, charStats, nil
}

// parseSinglePinyin 解析 pinyin-data pinyin.txt：每行「U+XXXX: pīn,yīn  # 字」。
func parseSinglePinyin(path string) (map[rune][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开单字拼音表 %s 失败: %w", path, err)
	}
	defer f.Close()
	out := make(map[rune][]string, 22000)
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
		if i := strings.IndexByte(rest, '#'); i >= 0 {
			rest = rest[:i]
		}
		cp, perr := strconv.ParseInt(code[2:], 16, 32)
		if perr != nil || cp <= 0 || cp > 0x10FFFF {
			continue
		}
		var readings []string
		seen := map[string]bool{}
		for _, raw := range strings.Split(rest, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			sylls := engine.NormalizePinyin(raw)
			if len(sylls) != 1 || !engine.IsSyllable(sylls[0]) || seen[sylls[0]] {
				continue
			}
			seen[sylls[0]] = true
			readings = append(readings, sylls[0])
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

// parseCharFrequency 解析 Jun Da 字频 CSV，返回「字 → 排名」与排名总数。
func parseCharFrequency(path string) (map[rune]int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("打开字频表 %s 失败: %w", path, err)
	}
	defer f.Close()
	out := make(map[rune]int, 10000)
	rc := csv.NewReader(f)
	rc.FieldsPerRecord = -1
	header, err := rc.Read()
	if err != nil {
		return nil, 0, fmt.Errorf("读取字频表表头失败: %w", err)
	}
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
		return nil, 0, fmt.Errorf("字频表 %s 缺少 frequency_rank/character 列", path)
	}
	for {
		rec, rerr := rc.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			continue
		}
		if rankCol >= len(rec) || charCol >= len(rec) {
			continue
		}
		rank, cerr := strconv.Atoi(strings.TrimSpace(rec[rankCol]))
		if cerr != nil || rank <= 0 {
			continue
		}
		runes := []rune(strings.TrimSpace(rec[charCol]))
		if len(runes) == 1 {
			out[runes[0]] = rank
		}
	}
	if len(out) == 0 {
		return nil, 0, fmt.Errorf("字频表 %s 没有有效条目", path)
	}
	return out, len(out), nil
}

// parseGSCChars 读取通用规范汉字表一级/二级/三级字表（每行一个字）。
func parseGSCChars(paths ...string) (map[rune]bool, error) {
	out := make(map[rune]bool, 8200)
	for _, path := range paths {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("打开通用规范汉字表 %s 失败: %w", path, err)
		}
		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			runes := []rune(line)
			if len(runes) == 1 {
				out[runes[0]] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("通用规范汉字表没有有效条目")
	}
	return out, nil
}

// parseWordListColumn 读取「每行首个 tab 列是词」的词表。
func parseWordListColumn(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开词表 %s 失败: %w", path, err)
	}
	defer f.Close()
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '\t'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out, sc.Err()
}

// parseHSKWordList 解析 HSK 3.0 词表：支持「编号 词」与「每行一个词」两种格式。
func parseHSKWordList(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 HSK 词表 %s 失败: %w", path, err)
	}
	defer f.Close()
	// 先判断文件是否「编号 词」格式：是则忽略非编号行（一级词汇表 这类标题行），
	// 否则按「每行一个词」处理（并过滤标题行）。
	var rawLines []string
	numbered := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rawLines = append(rawLines, line)
		if i := strings.IndexByte(line, ' '); i > 0 {
			if _, err := strconv.Atoi(line[:i]); err == nil {
				numbered++
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取 HSK 词表 %s 失败: %w", path, err)
	}
	var out []string
	seen := map[string]bool{}
	for _, line := range rawLines {
		if numbered > 0 {
			i := strings.IndexByte(line, ' ')
			if i <= 0 {
				continue
			}
			if _, err := strconv.Atoi(line[:i]); err != nil {
				continue
			}
			line = strings.TrimSpace(line[i+1:])
		} else if strings.Contains(line, "词汇表") || strings.Contains(line, "汉字表") {
			continue
		}
		if i := strings.Index(line, "｜"); i > 0 {
			line = line[:i]
		}
		if i := strings.IndexAny(line, "（("); i > 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if len([]rune(line)) < 2 || !cjkWordOK(line) || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out, sc.Err()
}

// parseWordfreq 解析 wordfreq 导出 TSV（词 tab zipf），返回词频与最大 zipf。
func parseWordfreq(path string) (map[string]float64, float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("打开 wordfreq 词频表 %s 失败: %w", path, err)
	}
	defer f.Close()
	out := make(map[string]float64, 330000)
	max := 0.0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		parts := strings.Split(strings.TrimSpace(sc.Text()), "\t")
		if len(parts) != 2 {
			continue
		}
		z, perr := strconv.ParseFloat(parts[1], 64)
		if perr != nil || z <= 0 {
			continue
		}
		out[parts[0]] = z
		if z > max {
			max = z
		}
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("读取 wordfreq 词频表 %s 失败: %w", path, err)
	}
	if len(out) == 0 {
		return nil, 0, fmt.Errorf("wordfreq 词频表 %s 没有有效条目", path)
	}
	return out, max, nil
}

// parseHanLPFreq 解析 HanLP CoreNatureDictionary；full 与 mini 都提供时按词取 max。
func parseHanLPFreq(path, miniPath string) (map[string]int, int, error) {
	out := make(map[string]int, 160000)
	if err := mergeHanLPFile(out, path); err != nil {
		return nil, 0, err
	}
	if miniPath != "" {
		if err := mergeHanLPFile(out, miniPath); err != nil {
			return nil, 0, err
		}
	}
	max := 0
	for _, f := range out {
		if f > max {
			max = f
		}
	}
	if len(out) == 0 || max <= 0 {
		return nil, 0, fmt.Errorf("HanLP 词频表没有有效条目")
	}
	return out, max, nil
}

func mergeHanLPFile(dst map[string]int, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开 HanLP 词频表 %s 失败: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		parts := strings.Split(strings.TrimSpace(sc.Text()), "\t")
		if len(parts) < 3 {
			continue
		}
		total := 0
		for i := 1; i+1 < len(parts); i += 2 {
			freq, perr := strconv.Atoi(parts[i+1])
			if perr != nil {
				break
			}
			total += freq
		}
		if total > 0 {
			dst[parts[0]] += total
		}
	}
	return sc.Err()
}

// parseTHUOCL 读取 THUOCL 目录下全部词表（词 tab DF），返回词→DF 与最大 DF。
func parseTHUOCL(dir string) (map[string]int, int, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("读取 THUOCL 目录 %s 失败: %w", dir, err)
	}
	out := make(map[string]int, 120000)
	for _, it := range items {
		if it.IsDir() || !strings.HasSuffix(it.Name(), ".txt") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, it.Name()))
		if rerr != nil {
			return nil, 0, rerr
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) != 2 {
				continue
			}
			freq, perr := strconv.Atoi(strings.TrimSpace(parts[1]))
			if perr != nil || freq <= 0 {
				continue
			}
			out[parts[0]] += freq
		}
	}
	max := 0
	for _, f := range out {
		if f > max {
			max = f
		}
	}
	if len(out) == 0 {
		return nil, 0, fmt.Errorf("THUOCL 目录 %s 没有有效条目", dir)
	}
	return out, max, nil
}

// parseHanLPPinyin 解析 HanLP 拼音词典（词=yi1,ding1,...），转成无调拼音。
func parseHanLPPinyin(path string) (map[string][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 HanLP 拼音词典 %s 失败: %w", path, err)
	}
	defer f.Close()
	out := make(map[string][]string, 32000)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		word, py, ok := strings.Cut(line, "=")
		if !ok || word == "" || py == "" {
			continue
		}
		var sylls []string
		valid := true
		for _, raw := range strings.Split(py, ",") {
			s := normalizeToneNumber(raw)
			if s == "" || !engine.IsSyllable(s) {
				valid = false
				break
			}
			sylls = append(sylls, s)
		}
		if !valid || len(sylls) != len([]rune(word)) {
			continue
		}
		out[word] = sylls
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取 HanLP 拼音词典 %s 失败: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("HanLP 拼音词典 %s 没有有效条目", path)
	}
	return out, nil
}

// normalizeToneNumber 把 HanLP 的 yi1 / lv4 / er5 这类带调号数字拼音转成无声调音节。
func normalizeToneNumber(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	last := s[len(s)-1]
	if last >= '1' && last <= '5' {
		s = s[:len(s)-1]
	}
	s = strings.ReplaceAll(s, "u:", "v")
	s = strings.ReplaceAll(s, "ü", "v")
	return s
}

// parseManualTable 读取手工补充词表（词 tab 拼音 tab 权重），逐条严格校验。
func parseManualTable(path string) ([]manualRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开补充词表 %s 失败: %w", path, err)
	}
	var out []manualRow
	seen := map[string]bool{}
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lineNo := i + 1
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("%s 第 %d 行应为 3 列（词/拼音/权重）: %q", path, lineNo, line)
		}
		word := strings.TrimSpace(parts[0])
		sylls := strings.Fields(parts[1])
		weight, werr := strconv.Atoi(strings.TrimSpace(parts[2]))
		if word == "" || len(sylls) == 0 {
			return nil, fmt.Errorf("%s 第 %d 行词或拼音为空", path, lineNo)
		}
		if !cjkWordOK(word) {
			return nil, fmt.Errorf("%s 第 %d 行不是纯 CJK 词（长度 1..%d）: %q", path, lineNo, maxWordLen, word)
		}
		if werr != nil || weight <= 0 {
			return nil, fmt.Errorf("%s 第 %d 行权重不是正整数: %q", path, lineNo, parts[2])
		}
		if len(sylls) != len([]rune(word)) {
			return nil, fmt.Errorf("%s 第 %d 行音节数与字数不等: %s / %v", path, lineNo, word, sylls)
		}
		for _, s := range sylls {
			if !engine.IsSyllable(s) {
				return nil, fmt.Errorf("%s 第 %d 行存在非法音节 %q", path, lineNo, s)
			}
		}
		key := word + "\x00" + strings.Join(sylls, " ")
		if seen[key] {
			return nil, fmt.Errorf("%s 第 %d 行重复词条: %s / %v", path, lineNo, word, sylls)
		}
		seen[key] = true
		out = append(out, manualRow{word: word, sylls: sylls, weight: weight})
	}
	return out, nil
}

// writeReview 输出补拼音人工校对清单（TSV，含不能自动确定的多音字候选）。
func writeReview(path string, items []reviewItem, mode string) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建校对清单 %s 失败: %w", path, err)
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	fmt.Fprintf(bw, "# chaoset-dictgen 补拼音人工校对清单（模式 %s）\n", mode)
	fmt.Fprintf(bw, "# 只有不在 phrase-pinyin-data 里的词会出现在这里；confidence 为字读音统计的\n")
	fmt.Fprintf(bw, "# 最低占比（1.0 = 词内词组拼音拼接或 HanLP 词典命中；<0.9 建议人工复核）。\n")
	fmt.Fprintf(bw, "# 格式：词 \t 采用拼音 \t 来源 \t confidence \t 多音字候选\n")
	sort.Slice(items, func(i, j int) bool {
		if items[i].word != items[j].word {
			return items[i].word < items[j].word
		}
		return strings.Join(items[i].sylls, " ") < strings.Join(items[j].sylls, " ")
	})
	for _, it := range items {
		fmt.Fprintf(bw, "%s\t%s\t%s\t%.2f\t%s\n", it.word, strings.Join(it.sylls, " "), it.method, it.confidence, it.alternatives)
	}
	return bw.Flush()
}

// writeTSV 写出三列 TSV；文件头按词频模式声明来源与许可，并附再生成命令。
func writeTSV(path string, entries []entry, o options) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 1<<16)

	header := []string{
		"# chaoset 内置常用词库（由 cmd/chaoset-dictgen 生成，禁止手工编辑）",
		"# 格式：词 \t 拼音（空格分隔，无声调，ü 以 v 输入） \t 词频（1..99）",
		"#",
		"# 词表（常用性门槛）：《现代汉语常用词表》∪ HSK 3.0 ∪ 高频用法 topN",
		"#   ∪ data/dict_hot.tsv（现代/热词） ∪ data/dict_extra.tsv（口语补充）；",
		"#   不在 phrase-pinyin-data 的词用「词内词组拼音 → HanLP 拼音词典 →",
		"#   大词表字读音统计 → pinyin-data 首读音」补拼音，不整词丢弃。",
		"# 单字：通用规范汉字表 8105 字（一级/二级/三级）∩ Jun Da 现代汉语字频排名。",
		"#",
		"# 拼音数据：phrase-pinyin-data large_pinyin.txt（MIT）+ pinyin-data pinyin.txt（MIT）",
		"#   + 通用规范汉字表（国家标准）+ HanLP 拼音词典（Apache-2.0，仅补缺用）",
		"#   注意：large_pinyin 由汉典 zdic + CC-CEDICT（CC BY-SA 4.0）等来源合并，",
		"#   词表/拼音的再分发 provenance 需在发布说明中保留说明。",
	}
	switch o.freqMode {
	case "wf":
		header = append(header,
			"#",
			"# 词频数据：wordfreq zh（代码 Apache-2.0；**数据 CC BY-SA 4.0**，"+
				"https://github.com/rspeer/wordfreq）",
			"#   + THUOCL（MIT，领域词兜底，https://github.com/THUNLP/THUOCL）",
			"#   本词库文件（数据部分）按 CC BY-SA 4.0 分发并署名 wordfreq/THUOCL；代码仍为 MIT。",
			"#   完整许可与署名见 data/NOTICE.md。",
		)
	case "hanlp":
		header = append(header,
			"#",
			"# 词频数据：HanLP CoreNatureDictionary（Apache-2.0，"+
				"https://github.com/hankcs/HanLP；产品说明中保留链接与协议）",
			"#   + THUOCL（MIT，领域词兜底，https://github.com/THUNLP/THUOCL）",
		)
	}
	header = append(header,
		"#",
		"# 单字字频源：Jun Da 现代汉语字频（https://github.com/ruddfawcett/hanziDB.csv）；",
		"#   原作者声明仅限研究/教学、商业使用需书面许可——发布前需替换为 Unihan",
		"#   kHanyuPinlu/kFrequency（Unicode License）或取得授权。",
		"#",
		"# 再生成（在仓库根目录，数据文件需自行下载；完整参数见 README「内置词库与再生成」）：",
		fmt.Sprintf("#   go run ./cmd/chaoset-dictgen -freq %s -norm changyongcibiao.txt -hsk hsk30_words.txt \\", o.freqMode),
		fmt.Sprintf("#     %s -thucl THUOCL_data \\", freqFlagHint(o.freqMode)),
		"#     -pinyin large_pinyin.txt -single pinyin.txt -charfreq hanzi_db.csv \\",
		"#     -hanlppinyin hanlp_pinyin.txt -gsc1 level-1.txt -gsc2 level-2.txt -gsc3 level-3.txt \\",
		"#     -hot data/dict_hot.tsv -extra data/dict_extra.tsv -o data/dict_common.tsv",
		"# 同一输入重复运行输出逐字节一致（已排序，不含时间戳）。",
	)
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
	return bw.Flush()
}

func freqFlagHint(mode string) string {
	if mode == "wf" {
		return "-wordfreq wordfreq_zh.tsv"
	}
	return "-hanlp CoreNatureDictionary.txt -hanlpmini CoreNatureDictionary.mini.txt"
}
