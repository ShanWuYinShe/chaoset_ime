package engine

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Entry 是词典中的一条记录：一个词、其标准拼音切分与基础词频。
type Entry struct {
	Word      string
	Syllables []string
	Freq      int
}

type dictNode struct {
	children map[string]*dictNode
	ids      []int
}

// Dictionary 以音节为边的 Trie：每个节点代表一段音节序列，
// 节点上挂着以该序列开头的词条。查询支持「完整音节序列」与
// 「音节序列 + 末尾残缺音节前缀」两类（对应击键打到一半的场景）。
// 种子规模下用 map Trie；词条量到十万级再换紧凑结构（双数组 Trie）。
type Dictionary struct {
	root    *dictNode
	entries []*Entry
	seen    map[string]int // word \x00 syllables -> entry 下标，跨文件去重
}

func NewDictionary() *Dictionary {
	return &Dictionary{root: &dictNode{}, seen: make(map[string]int)}
}

func (d *Dictionary) add(word string, syllables []string, freq int) {
	id := len(d.entries)
	d.entries = append(d.entries, &Entry{
		Word:      word,
		Syllables: append([]string(nil), syllables...),
		Freq:      freq,
	})
	node := d.root
	for _, s := range syllables {
		if node.children == nil {
			node.children = map[string]*dictNode{}
		}
		next := node.children[s]
		if next == nil {
			next = &dictNode{}
			node.children[s] = next
		}
		node = next
	}
	node.ids = append(node.ids, id)
}

// walk 沿音节序列走到目标节点；序列不存在返回 nil。
func (d *Dictionary) walk(syllables []string) *dictNode {
	node := d.root
	for _, s := range syllables {
		if node.children == nil {
			return nil
		}
		node = node.children[s]
		if node == nil {
			return nil
		}
	}
	return node
}

// Query 按「完整音节序列」查询，返回该节点及其子树上的所有词条
// （即以该序列开头的词，长短皆含）。
func (d *Dictionary) Query(syllables []string) []*Entry {
	node := d.walk(syllables)
	if node == nil {
		return nil
	}
	return d.collect(node)
}

// QueryPrefix 在走完 syllables 之后，对所有以 frag 为前缀的下一音节做子树收集。
func (d *Dictionary) QueryPrefix(syllables []string, frag string) []*Entry {
	node := d.walk(syllables)
	if node == nil || node.children == nil {
		return nil
	}
	var out []*Entry
	for s, child := range node.children {
		if strings.HasPrefix(s, frag) {
			out = append(out, d.collect(child)...)
		}
	}
	return out
}

// SyllableContinuation 描述一次「末音节续接」命中的词条：
// Remain 是该词末音节在用户已敲部分之后还需要补敲的字符，
// 补全越短说明用户越可能正想打这个词。
type SyllableContinuation struct {
	Entry  *Entry
	Remain string
}

