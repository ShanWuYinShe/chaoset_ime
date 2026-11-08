package engine

import (
	"sort"
	"strings"
)

// compose_typo.go —— 错拼修正路径（原 compose() 的 E/F/K 段）。
//
// 用户手滑打错一两个字母时（zhogn / niihao / zhonguo / nihap），常规路径要么拼出
// 一堆碎片假词，要么直接没候选。这里对输入做「单次编辑」修正，把能完整切分的
// 修正串拿去做正常查询，才能把「中 / 你好 / 中国」找回来。
//
// 三个阶段的顺序是语义的一部分（见 compose.go 编排）：
//  1. addTypoCorrections —— 生成/查询纠错候选，统一以最低层 rankTypoFix 加入；
//  2. promoteTypoCompositions —— 只在没有任何「整词级」纠错命中时，把纠错组合
//     DP 候选提升到精确层（长输入 / 图中 这类词库里没有整词条的场景）；
//  3. promoteTypoCorrections —— 排序前按词典事实把高置信度纠错提升到精确层，
//     并取消「常规路径也能命中」的候选的 typoFix 标记。
//
// typoState 与 items 同生命周期：typoFix 标记通过 items[add(...)] 的下标打上，
// 后续阶段按 rank/键反推，不能各自拷贝状态。

// typoState 是错拼路径的共享状态（原 compose() 内的 5 个 map + 计数器）。
type typoState struct {
	added       map[string]bool // 已加入候选池的纠错词条（词 + 音节串）
	phraseAdded map[string]bool // 来自纠错后「按已有词切分」DP 的候选
	isNear      map[string]bool // 该纠错来自物理邻键替换（保留原词频）
	mistakeKeys map[string]bool // 来自反序双字母归位修正（mistakeInput 时无条件提升）
	lens        map[string]int  // 修正串长度（tailSuspicious 时的完整性门槛）
	// wordFound 记录本次错拼修正是否命中过「整词级」结果（词典/用户词库里存在
	// 的完整词）。有整词级结果时，组合候选不得抢到精确层——否则 guabxi 的
	// 「古巴+西」会压掉真正纠错出来的「关系」。
	wordFound bool
	// mistakeSyllable 是反序归位修正后的首个音节（chagnshuru → chang），用于在
	// 只有词组候选时补首音节单字，保证「单字始终保留」。
	mistakeSyllable string
	clusterFixCount int // 归位修正在 fixes 里的条目数（其余是通用纠错兜底）
	phraseQueries   int // 已做的纠错分词 DP 次数（typoMaxPhraseQueries 上限）
}

func newTypoState() typoState {
	return typoState{
		added:       make(map[string]bool),
		phraseAdded: make(map[string]bool),
		isNear:      make(map[string]bool),
		mistakeKeys: make(map[string]bool),
		lens:        make(map[string]int),
	}
}

