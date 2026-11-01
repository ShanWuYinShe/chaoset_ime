package engine

import (
	"sort"
	"strings"
)

// 错拼修正后的「按已有词切分」DP（2026-11-01 用户要求）：
//
//	用户输入长拼音时经常打错其中一两个字母（chagnshuru → changshuru，
//	tuzhogng → tuzhong）。旧逻辑只对修正后的整串做一次「词典整词」查询：
//	词库里没有「长输入」「图中」这种整词词条时（长+输入、图+中 都存在），
//	修正结果就完全出不来。
//
// 这里对修正串正常切分后的音节序列做 DP，把整串切成「已存在的词」序列，
// 拼出的词组作为错拼修正候选（rankTypoFix + typoFix 标记，选中即整串消费）。
//
// 边界（与用户此前的「不拼合造词」规则不冲突）：
//   - 只在**错拼修正路径**里调用（typoSuspicious / midSuspicious / tailSuspicious），
//     正常输入仍然不会把多个词拼成词组候选；
//   - 每一段都必须能在词典或用户词库里精确查到，不引入词库不存在的词；
//   - 整词本身在词库中存在的组合（如「你好」= 你+好 恰好是词条）直接跳过，
//     避免把整词候选加上分段、改变学习语义。
const (
	// typoDPWordBonus 是多字词每多一个音节的奖励：鼓励把长段当作一个已有的
	// 多字词（输入…），而不是拆成更碎的组合。取值刻意小于「一个普通词的词频
	// 量级」——纠错组合候选只能与其他候选同层竞争，不能靠奖励虚高把正常候选
	// （你好/我们）挤掉。
	typoDPWordBonus = 40
	// typoDPSegmentPenalty 是每多一个切分段的小惩罚：同等词频下分得越少越可信。
	typoDPSegmentPenalty = 25
	// typoDPSinglePenalty 是单字分段的额外惩罚：单字拼接的置信度低于成词。
	typoDPSinglePenalty = 15
	// typoDPTailWordBonus 是「末段是多字词」的奖励：中文词组通常是右重心的，
	// 以多字词收尾（长+输入）比以单字收尾（常数+如）更可能是用户意图。
	typoDPTailWordBonus = 40
	// typoDPTailSinglePenalty 对应地惩罚以单字收尾的切分。
	typoDPTailSinglePenalty = 20
	// typoDPMaxSyllables 限制参与 DP 的音节数：错拼场景输入不会太长，超长串
	// 组合数爆炸且价值低。
	typoDPMaxSyllables = 12
	// typoDPMaxStates 是每个位置保留的最优路径数（按累计得分截断）。
	typoDPMaxStates = 24
	// typoDPMaxSpan 是一个词最多占用的音节数，与词库生成器的 maxWordLen 对齐。
	typoDPMaxSpan = 6
)

// typoMaxPhraseQueries 是一次错拼修正里最多做多少次「分词 DP」。
//
// 修正串按价值排序（相邻互换/删除在前），真实手误的修正通常落在前十几条；
// 每次 DP 内部有段数/状态上限，且只在错拼路径执行，不增加正常输入的热路径成本。
const typoMaxPhraseQueries = 16

// typoPhrasePromoteMax 是错拼组合候选最多提升到精确层的个数。
// 组合候选没有语言模型，只能按「词频 × 修正类型置信度」排序；给太多名额会
// 稀释首屏（chagnshuru 会出几十个 X输入 变体）。其余候选仍保留在低层，可从
// 后页选到，保证想要的词不会因为名额被裁掉而完全不可达。
const typoPhrasePromoteMax = 8

// typoPhraseSpan 是 DP 中的一个「词段」：词典/用户词库里精确存在的一条词。
type typoPhraseSpan struct {
	word  string
	sylls []string
	freq  int
	segs  []string
}

// typoPhrasePath 是 DP 的部分切分结果。
type typoPhrasePath struct {
	words []string
	sylls []string
	score int
}

// typoFixPhraseBonus 判断某个修正串是否允许做「分词 DP」组合，并返回
// 该修正类型的置信度加权（分）。
//
// 只允许两类修正：相邻互换（chagn → chang，+50）与删除/粘键折叠
// （tuzhogng → tuzhong，字符数变少，+30）。替换/插入属于「单词语义已能覆盖」
// 的修正（p→o 的 nihao、漏敲字母的 zhongguo），在这些修正上做组合 DP 噪声
// 明显大于收益：会把「法票/我么/吃饭了」这类拼接顶到正常候选前面。
//
// 加权用来压住「删除合法音节得到的另一个修正」（chagnshuru 删 g 得到的
// chanshuru 会切出「阐述如」）：相邻互换是打字顺序手误里最常见的形态，
// 同为单次编辑时优先考虑它。
func typoFixPhraseBonus(src, fixed string) (bonus int, ok bool) {
	if fixed == "" || src == "" || fixed == src {
		return 0, false
	}
	if len(fixed) < len(src) {
		return 30, true // 删除或连击折叠
	}
	if len(fixed) != len(src) {
		return 0, false // 插入：交给单词语义
	}
	diff := 0
	first := -1
	for i := 0; i < len(src); i++ {
		if src[i] != fixed[i] {
			diff++
			if first < 0 {
				first = i
			}
		}
	}
	if diff != 2 || first < 0 || first+1 >= len(src) {
		return 0, false
	}
	if src[first] == fixed[first+1] && src[first+1] == fixed[first] {
		return 50, true
	}
	return 0, false
}