// QueryContinuation 处理「末段音节打了一半」的场景：tokens 是前面已经
// 完整/简拼的音节约束，frag 是末段残缺串。命中条件是「tokens + 某个以
// frag 为前缀的完整音节」构成的词条前缀。
//
// 与 QueryTokens 的差别：QueryTokens 只能整段吃掉 frag 作为若干音节前缀
// （于是 zhonggu 被切成 zhong+g+u 这种碎片化路径，命中「中古」而不是「中国」），
// 本方法把 frag 视为**一个尚未打完的音节**去续接，正是输入法预测的核心形态。
func (d *Dictionary) QueryContinuation(tokens []Token, frag string, maxResults int) []SyllableContinuation {
	if frag == "" || len(tokens) == 0 {
		return nil
	}
	if maxResults <= 0 {
		maxResults = 50
	}
	nodes := d.walkTokens(tokens)
	if len(nodes) == 0 {
		return nil
	}
	var out []SyllableContinuation
	seen := make(map[int]bool)
	// 沿「以 frag 为前缀的下一音节」下探两层，覆盖 中国 / 中国人民 这类词
	// 以及 中国人 这类多音节衍生词；更长的衍生词由通用前缀查询兜底。
	var walk func(node *dictNode, depth int, remain string)
	walk = func(node *dictNode, depth int, remain string) {
		if node.children == nil || depth > 2 {
			return
		}
		for syl, child := range node.children {
			if !strings.HasPrefix(syl, frag) {
				continue
			}
			r := syl[len(frag):]
			if depth == 0 {
				remain = r
			}
			for _, id := range child.ids {
				if !seen[id] {
					seen[id] = true
					out = append(out, SyllableContinuation{Entry: d.entries[id], Remain: remain})
				}
			}
			walk(child, depth+1, remain)
		}
	}
	for _, node := range nodes {
		walk(node, 0, "")
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		// 补全越短越可能：zhonggu → 中国(remain "o") 优先于 中关村(remain "an")
		if len(out[i].Remain) != len(out[j].Remain) {
			return len(out[i].Remain) < len(out[j].Remain)
		}
		if out[i].Entry.Freq != out[j].Entry.Freq {
			return out[i].Entry.Freq > out[j].Entry.Freq
		}
		return out[i].Entry.Word < out[j].Entry.Word
	})
	if len(out) > maxResults {
		out = out[:maxResults]
	}
	return out
}

// QueryExact 返回音节序列与 syllables 恰好完全一致的词条（不含子树衍生更长的词）。
func (d *Dictionary) QueryExact(syllables []string) []*Entry {
	node := d.walk(syllables)
	if node == nil || len(node.ids) == 0 {
		return nil
	}
	out := make([]*Entry, len(node.ids))
	for i, id := range node.ids {
		out[i] = d.entries[id]
	}
	return out
}

// Token 表示音节约束（用于全拼、简拼与混拼匹配）：
// IsExact=true 时必须完全匹配 Text 音节（如 "zhong"）；
// IsExact=false 时匹配以此为前缀的音节（如 "z" 匹配 z/zh 开头，"b" 匹配 b 开头）。
type Token struct {
	Text    string
	IsExact bool
}

func matchSyllable(s string, tok Token) bool {
	if tok.IsExact {
		return s == tok.Text
	}
	if strings.HasPrefix(s, tok.Text) {
		return true
	}
	if tok.Text == "z" && strings.HasPrefix(s, "zh") {
		return true
	}
	if tok.Text == "c" && strings.HasPrefix(s, "ch") {
		return true
	}
	if tok.Text == "s" && strings.HasPrefix(s, "sh") {
		return true
	}
	return false
}

func (d *Dictionary) walkTokens(tokens []Token) []*dictNode {
	if len(tokens) == 0 || d.root == nil {
		return nil
	}
	curr := []*dictNode{d.root}
	for _, tok := range tokens {
		var next []*dictNode
		if tok.IsExact {
			for _, node := range curr {
				if node.children == nil {
					continue
				}
				if child := node.children[tok.Text]; child != nil {
					next = append(next, child)
				}
			}
		} else {
			for _, node := range curr {
				if node.children == nil {
					continue
				}
				for s, child := range node.children {
					if matchSyllable(s, tok) {
						next = append(next, child)
					}
				}
			}
		}
		if len(next) == 0 {
			return nil
		}
		curr = next
	}
	return curr
}