// addTypoCorrections 生成并查询错拼修正候选（原 compose() 的 E 段 + 反序归位后的
// 首音节单字补全）。只有输入形态被判定为可疑（typo/mid/tail）时才执行。
func (st *composeState) addTypoCorrections() {
	s := st.s
	typoAdded := st.typo.added
	typoPhraseAdded := st.typo.phraseAdded
	mistakeKeys := st.typo.mistakeKeys
	typoIsNear := st.typo.isNear
	typoLen := st.typo.lens
	tailSuspicious := st.shape.TailSuspicious
	if st.shape.TypoSuspicious || st.shape.MidSuspicious || tailSuspicious {
		// 含「反序双字母」（gn/hz/hc/hs）时只使用最高置信度的归位修正：
		// 这类组合在拼音里不合法，是明确手误；不再混入删除/插入/邻键替换等
		// 通用编辑，避免「chagn 删 g 得到 chan → 产」或用户学习词（孩子/产）
		// 把正确修正挤掉。若归位修正无法切成合法音节（极端情况），退回通用纠错。
		mistakeOnly := st.shape.MistakeInput
		var fixes []typoCorrection
		if mistakeOnly {
			clusterFixes := commonMistakeFixes(s.input)
			st.typo.clusterFixCount = len(clusterFixes)
			for _, f := range clusterFixes {
				fixes = append(fixes, typoCorrection{text: f, isNear: true})
			}
			// 归位修正之外仍保留通用纠错作为兜底/补充：单字候选来自这里
			// （chagnshuru 的「差/茶馆」），保证「单字始终保留」不被破坏；
			// 归位候选会无条件提升到精确层，仍排在前面。
			fixes = append(fixes, typoCorrections(s.input, typoMaxCorrections)...)
		}
		if len(fixes) == 0 {
			fixes = typoCorrections(s.input, typoMaxCorrections)
		}
		if mistakeOnly && len(fixes) > 0 {
			if pats := enumerateAllTokens(strings.Split(fixes[0].text, "'")); len(pats) > 0 && len(pats[0]) > 0 {
				st.typo.mistakeSyllable = pats[0][0].Text
			}
		}
		// 只查询靠前的若干条：生成很便宜（约 39µs），而每条一次的 Trie 查询
		// 才是这部分的主要成本。typoCorrections 已按价值排序（相邻互换 →
		// 删除 → 粘键折叠 → 邻键替换 → 插入 → 远端替换），前若干条覆盖绝大
		// 多数真实手误。
		if len(fixes) > typoMaxQueries {
			fixes = fixes[:typoMaxQueries]
		}
		// 修正优先级加权：typoCorrections 已按「相邻互换 → 删除 → 粘键 → 邻键
		// 替换 → 插入」排好序，人工手误里越靠前的修正越可信。组合 DP 候选没有
		// 语言模型，用这个顺序做先验，避免「删除 g 得到的 chanshuru」压过
		// 「ng 互换得到的 changshuru」这类真正的意图。
		for fixIdx, fix := range fixes {
			isClusterFix := mistakeOnly && fixIdx < st.typo.clusterFixCount
			patterns := enumerateAllTokens(strings.Split(fix.text, "'"))
			if len(patterns) == 0 {
				continue
			}
			qp := patterns[0]
			penalty := 0
			if !fix.isNear {
				penalty = nonAdjacentTypoPenalty
			}
			exacts, _ := s.dict.QueryTokens(qp, 8)
			for _, e := range exacts {
				key := userKey(e.Word, e.Syllables)
				if typoAdded[key] {
					continue
				}
				typoAdded[key] = true
				typoIsNear[key] = fix.isNear
				typoLen[key] = len(fix.text)
				boost := 0
				if s.user != nil && !mistakeOnly && !st.shape.MistakeInput {
					boost = s.user.Score(e.Word, e.Syllables, s.prev)
				}
				score := e.Freq + boost - penalty
				if score < 1 {
					score = 1
				}
				st.items[st.add(e, score, boost > 0, rankTypoFix)].cand.typoFix = true
				if isClusterFix {
					mistakeKeys[key] = true
				}
				st.typo.wordFound = true
			}
			if s.user != nil && !mistakeOnly && !st.shape.MistakeInput {
				uExacts, _ := s.user.QueryTokens(qp, 4)
				for _, e := range uExacts {
					key := userKey(e.Word, e.Syllables)
					if typoAdded[key] {
						continue
					}
					typoAdded[key] = true
					typoIsNear[key] = fix.isNear
					typoLen[key] = len(fix.text)
					score := e.Freq - penalty
					if score < 1 {
						score = 1
					}
					st.items[st.add(e, score, true, rankTypoFix)].cand.typoFix = true
					st.typo.wordFound = true
				}
			}

			// 纠错后的「按已有词切分」DP：修正串在词库里没有单条整词时（如
			// changshuru 没有「长输入」词条，但「长」+「输入」都存在），把修正后
			// 的音节切成已有词序列，给出词组候选（图中 = 图+中 同理）。
			// 只在这个错拼路径里组合；正常输入仍不拼合造词。查询条数有上限。
			//
			// 先以最低层级加入；只有在**没有任何整词级纠错命中**时，循环结束
			// 后才提升到精确层（见 promoteTypoCompositions）。这样组合候选
			// 不会靠奖励分把「关系/发票/你好/我们」这些真正的纠错结果挤掉。
			// tailSuspicious（合法混拼的末位声母，如 chifanl / nihap）不生成；
			// 替换/插入类修正也不生成（单词语义已能覆盖，组合只会带来噪声）。
			phraseBonus, phraseOK := typoFixPhraseBonus(s.input, fix.text)
			if !tailSuspicious && phraseOK && st.typo.phraseQueries < typoMaxPhraseQueries {
				st.typo.phraseQueries++
				fixBonus := phraseBonus
				for _, e := range s.composedPhraseEntries(qp, 6) {
					key := userKey(e.Word, e.Syllables)
					if typoAdded[key] {
						continue
					}
					typoAdded[key] = true
					typoPhraseAdded[key] = true
					typoIsNear[key] = fix.isNear
					typoLen[key] = len(fix.text)
					score := e.Freq - penalty + fixBonus
					if score < 1 {
						score = 1
					}
					st.items[st.add(e, score, false, rankTypoFix)].cand.typoFix = true
				}
			}
		}
	}

	// 反序归位输入如果只有词组候选（chagnshuru → 常输入/长输入），补上
	// 修正后首音节的单字候选，保证「单字始终保留」、用户仍可逐字选择。
	if st.shape.MistakeInput && st.typo.mistakeSyllable != "" {
		hasSingle := false
		for i := range st.items {
			if len([]rune(st.items[i].cand.Text)) == 1 {
				hasSingle = true
				break
			}
		}
		if !hasSingle {
			for _, e := range s.dict.QueryTokensExact([]Token{{Text: st.typo.mistakeSyllable, IsExact: true}}, 8) {
				key := userKey(e.Word, e.Syllables)
				if typoAdded[key] {
					continue
				}
				typoAdded[key] = true
				typoIsNear[key] = true
				typoLen[key] = len(s.input)
				st.items[st.add(e, e.Freq, false, rankTypoFix)].cand.typoFix = true
			}
		}
	}
}

