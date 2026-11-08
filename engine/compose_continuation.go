package engine

// compose_continuation.go —— 两条「末段残缺」的续接预测路径（原 compose() 的 D 段）。
//
// 路径一：末音节本身已是合法音节，但它同时是更长音节的前缀（gu→guo、jin→jing），
// 用户很可能还没打完；路径二：末段连完整音节都不是（quanmia 的 mia），常规切分
// 会把残缺段拆成「完整音节 + 声母 + 韵母」碎片（quan'mi'a）并命中「全米啊」这类
// 假词，必须把「前面已完整 + 末段整体当待补全串」再查一次。
//
// 两条路径共用 addedContinuation 去重与同一层级规则（由 composeShape.ContinuationRank
// 决定：存在干净解析时与 rankExact 同层竞争，否则用 rankContinuationLead 领跑）。

// addContinuations 收集两条续接路径的候选。
func (st *composeState) addContinuations() {
	s := st.s
	// 末音节续接预测：末音节虽然已经打成一个合法音节，但它同时是某个更长
	// 音节的前缀时（gu→guo、jin→jing、xu→xue、ha→hao、ma→mian…），用户很
	// 可能还没打完。此时把末音节当作「待补全」做一次续接查询，才能让
	// zhonggu 打出「中国」、beijin 打出「北京」、daxu 打出「大学」。
	// 不加这一步时这些输入只能出「中古 / 被进 / 大需」这类巧合的碎词。
	for _, pat := range st.patterns {
		if len(pat) < 2 {
			continue
		}
		last := pat[len(pat)-1]
		if !last.IsExact || len(syllableContinuations(last.Text)) == 0 {
			continue
		}
		head := pat[:len(pat)-1]
		r := st.shape.ContinuationRank
		entryList := s.dict.QueryContinuation(head, last.Text, 24)
		if s.user != nil {
			entryList = append(entryList, s.user.QueryContinuation(head, last.Text, 8)...)
		}
		for _, sc := range entryList {
			key := userKey(sc.Entry.Word, sc.Entry.Syllables)
			if st.addedContinuation[key] {
				continue
			}
			st.addedContinuation[key] = true
			if sc.Remain == "" {
				continue // 末音节本身已是完整音节：等同精确匹配，由常规路径覆盖
			}
			boost := 0
			if s.user != nil {
				boost = s.user.Score(sc.Entry.Word, sc.Entry.Syllables, s.prev)
			}
			// 补全越长越不确定（gu→guo 远强于 gu→guang），按字符数折价。
			score := sc.Entry.Freq + boost - continuationPenalty*len(sc.Remain)
			st.add(sc.Entry, score, boost > 0, r)
		}
	}
	// 情况二：末段本身不是一个完整音节（quanmia / shangmia / zhonghuarenming），
	// 常规切分会把残缺段拆成「完整音节 + 声母 + 韵母」的碎片（quan'mi'a），
	// 命中「全米啊」这类假词。这里把「前面已完整 + 末段整体当作待补全串」
	// 再查一次，才能出「全面 / 上面」。
	for _, tail := range partialTailSplits(s.input) {
		entryList := s.dict.QueryContinuation(tail.tokens, tail.frag, 24)
		if s.user != nil {
			entryList = append(entryList, s.user.QueryContinuation(tail.tokens, tail.frag, 8)...)
		}
		for _, sc := range entryList {
			key := userKey(sc.Entry.Word, sc.Entry.Syllables)
			if st.addedContinuation[key] {
				continue
			}
			st.addedContinuation[key] = true
			boost := 0
			if s.user != nil {
				boost = s.user.Score(sc.Entry.Word, sc.Entry.Syllables, s.prev)
			}
			// 补全越长越不确定（qua→quan 稳，qua→quang 更勉强），按字符数折价；
			// 层级统一由 continuationRank 决定（干净解析时退后，否则领跑）。
			score := sc.Entry.Freq + boost - continuationPenalty*len(sc.Remain)
			st.add(sc.Entry, score, boost > 0, st.shape.ContinuationRank)
		}
	}
}