// QueryTokensExact 返回音节数恰好等于 len(tokens) 的完全匹配词条（按词频降序，最多 maxResults 条）。
// 不遍历子树，专供整句规划（DP）与逐级回退等高频路径极速调用。
func (d *Dictionary) QueryTokensExact(tokens []Token, maxResults int) []*Entry {
	if maxResults <= 0 {
		maxResults = 50
	}
	curr := d.walkTokens(tokens)
	if len(curr) == 0 {
		return nil
	}
	var exactMatches []*Entry
	seen := make(map[int]bool)
	for _, node := range curr {
		for _, id := range node.ids {
			if !seen[id] {
				seen[id] = true
				exactMatches = append(exactMatches, d.entries[id])
			}
		}
	}
	sort.Slice(exactMatches, func(i, j int) bool {
		if exactMatches[i].Freq != exactMatches[j].Freq {
			return exactMatches[i].Freq > exactMatches[j].Freq
		}
		return exactMatches[i].Word < exactMatches[j].Word
	})
	if len(exactMatches) > maxResults {
		exactMatches = exactMatches[:maxResults]
	}
	return exactMatches
}

// QueryTokens 在 Trie 中根据音节约束序列匹配词条。
// 返回 exactMatches（音节数恰好等于 len(tokens)）与 prefixMatches（更长的衍生词条），
// 两组结果均已按词频降序排序，数量分别以 maxResults 为上限。
func (d *Dictionary) QueryTokens(tokens []Token, maxResults int) ([]*Entry, []*Entry) {
	return d.queryTokens(tokens, maxResults, maxResults)
}

// queryTokens 拆分 exact/prefix 上限：单音节全量召回同音字时 prefix 仍保持
// 原扫描量——衍生词组扫描与 maxResults 联动，一并放大只会引入大量低频
// 词组稀释单字页位并拖慢热路径（2026-10-28「示」字反馈修复）。
func (d *Dictionary) queryTokens(tokens []Token, maxResults, exactMax int) ([]*Entry, []*Entry) {
	if maxResults <= 0 {
		maxResults = 50
	}
	if exactMax <= 0 {
		exactMax = maxResults
	}
	curr := d.walkTokens(tokens)
	if len(curr) == 0 {
		return nil, nil
	}

	var exactMatches []*Entry
	seen := make(map[int]bool)
	for _, node := range curr {
		for _, id := range node.ids {
			if !seen[id] {
				seen[id] = true
				exactMatches = append(exactMatches, d.entries[id])
			}
		}
	}
	sort.Slice(exactMatches, func(i, j int) bool {
		if exactMatches[i].Freq != exactMatches[j].Freq {
			return exactMatches[i].Freq > exactMatches[j].Freq
		}
		return exactMatches[i].Word < exactMatches[j].Word
	})
	if len(exactMatches) > exactMax {
		exactMatches = exactMatches[:exactMax]
	}

	// 衍生偏长词（前缀匹配）：采用逐层探索（最多向下深入 2 层，即多 1~2 个音节），
	// 避免在宽泛节点下触发全树无上限递归与万级对象巨型排序。
	var prefixMatches []*Entry
	prefixSeen := make(map[int]bool)
	// prefix BFS 的第一层可能是「宽前缀」：zh 一个 token 就命中几十个音节
	// 节点，每个再向下展开一层，扫描与排序就是数千词条——这是简拼输入
	// （z / zh / y）单键延迟的主要来源（实测 1.2-1.8ms）。
	//
	// 优化：第一层节点按「节点内词条最高词频」排序后再展开，并限制每层
	// 展开的节点数。原先 map 遍历无序收集，有限名额给到的是随机词；
	// 现在优先给高频词，候选质量同步提升，长尾节点被剪枝。
	levelNodes := curr
	maxPrefixScan := maxResults * 4
	const maxExpandPerLevel = 12
	for depth := 0; depth < 2 && len(levelNodes) > 0 && len(prefixMatches) < maxPrefixScan; depth++ {
		type childFreq struct {
			node *dictNode
			freq int
		}
		var ranked []childFreq
		for _, node := range levelNodes {
			if node.children == nil {
				continue
			}
			for _, child := range node.children {
				top := 0
				for _, id := range child.ids {
					if d.entries[id].Freq > top {
						top = d.entries[id].Freq
					}
				}
				ranked = append(ranked, childFreq{child, top})
			}
			sort.Slice(ranked, func(i, j int) bool { return ranked[i].freq > ranked[j].freq })
			if len(ranked) > maxExpandPerLevel {
				ranked = ranked[:maxExpandPerLevel]
			}
			var nextLevel []*dictNode
			for _, cf := range ranked {
				nextLevel = append(nextLevel, cf.node)
				for _, id := range cf.node.ids {
					if !prefixSeen[id] {
						prefixSeen[id] = true
						prefixMatches = append(prefixMatches, d.entries[id])
					}
				}
			}
			levelNodes = nextLevel
		}
	}

	sort.Slice(prefixMatches, func(i, j int) bool {
		if prefixMatches[i].Freq != prefixMatches[j].Freq {
			return prefixMatches[i].Freq > prefixMatches[j].Freq
		}
		return len(prefixMatches[i].Word) < len(prefixMatches[j].Word)
	})
	if len(prefixMatches) > maxResults {
		prefixMatches = prefixMatches[:maxResults]
	}

	return exactMatches, prefixMatches
}