// promoteTypoCompositions 提升纠错组合候选（原 compose() 的 F 段）。
//
// 只有该输入没有任何「整词级」纠错命中时才提到精确层（长输入 / 图中 这类词库里
// 没有整词条的场景）；只要纠错已找到词库里的完整词（关系/发票/你好），组合候选
// 就留在低层，避免拼接结果压过正确纠错。提升名额按得分取前 typoPhrasePromoteMax
// 个：组合没有语言模型，候选之间只能靠词频×修正类型置信度排序，宁少勿滥
// （其余仍可从后页选到）。
func (st *composeState) promoteTypoCompositions() {
	if len(st.typo.phraseAdded) == 0 || st.typo.wordFound {
		return
	}
	var idx []int
	for i := range st.items {
		if !st.items[i].cand.typoFix {
			continue
		}
		candKey := userKey(st.items[i].cand.Text, strings.Split(st.items[i].cand.Syllables, "'"))
		if st.typo.phraseAdded[candKey] {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return st.items[idx[a]].cand.Score > st.items[idx[b]].cand.Score
	})
	if len(idx) > typoPhrasePromoteMax {
		idx = idx[:typoPhrasePromoteMax]
	}
	for _, i := range idx {
		st.items[i].rank = rankExact
	}
}

// promoteTypoCorrections 是排序前的纠错层级提升 + typoFix 标记清理
// （原 compose() 的 K 段）。必须在 G/H/I/J 全部收集完之后、L 过滤之前执行。
func (st *composeState) promoteTypoCorrections() {
	if len(st.typo.added) == 0 {
		return
	}
	s := st.s
	typoKey := func(c Candidate) string {
		return userKey(c.Text, strings.Split(c.Syllables, "'"))
	}
	// 只有**纯错拼来源**的候选才保留整串消费标记。常规切分路径也命中的
	// 候选（chifanl 的「吃饭」rankInitialPrefix、zhongg 的「中国」rankInitialExact、
	// nihaox 的「你好」rankExact）说明输入本身就能按音节消费到该词——
	// 渐进上屏必须保持既有语义，只消费其音节前缀（chifanl 选「吃饭」剩 l）。
	// rank 在常规路径命中去重时会降到 < rankTypoFix（add 只降不升），
	// 所以「rank 仍是 rankTypoFix」= 只有错拼路径找到过它。
	for i := range st.items {
		if !st.items[i].cand.typoFix || st.items[i].rank == rankTypoFix {
			continue
		}
		// 纠错组合 DP 的候选直接以 rankExact 加入（见上），不是常规路径
		// 命中的整词——它的「整串消费」语义必须保留，不能在这里被取消。
		if st.typo.phraseAdded[typoKey(st.items[i].cand)] {
			continue
		}
		st.items[i].cand.typoFix = false
	}
	bestTypoFreq := 0
	for i := range st.items {
		k := typoKey(st.items[i].cand)
		if st.typo.phraseAdded[k] {
			continue
		}
		if st.shape.MistakeInput && !st.typo.mistakeKeys[k] {
			continue
		}
		if st.typo.added[k] && st.typo.isNear[k] &&
			(!st.shape.TailSuspicious || st.typo.lens[k] >= len(s.input)) && st.items[i].cand.Score > bestTypoFreq {
			bestTypoFreq = st.items[i].cand.Score
		}
	}
	// 「常规候选有多强」只能采信**单词级精确匹配**，必须排除一切拼接产物：
	// zhogn 的候选里有「中欧个」(265 分)，它是靠把残段拆成 1 字母音节拼出来
	// 的（zhong'ou'ge，层级为 rankContinuationLead）——这类分数是「多字词
	// 奖励叠出来的虚高值」，与真实词频不可比，用它做门槛会把纠错永远挡住。
	//
	// 只采信 rankExact / rankInitialExact 两级（直接命中词典词条）。
	bestRegularFreq := 0
	hasRegularWord := false
	for i := range st.items {
		if st.typo.added[typoKey(st.items[i].cand)] {
			continue
		}
		if st.items[i].rank != rankExact && st.items[i].rank != rankInitialExact {
			continue
		}
		// transpose 触发的手误场景：只有「音节数 == 输入段数」的全消耗
		// 精确词才算常规候选。中间夹声母的组合产物（zhengchagn 的
		// 「正茶馆你」）与单字碎片（正/茶）都是强行切分的产物——用户
		// 打 zhengchagn 的意图显然不是「正」单字，拿它们做门槛分母会让
		// 正确的纠错（正常）永远无法领跑（2026-10-25 用户反馈）。
		if (st.shape.TypoSuspicious || st.shape.MidSuspicious || st.shape.TailSuspicious) &&
			st.shape.InputSegs > 0 && strings.Count(st.items[i].cand.Syllables, "'")+1 != st.shape.InputSegs {
			continue
		}
		hasRegularWord = true
		if st.items[i].cand.Score > bestRegularFreq {
			bestRegularFreq = st.items[i].cand.Score
		}
	}
	// 反序双字母（gn/hz/hc/hs）是明确手误：归位修正无条件提升到精确层，
	// 不受常规候选词频门槛约束（常规解析往往是另一条合法混拼，如 hzong
	// 的「汇总」、hci 的「贺词」）。
	if bestTypoFreq > 0 && (st.shape.MistakeInput || !hasRegularWord || bestTypoFreq >= typoPromoteRatio*bestRegularFreq) {
		for i := range st.items {
			k := typoKey(st.items[i].cand)
			if st.typo.phraseAdded[k] {
				continue // 组合候选只由 promoteTypoCompositions 的规则提升
			}
			if st.shape.MistakeInput && !st.typo.mistakeKeys[k] {
				continue // 反序归位输入只提升归位修正，通用编辑保持低层
			}
			if st.typo.added[k] && st.typo.isNear[k] &&
				(!st.shape.TailSuspicious || st.typo.lens[k] >= len(s.input)) {
				st.items[i].rank = rankExact
			}
		}
	}
}
