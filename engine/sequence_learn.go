package engine

import "strings"

// 本文件承载「整句式输入习惯」的学习与安全约束（2026-10-30 用户需求）：
//
//   - 多字拼音没有合适整句候选时，用户可以逐段选择（SelectPrefix）；
//   - 这个逐段拼句的过程要进入用户习惯学习：整串耗尽时把各段拼成的整句
//     记为带分段的 Words 记录，下次完整输入同一串时整句候选被调频加权；
//   - 但学习不得造成「整词贪婪匹配」：整句式记录只在**完整输入**时作为
//     整句候选，部分输入不产生整词/尾巴候选，联想也只在用户确认过的分段
//     边界上剥前缀——打「今」绝不给出「天星期一」，否则用户只想打
//     「今天」时会被整词候选/尾巴挤掉（用户原话：不能直接匹配整个词）。
//
// 分段用词文本表示：整句「今天星期一」的分段是 ["今天","星期一"]，与音节
// 序列按「汉字字数 == 音节数」对齐（词库构建期强制的口径，引擎自己上屏的
// 候选也来自词库条目）。任何对不上分段的数据一律退化为普通词学习，不臆造。

// consumeInputPrefix 从 input 中吃掉候选音节的字母前缀，返回剩余输入。
//
// 比较时忽略用户敲下的音节分隔符 '（jin'tian… 形态），吃到字母后连同紧随
// 的分隔符一起跳过，剩余串不会以 ' 开头。不匹配返回 ok=false（简拼/模糊音
// 音节展开等形态差异），调用方回退为整段上屏。
func consumeInputPrefix(input, typed string) (rest string, ok bool) {
	if typed == "" {
		return input, true
	}
	i, j := 0, 0
	for j < len(typed) {
		if i >= len(input) {
			return "", false
		}
		c := input[i]
		if c == '\'' {
			i++
			continue
		}
		if c != typed[j] {
			return "", false
		}
		i++
		j++
	}
	for i < len(input) && input[i] == '\'' {
		i++
	}
	return input[i:], true
}

// splitSyllsBySegs 按分段词的字数切分音节序列（「词长 == 音节数」）。
// 任一分段字数为 0 或总字数对不上时返回 nil，调用方退化为普通词学习。
func splitSyllsBySegs(sylls, segs []string) [][]string {
	if len(segs) < 2 || len(sylls) == 0 {
		return nil
	}
	out := make([][]string, 0, len(segs))
	pos := 0
	for _, seg := range segs {
		n := len([]rune(seg))
		if n == 0 || pos+n > len(sylls) {
			return nil
		}
		out = append(out, sylls[pos:pos+n])
		pos += n
	}
	if pos != len(sylls) {
		return nil
	}
	return out
}

// isSentenceSelection 判断一次上屏是否属于「整句式」：候选自带 >=2 个分段、
// 分段与音节序列严格对齐、整词不是词典里的普通词条、音节数不超过
// maxSentenceSylls（更长的输入是连打/误触，不是值得记住的习惯词）。
func (s *Session) isSentenceSelection(text string, sylls, segs []string) bool {
	if len(segs) < 2 || len(sylls) < 2 || len(sylls) > maxSentenceSylls {
		return false
	}
	if splitSyllsBySegs(sylls, segs) == nil || strings.Join(segs, "") != text {
		return false
	}
	if s.dict != nil && s.dict.Contains(text, sylls) {
		return false // 词典词按普通词学习即可，不需要整句语义
	}
	return true
}

// learnProgressiveSequence 在「本次组词全部由渐进上屏段拼完」时，把整串记成
// 带分段的整句习惯：下一轮完整输入同一串时，整句候选被用户调频加权
// （「方便后续在候选中给出更符合用户想要的词组」）。
//
// 只记整句习惯、不记可被部分输入命中的「整词」候选——见 learnSelection
// 对 Segs 语义的说明。>4 音节也能记住（词组进化 maxLearnSylls 只约束
// Phrases 表；2026-10-30 探针确认 5 音节组合此前完全学不到）。
func (s *Session) learnProgressiveSequence(segs []learnEntry) {
	if s.user == nil || s.dict == nil || len(segs) < 2 {
		return
	}
	var b strings.Builder
	var sylls []string
	segWords := make([]string, 0, len(segs))
	for _, seg := range segs {
		if seg.word == "" || len([]rune(seg.word)) != len(seg.sylls) {
			return // 分段与音节对不上：不记，宁缺勿错
		}
		b.WriteString(seg.word)
		sylls = append(sylls, seg.sylls...)
		segWords = append(segWords, seg.word)
	}
	word := b.String()
	if len(sylls) > maxSentenceSylls || s.dict.Contains(word, sylls) {
		return // 太长（非习惯）或本身就是词典词（普通词学习已覆盖）
	}
	_ = s.user.BumpWordWithSegs(word, sylls, segWords)
}