// Contains 判断主词典是否已存在「词 + 精确音节序列」一致的词条。
// 词组进化用它避免把主词典已有的词重复学一遍。
func (d *Dictionary) Contains(word string, syllables []string) bool {
	node := d.walk(syllables)
	if node == nil {
		return false
	}
	for _, id := range node.ids {
		if d.entries[id].Word == word {
			return true
		}
	}
	return false
}

// Entries 返回全部词条（供词库导出与合并工具使用）。
func (d *Dictionary) Entries() []*Entry {
	return append([]*Entry(nil), d.entries...)
}

// Len 返回词条总数。
func (d *Dictionary) Len() int { return len(d.entries) }

// collect 深度优先收集节点自身及子树上的词条。遍历顺序不确定，
// 调用方（Session）最终会按确定性规则排序。
func (d *Dictionary) collect(node *dictNode) []*Entry {
	var out []*Entry
	for _, id := range node.ids {
		out = append(out, d.entries[id])
	}
	for _, child := range node.children {
		out = append(out, d.collect(child)...)
	}
	return out
}

// ParseDict 解析 TSV 词库：每行「词 \t 拼音(空格分隔) \t 词频」，
// # 开头的行为注释。重复词条保留较大词频；非法音节直接报错，
// 防止脏数据静默进入词库。
func ParseDict(r io.Reader) (*Dictionary, error) {
	d := NewDictionary()
	if _, err := parseInto(d, r, true); err != nil {
		return nil, err
	}
	return d, nil
}

// LoadDictFiles 依次解析多个词库文件并合并进同一 Dictionary；重复词条取
// 较大词频。**严格**：任何一行非法即报错——用于内置词库（生成产物，正确性
// 由 CI 保证）与其它需要立刻暴露脏数据的场景。
func LoadDictFiles(paths []string) (*Dictionary, error) {
	d := NewDictionary()
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("打开词库 %s 失败: %w", p, err)
		}
		_, perr := parseInto(d, f, true)
		f.Close()
		if perr != nil {
			return nil, fmt.Errorf("解析词库 %s 失败: %w", p, perr)
		}
	}
	return d, nil
}

