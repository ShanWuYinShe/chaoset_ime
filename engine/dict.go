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
	if maxResults <= 0 {
		maxResults = 50
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
	if len(exactMatches) > maxResults {
		exactMatches = exactMatches[:maxResults]
	}

	// 衍生偏长词（前缀匹配）：采用逐层探索（最多向下深入 2 层，即多 1~2 个音节），
	// 避免在宽泛节点下触发全树无上限递归与万级对象巨型排序。
	var prefixMatches []*Entry
	prefixSeen := make(map[int]bool)
	levelNodes := curr
	maxPrefixScan := maxResults * 4
	for depth := 0; depth < 2 && len(levelNodes) > 0 && len(prefixMatches) < maxPrefixScan; depth++ {
		var nextLevel []*dictNode
		for _, node := range levelNodes {
			if node.children == nil {
				continue
			}
			for _, child := range node.children {
				nextLevel = append(nextLevel, child)
				for _, id := range child.ids {
					if !prefixSeen[id] {
						prefixSeen[id] = true
						prefixMatches = append(prefixMatches, d.entries[id])
					}
				}
			}
		}
		levelNodes = nextLevel
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

// LoadDictFiles 依次解析多个词库文件并合并进同一 Dictionary
// （daemon 用它把种子词库与热词词库叠加加载）；重复词条取较大词频。
// 各文件沿用严格校验：任何一行非法即报错。
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

// parseInto 解析词库进 d。strict 模式（种子/主词库）逐行强校验，
// 遇到脏数据即报错；宽容模式（热词导入）跳过无效行并计数，
// 缺省词频补 20、词频压缩到 1..99（用户学习权重远高于静态词频，
// 压缩不影响「越用越顺手」的调频效果）。
func parseInto(d *Dictionary, r io.Reader, strict bool) (skipped int, err error) {
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
				return skipped, fmt.Errorf("词库第 %d 行格式错误：应为「词\\t拼音\\t词频」", lineNo)
			}
			skipped++
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
				return skipped, fmt.Errorf("词库第 %d 行格式错误：应为「词\\t拼音\\t词频」", lineNo)
			}
			if word == "" || len(sylls) == 0 {
				return skipped, fmt.Errorf("词库第 %d 行词或拼音为空", lineNo)
			}
			if freq <= 0 {
				return skipped, fmt.Errorf("词库第 %d 行词频不是正整数", lineNo)
			}
			for _, s := range sylls {
				if !IsSyllable(s) {
					return skipped, fmt.Errorf("词库第 %d 行拼音 %q 不是合法音节", lineNo, s)
				}
			}
		} else {
			// 宽容模式（热词导入）：先做拼音归一化（去声调/ü→v），
			// 再补默认词频、压缩到 1..99（用户学习权重远高于静态词频）。
			sylls = normalizePinyin(parts[1])
			if freq <= 0 {
				freq = 20
			}
			if freq > 99 {
				freq = 99
			}
			if word == "" || sylls == nil {
				skipped++
				continue
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
		return skipped, fmt.Errorf("读取词库失败: %w", err)
	}
	return skipped, nil
}

// WriteTSV 把词条导出为 TSV（词频降序、同频按词排序），
// 供 chaoset-import 生成合并后的词库文件。
func WriteTSV(w io.Writer, entries []*Entry) error {
	sorted := append([]*Entry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Freq != b.Freq {
			return a.Freq > b.Freq
		}
		return a.Word < b.Word
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