// typoPhraseEntries 把（已修正的）完整音节序列切成已有词序列，返回词组候选。
// tokens 必须是纯完整音节（typoCorrections 产出的修正串都满足）。
func (s *Session) typoPhraseEntries(tokens []Token, maxResults int) []*Entry {
	n := len(tokens)
	if n < 2 || n > typoDPMaxSyllables || s.dict == nil {
		return nil
	}
	for _, t := range tokens {
		if !t.IsExact {
			return nil
		}
	}
	if maxResults <= 0 {
		maxResults = 4
	}
	syllText := make([]string, n)
	for i, t := range tokens {
		syllText[i] = t.Text
	}
	spanCache := make(map[[2]int][]typoPhraseSpan, n)
	span := func(i, j int) []typoPhraseSpan {
		key := [2]int{i, j}
		if got, ok := spanCache[key]; ok {
			return got
		}
		limit := 4
		if j-i == 1 {
			limit = 8 // 单字同音候选多，取高频前 8 个足够覆盖常用字
		}
		var out []typoPhraseSpan
		seen := make(map[string]bool, limit*2)
		add := func(word string, sylls []string, freq int, segs []string) {
			if word == "" || seen[word] {
				return
			}
			seen[word] = true
			out = append(out, typoPhraseSpan{
				word:  word,
				sylls: append([]string(nil), sylls...),
				freq:  freq,
				segs:  append([]string(nil), segs...),
			})
		}
		syll := append([]string(nil), syllText[i:j]...)
		for _, e := range s.dict.QueryExact(syll) {
			add(e.Word, e.Syllables, e.Freq, nil)
		}
		if s.user != nil {
			for _, e := range s.user.QueryTokensExact(tokens[i:j], limit) {
				add(e.Word, e.Syllables, e.Freq, e.Segs)
			}
		}
		sort.SliceStable(out, func(a, b int) bool {
			if out[a].freq != out[b].freq {
				return out[a].freq > out[b].freq
			}
			return out[a].word < out[b].word
		})
		if len(out) > limit {
			out = out[:limit]
		}
		spanCache[key] = out
		return out
	}

	states := make([][]typoPhrasePath, n+1)
	states[0] = []typoPhrasePath{{}}
	for i := 0; i < n; i++ {
		if len(states[i]) == 0 {
			continue
		}
		maxJ := i + typoDPMaxSpan
		if maxJ > n {
			maxJ = n
		}
		for j := i + 1; j <= maxJ; j++ {
			spans := span(i, j)
			if len(spans) == 0 {
				continue
			}
			for _, st := range states[i] {
				for _, sp := range spans {
					np := typoPhrasePath{
						words: append(append([]string(nil), st.words...), sp.word),
						sylls: append(append([]string(nil), st.sylls...), sp.sylls...),
						score: st.score + sp.freq + typoDPWordBonus*(len(sp.sylls)-1),
					}
					if len(sp.sylls) == 1 {
						np.score -= typoDPSinglePenalty
					}
					if len(st.words) > 0 {
						np.score -= typoDPSegmentPenalty
					}
					if j == n {
						if len(sp.sylls) > 1 {
							np.score += typoDPTailWordBonus
						} else {
							np.score -= typoDPTailSinglePenalty
						}
					}
					states[j] = append(states[j], np)
				}
			}
			if len(states[j]) > typoDPMaxStates*4 {
				sort.SliceStable(states[j], func(a, b int) bool {
					return states[j][a].score > states[j][b].score
				})
				states[j] = states[j][:typoDPMaxStates]
			}
		}
	}

	paths := states[n]
	seenText := make(map[string]bool, len(paths))
	filtered := paths[:0]
	for _, p := range paths {
		if len(p.words) < 2 {
			continue // 单段 = 整词，常规 typo 整词查询已覆盖
		}
		word := strings.Join(p.words, "")
		if seenText[word] {
			continue
		}
		// 整词本身在词库里存在（如 你好 = 你+好）时跳过：常规 typo 查询会以
		// 整词候选给出，绝不能在这里给它补分段（会改变上屏学习语义）。
		if s.dict.Contains(word, p.sylls) {
			continue
		}
		seenText[word] = true
		filtered = append(filtered, p)
	}
	sort.SliceStable(filtered, func(a, b int) bool {
		if filtered[a].score != filtered[b].score {
			return filtered[a].score > filtered[b].score
		}
		if len(filtered[a].words) != len(filtered[b].words) {
			return len(filtered[a].words) < len(filtered[b].words)
		}
		return strings.Join(filtered[a].words, "") < strings.Join(filtered[b].words, "")
	})
	if len(filtered) > maxResults {
		filtered = filtered[:maxResults]
	}
	out := make([]*Entry, 0, len(filtered))
	for _, p := range filtered {
		out = append(out, &Entry{
			Word:      strings.Join(p.words, ""),
			Syllables: append([]string(nil), p.sylls...),
			Freq:      p.score,
			Segs:      append([]string(nil), p.words...),
		})
	}
	return out
}