// LoadDictFilesLenient 宽松加载，供 daemon / ibus 引擎在**运行时**加载
// 用户自备的第三方词库。
//
// 严格与宽松的分工（2026-10-30 明确）：
//
//   - 内置词库的正确性由 CI 保证：生成阶段 cmd/chaoset-dictgen 严格校验，
//     引擎测试又严格解析 data/dict_common.tsv 与 data/dict_extra.tsv
//     （见 engine/data_contract_test.go）。所以运行时不需要靠严格模式去
//     「发现」内置词库的脏数据——那必须在构建期就失败；
//   - 运行时面对的是用户从网上下载或手写的词库：一行坏数据不能让人家整个
//     输入法起不来，因此跳过坏行并汇总报告。
//
// 具体语义：
//   - 文件打不开（路径写错、权限不足）仍返回错误——那是配置错误，静默少
//     加载一个词库只会让用户以为输入法坏了；
//   - 坏行（缺列、空词/空拼音、非法音节、词频非正）跳过并计数，样本说明
//     （含行号，最多 parseErrSamples 条）进 warnings；
//   - 拼音归一化（去声调、ü→v、连写自动切分）、缺省词频补 20、超过 99 压到
//     99——与 chaoset-import 导入热词同一套规则，手写词库可以直接用。
//
// warnings 为空表示所有行都合法；调用方应把非空 warnings 写日志。
func LoadDictFilesLenient(paths []string) (*Dictionary, []string, error) {
	d := NewDictionary()
	var warnings []string
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, nil, fmt.Errorf("打开词库 %s 失败: %w", p, err)
		}
		st, perr := parseInto(d, f, false)
		f.Close()
		if perr != nil {
			return nil, nil, fmt.Errorf("读取词库 %s 失败: %w", p, perr)
		}
		if st.skipped > 0 {
			warnings = append(warnings, fmt.Sprintf("词库 %s 跳过 %d 行无效数据（%s）",
				p, st.skipped, strings.Join(st.samples, "；")))
		}
	}
	return d, warnings, nil
}

// parseErrSamples 是宽松解析时写进警告的坏行样本上限：够定位问题，又不至于
// 让一条日志刷屏（第三方词库里坏行可能有上千条）。
const parseErrSamples = 3

// parseStats 汇总宽松解析的跳过情况；严格模式下 skipped 恒为 0（坏行会
// 变成返回的错误）。
type parseStats struct {
	skipped int
	samples []string // 前 parseErrSamples 条坏行说明（含行号）
}

func (s *parseStats) skip(lineNo int, reason string) {
	s.skipped++
	if len(s.samples) < parseErrSamples {
		s.samples = append(s.samples, fmt.Sprintf("第 %d 行%s", lineNo, reason))
	}
}

// parseInto 解析词库进 d。strict 模式（内置/主词库）逐行强校验，遇到脏数据
// 即报错；宽松模式（用户自备词库、热词导入）跳过无效行并计数，缺省词频补
// 20、词频压缩到 1..99（用户学习权重远高于静态词频，压缩不影响「越用越
// 顺手」的调频效果）。
func parseInto(d *Dictionary, r io.Reader, strict bool) (st parseStats, err error) {
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			if strict {
				return st, fmt.Errorf("词库第 %d 行格式错误：应为「词\t拼音\t词频」", lineNo)
			}
			st.skip(lineNo, "缺少制表符分隔（应为「词\t拼音\t词频」）")
			continue
		}
		word := strings.TrimSpace(parts[0])
		sylls := strings.Fields(parts[1])
		freq := 0
		if len(parts) >= 3 {
			if n, nerr := strconv.Atoi(strings.TrimSpace(parts[2])); nerr == nil {
				freq = n
			}
		}
		if strict {
			if len(parts) != 3 {
				return st, fmt.Errorf("词库第 %d 行格式错误：应为「词\t拼音\t词频」", lineNo)
			}
			if word == "" || len(sylls) == 0 {
				return st, fmt.Errorf("词库第 %d 行词或拼音为空", lineNo)
			}
			if freq <= 0 {
				return st, fmt.Errorf("词库第 %d 行词频不是正整数", lineNo)
			}
			for _, s := range sylls {
				if !IsSyllable(s) {
					return st, fmt.Errorf("词库第 %d 行拼音 %q 不是合法音节", lineNo, s)
				}
			}
		} else {
			// 宽松模式分两步，顺序很重要：
			//
			//  1. 先按严格规则校验。通过则**原样保留音节**——这保证官方词库
			//     走宽松加载与走严格加载得到完全相同的词条（内置词库在
			//     daemon 里走的正是这里，而 CI 用严格加载断言，两条路径必须
			//     一致，见 TestLenientLoadMatchesStrictForBuiltinDict）；
			//  2. 校验不过才尝试归一化（去声调、ü→v、连写自动切分）——用户
			//     手写或从别处导出的词库常见「nǐ hǎo」「nihao」这类写法；
			//     归一化仍失败才跳过该行。
			canonical := word != "" && len(sylls) > 0
			if canonical {
				for _, s := range sylls {
					if !IsSyllable(s) {
						canonical = false
						break
					}
				}
			}
			if !canonical {
				sylls = normalizePinyin(parts[1])
				if word == "" || len(sylls) == 0 {
					st.skip(lineNo, "词或拼音无法解析为合法音节")
					continue
				}
			}
			if freq <= 0 {
				freq = 20
			}
			if freq > 99 {
				freq = 99
			}
		}
		key := word + "\x00" + strings.Join(sylls, " ")
		if prev, ok := d.seen[key]; ok {
			if freq > d.entries[prev].Freq {
				d.entries[prev].Freq = freq
			}
			continue
		}
		d.seen[key] = len(d.entries)
		d.add(word, sylls, freq)
	}
	if err := sc.Err(); err != nil {
		return st, fmt.Errorf("读取词库失败: %w", err)
	}
	return st, nil
}

