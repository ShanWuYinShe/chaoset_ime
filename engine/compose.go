package engine

import (
	"strconv"
	"strings"
)

// compose.go —— Compose() 的组词编排（原 compose() 1009 行函数的骨架）。
//
// 拆分后的职责文件（均在本包内、导出面不变）：
//
//	compose_state.go         composeState / candItem / add（候选收集器，唯一可变状态）
//	compose_shape.go         analyzeShape（输入形态判定，纯函数）
//	compose_continuation.go  两条末段残缺续接路径
//	compose_typo.go          错拼修正的生成/查询/提升
//	compose_query.go         连写匹配、常规 token 循环、单字兜底、已有词组合
//	compose_rank.go          匹配层级常量、排序比较器、纠错固定第 3 位
//	compose.go（本文件）      编排 + 单段过滤 + 分页/preedit/写 memo
//
// 调用顺序即语义，不能交换：
//  1. 续接（D）先于错拼（E）——错拼候选靠 add 的 rank 只降不升与去重合并；
//  2. E 之后立即做组合提升（F），再收集连写/常规/单字/组合（G/H/I/J）；
//  3. 纠错层级提升（K）必须在 H/I/J 全部收集完、单段过滤（L）之前执行
//     ——它按 items 下标与 rank 反推「纯错拼来源」；
//  4. L 用 items[:0] 原地复用（依赖旧切片不再被引用），之后才是排序（M/N）
//     与分页（O）。
func (s *Session) compose() Composition {
	// 双拼方案随每次组词状态下发（壳据此把 ';' 路由到 Input）；记忆化缓存
	// 存的是整个 Composition，命中路径同样带该字段。
	comp := Composition{Input: s.input, Shuangpin: s.dpScheme}
	if s.input == "" {
		return comp
	}
	st := newComposeState(s)
	st.addContinuations()
	st.addTypoCorrections()
	st.promoteTypoCompositions()
	st.addJoinedMatches()
	st.addTokenMatches()
	st.addSingleCharBackfill()
	st.addCompositionCandidates()
	st.promoteTypoCorrections()
	st.filterSingleSegment()
	st.sortCandidates()
	st.placeTypoFixedPosition()
	return st.buildComposition(comp)
}

// filterSingleSegment 是单段输入的单字过滤（原 compose() 的 L 段）。
//
// 单音节全拼（diao/yi/jie）与单段声母（d/y）不再出任何词语——既不是前缀扩展词
// （jie 的「结果/节约」），也不是连写恰好等于输入的全匹配词（xian 的「西安」、
// jie 的「饥饿」）。用户明确：把拼音当单字拼音时，拼音匹配候选里不能出现词语；
// 词语只能通过显式分段（xi'an/ji'e）或完整多音节输入匹配，联想另算。
//
// 复用 items[:0] 原地过滤：旧切片元素在循环内被覆盖，但读取下标恒大于写入下标，
// 不会读到未处理的元素（原实现的刻意优化，勿改成新建切片）。
func (st *composeState) filterSingleSegment() {
	if len(st.patterns) == 0 || len(st.patterns[0]) != 1 {
		return
	}
	tok := st.patterns[0][0]
	// 单字母声母（d/y）才是「单段输入」；sh/ch/zh 是多字母简拼，
	// 继续按词候选处理，不做单字过滤。
	if !tok.IsExact && len(tok.Text) != 1 {
		return
	}
	// 单音节全拼只保留「整个输入就是该音节」的单字；回退/另一切分产生的
	// 其他音节单字（diao 的 di 声母字「第」）也不应混进来。模糊音/别名
	// 输入的候选音节按 canonical + fuzzyVariants 归一后比较。
	canon := canonicalSyllable(tok.Text)
	allowed := map[string]bool{canon: true}
	for _, v := range fuzzyVariants(canon) {
		allowed[v] = true
	}
	kept := st.items[:0]
	for _, it := range st.items {
		text := it.cand.Text
		if len([]rune(text)) > 1 {
			// 单段输入的拼音匹配层只允许单字：词语全部丢弃。
			continue
		}
		if !tok.IsExact {
			// 单字母声母：只保留该声母能匹配的单字。纠错/回退路径会给
			// d 混入 e/a/o 声母字（QWERTY 邻键修正），这里一并清掉。
			if matchSyllable(canonicalSyllable(strings.ReplaceAll(it.cand.Syllables, "'", "")), tok) {
				kept = append(kept, it)
			}
			continue
		}
		if allowed[strings.ReplaceAll(it.cand.Syllables, "'", "")] {
			kept = append(kept, it)
		}
	}
	st.items = kept
}

// buildComposition 是分页/preedit 收尾（原 compose() 的 O 段）。
//
// 必须保留两处副作用：
//   - 把 s.page 钳制到 [0, pageCount-1]（翻页越界钳制语义，pageCount=0 时归零）；
//   - 写回记忆化缓存（key = input + \x00 + page，见 Compose()）。
func (st *composeState) buildComposition(comp Composition) Composition {
	s := st.s
	merged := make([]Candidate, len(st.items))
	for i, it := range st.items {
		merged[i] = it.cand
	}

	// 分页处理
	pageCount := (len(merged) + pageSize - 1) / pageSize
	if pageCount == 0 {
		s.page = 0
	} else if s.page >= pageCount {
		s.page = pageCount - 1
	} else if s.page < 0 {
		s.page = 0
	}
	start := s.page * pageSize
	end := start + pageSize
	if end > len(merged) {
		end = len(merged)
	}

	preedit := s.input
	if len(merged) > 0 && len(st.patterns) > 0 && len(st.patterns[0]) > 0 {
		var parts []string
		for _, tok := range st.patterns[0] {
			parts = append(parts, tok.Text)
		}
		preedit = strings.Join(parts, "'")
	}

	comp.Preedit = preedit
	comp.Page = s.page
	comp.PageCount = pageCount
	comp.Candidates = merged[start:end]
	// 写入记忆化缓存（见 Compose 注释）
	s.composeMemoKey = s.input + "\x00" + strconv.Itoa(s.page)
	s.composeMemo = comp
	return comp
}
