package engine

// compose_shape.go —— 输入形态判定（compose() 拆出的纯函数）。
//
// 这里没有 Session 依赖、没有副作用：只根据输入串与全部合法音节切分算出
// 后续各路径共用的形态标志。单独成文件便于用 table test 覆盖形态判定，
// 不必构造完整 Session/词典。
type composeShape struct {
	// MinPatternLen 是最短切分的段数（0 = 没有任何合法切分）。
	MinPatternLen int
	// AllExactShape 表示输入存在一条「所有音节都是完整全拼、没有声母简拼段」
	// 的最干净解析。只有存在干净解析时，续接才退居次要位置。
	AllExactShape bool
	// ContinuationRank 是两条续接路径的层级：存在干净解析时与 rankExact 同层
	// 竞争（由「词频 - 补全代价」决定），否则用 rankContinuationLead 领跑。
	ContinuationRank int
	// 错拼形态（互斥的细分层级）：
	TypoSuspicious bool // 连简拼路径都排不出来 → 生成纠错候选，常规路径跳过昂贵 prefix 收集
	MidSuspicious  bool // 最优切分里非末位有单字母声母碎片（zhengchagn 的 g）
	TailSuspicious bool // 末位是孤立声母碎片（chifanl 的 l / nihap 的 p）
	MistakeInput   bool // 含反序双字母（gn/hz/hc/hs）且不能按正常切分解释：只走归位修正
	// 超段仲裁口径：首选切分的段数、所有切分里最多的段数、是否有简拼段。
	InputSegs        int
	MaxInputSegs     int
	InputHasInitials bool
}

// analyzeShape 计算输入形态（原 compose() 的 C 段，行为逐字保持）。
func analyzeShape(input string, patterns [][]Token) composeShape {
	sh := composeShape{AllExactShape: true}
	if len(patterns) > 0 {
		sh.MinPatternLen = len(patterns[0])
	}
	for _, pat := range patterns {
		if len(pat) < sh.MinPatternLen {
			sh.MinPatternLen = len(pat)
		}
	}
	// 输入的「最干净解析」：所有音节都是完整全拼、且没有任何一段被拆成
	// 声母简拼。只有当输入存在这样一条干净解析时，续接才退居次要位置——
	// 否则（quanmia 被切成 quan'mi'a、zhonggu 被切成 zhong'gu）候选里的
	// 东西全是碎片，应当由续接候选领跑。
	for _, pat := range patterns {
		if len(pat) != sh.MinPatternLen {
			continue
		}
		if !sh.AllExactShape {
			break
		}
		for _, tok := range pat {
			if !tok.IsExact {
				sh.AllExactShape = false
				break
			}
		}
	}
	// 分层规则（两条续接路径共用）。关键点：输入的末音节既可以按「已完整」
	// 解析（zhonggu = zhong'gu → 中古），也可以按「还差几个字母」解析
	// （zhong'gu→guo → 中国）。两种解析在输入串上**完全无法区分**，所以正确
	// 的做法不是人为规定谁优先，而是让它们同层竞争，由「词频 - 补全代价」
	// 决定（与真实输入法的拼音格 lattice + 语言模型一致）。
	// 例外：输入**无法**被干净切分成完整音节时（quanmia 只能切成 quan'mi'a），
	// 同级候选全是「全米啊」这类碎片假词，此时续接必须领跑，否则根本出不来。
	sh.ContinuationRank = rankExact
	if !sh.AllExactShape {
		sh.ContinuationRank = rankContinuationLead
	}
	// typoSuspicious 同时决定两件事：① 是否生成纠错候选；② 常规路径是否
	// 做昂贵的 prefix 收集——碎片输入的这些产物（zhogn 的「中欧个」）最终
	// 都会被纠错候选压掉，却花费数毫秒（实测 zhogn 单键 6ms 里常规路径占
	// 九成），是打错字瞬间卡顿的来源。
	sh.TypoSuspicious = misspelledFromPatterns(input, patterns)
	// 中间孤立声母形态：最优切分里「非末位」出现单字母声母碎片
	// （zhengchagn → zheng'cha'g'n，g 夹在两个完整音节之间）。可能是相邻
	// 颠倒手误（chagn → chang），也可能是合法混拼（shangxj → shang'x'j）
	// ——串形态不可分，放行常规路径，是否让纠错候选参与领跑由词典事实决定。
	sh.MidSuspicious = !sh.TypoSuspicious && hasMidIsolatedInitial(patterns)
	sh.TailSuspicious = !sh.TypoSuspicious && !sh.MidSuspicious && hasTailIsolatedInitial(patterns)
	// 含「反序双字母」（gn/hz/hc/hs）的输入：拼音里不合法，属于最高置信度手误。
	// 该形态只走归位修正，并强制把修正结果提升到精确层（不再受「纠错词频须
	// 达到常规候选 2 倍」的约束——h zong 的常规解析「汇总」词频很高，但用户
	// 显然想打 zhong）。整串能切成完整音节或纯声母简拼时，反序组合可能跨
	// 音节/声母边界（gongneng 的 gn = gong+neng；gn 简拼 = gong+neng），
	// 不能当手误。
	sh.MistakeInput = !hasCleanFullSplit(patterns) &&
		!allInitialPatterns(patterns) && hasCommonMistakeCluster(input)
	// maxInputSegs 是所有合法切分里最多的段数：tuan 既能切成 tuan（1 段）
	// 也能切成 tu'an（2 段），两种解析优先级相同——判「超段」必须按最多段数，
	// 否则 tu'an 的词（图案）会被当成超段，永远排在 tuan 的单字后面。
	if len(patterns) > 0 {
		sh.InputSegs = len(patterns[0])
		for _, tok := range patterns[0] {
			if !tok.IsExact {
				sh.InputHasInitials = true
				break
			}
		}
		for _, pat := range patterns {
			if len(pat) > sh.MaxInputSegs {
				sh.MaxInputSegs = len(pat)
			}
		}
	}
	return sh
}