// WriteTSV 把词条导出为 TSV（词频降序、同频按词、再按音节排序），
// 供 chaoset-import 生成合并后的词库文件。
func WriteTSV(w io.Writer, entries []*Entry) error {
	sorted := append([]*Entry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Freq != b.Freq {
			return a.Freq > b.Freq
		}
		if a.Word != b.Word {
			return a.Word < b.Word
		}
		// 同词多拼音（多音字）必须再按音节排定，否则比较器不是全序：
		// 「着 zhao」与「着 zhe」同频同词，compare 恒为 false，两条记录的
		// 相对顺序取决于 sort 内部实现与输入顺序——同样的输入从不同路径
		// 进来（CLI 脚本 vs GUI --selftest）会产出不同的 dict.tsv，
		// 等价性测试 B 段因此失败（2026-10-29 多音字全读音引入后暴露）。
		// dictgen 侧的排序早已含这一级（见 cmd/chaoset-dictgen main.go），
		// 这里是同一规则的补齐。
		return strings.Join(a.Syllables, " ") < strings.Join(b.Syllables, " ")
	})
	bw := bufio.NewWriter(w)
	if _, err := fmt.Fprintln(bw, "# chaoset 词库（由 chaoset-import 生成，词\\t拼音\\t词频）"); err != nil {
		return err
	}
	for _, e := range sorted {
		if _, err := fmt.Fprintf(bw, "%s\t%s\t%d\n", e.Word, strings.Join(e.Syllables, " "), e.Freq); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// QueryWordPrefix 按**词字面**前缀查词条（与音节前缀的 QueryPrefix 互补）。
// 联想续接按上屏词的字走：同音不同字（测试/策士）的续接词组应当不同
// （2026-10-28 用户反馈「联想不能只靠拼音，要跟字对应起来」）。
// 词库结构是音节 Trie，字面查询只能线性扫描全部词条——14 万条约 1-2ms，
// 联想只在上屏后调用一次（非按键热路径），可接受。
// 返回按词频降序、最多 maxResults 条；不含前缀自身（上屏词不重复出现）。
func (d *Dictionary) QueryWordPrefix(prefix string, maxResults int) []*Entry {
	if prefix == "" {
		return nil
	}
	if maxResults <= 0 {
		maxResults = 50
	}
	var out []*Entry
	for _, e := range d.entries {
		if len(e.Word) > len(prefix) && strings.HasPrefix(e.Word, prefix) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Freq != out[j].Freq {
			return out[i].Freq > out[j].Freq
		}
		return out[i].Word < out[j].Word
	})
	if len(out) > maxResults {
		out = out[:maxResults]
	}
	return out
}