// inferSegs 用主词典把「非词典整词」切成**分段数最少**的词典词序列。
//
// 为什么不是最长词贪心（2026-10-30 探针 I5 实测踩坑）：旧数据「天星期一」
// 用贪心会命中词典里的「天星」(tian xing) → 切成 [天星,期,一]，分段边界
// 全错——联想在上屏「天」后不再给「星期一」，错误分段词「天星」还会被逐段
// 调频后污染候选。改 DP 取最少段数后可得到用户实际确认的 [天,星期一]。
//
// 只用于兼容两层数据：① 2026-10-30 之前写下的旧学习记录（没有 Segs 字段）；
// ② 联想增量词（壳只回传 word+sylls）。每个分段都必须是词典词；无法完全
// 切分或切不出多段时返回 nil（宁可当普通词，也不臆造边界）。
func (s *Session) inferSegs(word string, sylls []string) []string {
	if s.dict == nil || len(sylls) < 2 {
		return nil
	}
	runes := []rune(word)
	if len(runes) != len(sylls) || s.dict.Contains(word, sylls) {
		return nil
	}
	n := len(runes)
	// best[pos]：覆盖 runes[:pos] 的最少分段数（-1 = 不可达）；
	// prev[pos]：该最优解最后一段的起点，用于回溯。
	best := make([]int, n+1)
	prev := make([]int, n+1)
	for i := range best {
		best[i] = -1
	}
	best[0] = 0
	for pos := 0; pos < n; pos++ {
		if best[pos] < 0 {
			continue
		}
		for k := 1; pos+k <= n; k++ {
			if !s.dict.Contains(string(runes[pos:pos+k]), sylls[pos:pos+k]) {
				continue
			}
			if best[pos+k] < 0 || best[pos+k] > best[pos]+1 {
				best[pos+k] = best[pos] + 1
				prev[pos+k] = pos
			}
		}
	}
	if best[n] < 2 {
		return nil
	}
	segs := make([]string, 0, best[n])
	for pos := n; pos > 0; {
		p := prev[pos]
		segs = append(segs, string(runes[p:pos]))
		pos = p
	}
	for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
		segs[i], segs[j] = segs[j], segs[i]
	}
	return segs
}

// entrySegs 返回用户学习词条的分段：优先取记录里的 Segs（新数据）；
// 旧数据没有 Segs 时用主词典最长词切分推断，推断不出则视为普通词（nil）。
func (s *Session) entrySegs(e *Entry) []string {
	if e == nil {
		return nil
	}
	if len(e.Segs) >= 2 {
		return e.Segs
	}
	return s.inferSegs(e.Word, e.Syllables)
}

// isSentenceEntry 判断用户课条是否为「整句式」学习记录（新数据看 Segs，
// 旧数据看长度/词典切分）。整句式记录**不参与部分输入的前缀候选**：
// 否则打 jin 就能看到「今天星期一」这类整词候选，正是用户反对的整词贪婪匹配。
func (s *Session) isSentenceEntry(e *Entry) bool {
	if e == nil || len(e.Syllables) < 2 {
		return false
	}
	if len(e.Segs) >= 2 {
		return true
	}
	if s.dict == nil || s.dict.Contains(e.Word, e.Syllables) {
		return false
	}
	if len(e.Syllables) > maxLearnSylls {
		return true // 旧数据：>4 音节且不在词典，只可能是整句
	}
	return len(s.inferSegs(e.Word, e.Syllables)) >= 2
}

// resetSeqIfEmpty 在组词输入被清空时丢弃未完成的渐进段累积。
//
// 2026-10-30 探针 J1 实测：渐进选「今天」→ 退格到空（未 Reset）→ 输入 wo
// 选「我」，会把 [今天 我] 拼成伪整句学习记录——因为 seqSegs 只在
// Reset/Select/AssociateSelect 清空，Backspace/标点丢弃残缺输入都没有清。
// 已上屏的旧段属于上一轮组词，不能在下一轮组词完成时被拼成整句习惯。
func (s *Session) resetSeqIfEmpty() {
	if s.input == "" {
		s.seqSegs = nil
	}
}

// stripAtSegmentBoundary 在整句式学习记录的分段边界上剥掉已上屏前缀：
// prevWord 必须恰好等于前 k 段（k>=1 且 k<总段数）的拼接且音节数一致。
// 不在边界上返回 ok=false——调用方不应提供该「尾巴」候选。
func (s *Session) stripAtSegmentBoundary(e *Entry, segs []string, prevWord string, prevSylls []string) (text, syllStr string, ok bool) {
	if prevWord == "" || len(prevSylls) == 0 || len(segs) < 2 {
		return "", "", false
	}
	wordRunes := []rune(e.Word)
	runeCount, sylCount := 0, 0
	for k, seg := range segs {
		n := len([]rune(seg))
		runeCount += n
		sylCount += n
		if runeCount > len(wordRunes) || sylCount > len(e.Syllables) {
			return "", "", false
		}
		if string(wordRunes[:runeCount]) == prevWord && sylCount == len(prevSylls) {
			if runeCount >= len(wordRunes) || sylCount >= len(e.Syllables) {
				return "", "", false // 恰好等于整词：没有续接部分
			}
			return string(wordRunes[runeCount:]), strings.Join(e.Syllables[sylCount:], "'"), true
		}
		if k == len(segs)-1 {
			break
		}
	}
	return "", "", false
}
