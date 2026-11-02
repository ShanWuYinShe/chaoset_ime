package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// 测试两字母声母简拼匹配常见词汇（如 zg -> 中国, bj -> 北京, cs -> 城市, sj -> 世界）。
func TestInitialsTwoLetters(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	cases := []struct {
		input   string
		wantTop string
	}{
		{"zg", "中国"},
		{"bj", "北京"},
		{"cs", "城市"},
		{"sj", "世界"},
	}

	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Fatalf("%s: 应出候选，实际无候选", c.input)
		}
		if comp.Candidates[0].Text != c.wantTop {
			t.Errorf("%s: 第一候选 = %q, 期望 %q（全部候选: %+v）", c.input, comp.Candidates[0].Text, c.wantTop, comp.Candidates)
		}
	}
}

// 测试三字及多字声母简拼（如 jsj -> 计算机, rmb -> 人民币）。
func TestInitialsMultiLetters(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	cases := []struct {
		input   string
		wantTop string
	}{
		{"jsj", "计算机"},
		{"rmb", "人民币"},
	}

	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Fatalf("%s: 应出候选，实际无候选", c.input)
		}
		if comp.Candidates[0].Text != c.wantTop {
			t.Errorf("%s: 第一候选 = %q, 期望 %q（全部候选: %+v）", c.input, comp.Candidates[0].Text, c.wantTop, comp.Candidates)
		}
	}
}

// 测试全拼+简拼混合输入（混拼，如 zhongg -> 中国, beij -> 北京, womd -> 我们的）。
func TestMixedPinyin(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	cases := []struct {
		input   string
		wantTop string
	}{
		{"zhongg", "中国"},
		{"beij", "北京"},
		// 混拼末段是完整音节时，最长已有词前缀「我们」领跑；「我们的」不在
		// 词典里，自 2026-11-01 起不再由引擎拼合（见 sentence_test.go）。
		{"womd", "我们"},
	}

	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Fatalf("%s: 应出候选，实际无候选", c.input)
		}
		if comp.Candidates[0].Text != c.wantTop {
			t.Errorf("%s: 第一候选 = %q, 期望 %q（全部候选: %+v）", c.input, comp.Candidates[0].Text, c.wantTop, comp.Candidates)
		}
	}

	// chifanl 混拼：已完整的「吃饭」可达；但「吃饭了」不在词典里，
	// 不属于「已存在的词」，不得由引擎拼合出来（2026-11-01 用户要求）。
	s.Reset()
	compChifan, err := s.Input("chifanl")
	if err != nil {
		t.Fatalf("Input chifanl 失败: %v", err)
	}
	all := allCandidates(s, compChifan)
	if !hasCandidate(all, "吃饭") {
		t.Fatalf("chifanl 候选应包含已完整的「吃饭」，实际候选: %+v", all[:min(6, len(all))])
	}
	for _, cand := range all {
		if cand.Text == "吃饭了" {
			t.Fatalf("chifanl 候选不应包含拼合造词「吃饭了」: %+v", all[:min(6, len(all))])
		}
	}
}

// 简拼不再用动态规划拼复合整句（2026-11-01 用户要求）：
// wmd 的候选全部来自词典，首选是能完整匹配简拼序列的词典词「无目地」
// （词频 9，属真实词条）；「我们的」不在词典里，不得出现。
// 用户真正常打的「我们」通过逐级回退可达，选中后剩余 d 继续组词。
func TestInitialsNoComposedSentence(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)

	comp, err := s.Input("wmd")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatalf("wmd 应出候选，实际无候选")
	}
	// 正向：已有词「我们」可达。简拼按声母逐音节消费：w→wo、m→men，
	// 剩余 d 继续组词（不再整串清空）。
	idx := findCandidate(t, s, "我们")
	rest, committed, err := s.SelectPrefix(idx)
	if err != nil || committed != "我们" {
		t.Fatalf("渐进选「我们」失败: committed=%q err=%v", committed, err)
	}
	if rest.Input != "d" || len(rest.Candidates) == 0 {
		t.Fatalf("简拼选「我们」应只消耗 w/m，剩余 d 继续出候选，实际 %+v", rest)
	}

	// 反向：全部候选必须能在词典找到出处，且不得出现「我们的」这类造词。
	s.Reset()
	all := allCandidates(s, mustComp(s, "wmd"))
	assertAllCandidatesInDict(t, d, "wmd", all)
	for _, cand := range all {
		if cand.Text == "我们的" {
			t.Fatalf("wmd 候选不应包含拼合造词「我们的」: %+v", all[:min(6, len(all))])
		}
	}
}

// 测试全拼音节绝不被简拼压制（如 ba 首选必须是单字「把」，而不是简拼匹配的「保安」）。
func TestFullPinyinOutranksInitials(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	comp, err := s.Input("ba")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "把" {
		t.Fatalf("ba 第一候选应为「把」，实际得到 %+v", comp.Candidates)
	}
}

// 测试使用撇号强制切分简拼（如 b'a 匹配 保安/备案，x'a 匹配 西安）。
func TestApostropheWithInitials(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	// x'a -> 西安
	comp1, _ := s.Input("x'a")
	foundXian := false
	for _, c := range comp1.Candidates {
		if c.Text == "西安" {
			foundXian = true
			break
		}
	}
	if !foundXian {
		t.Fatalf("x'a 候选应包含「西安」，得到 %+v", comp1.Candidates)
	}

	// b'a -> 候选应包含 保安 或 备案
	s.Reset()
	comp2, _ := s.Input("b'a")
	foundBaoan := false
	for _, c := range comp2.Candidates {
		if c.Text == "保安" || c.Text == "备案" {
			foundBaoan = true
			break
		}
	}
	if !foundBaoan {
		t.Fatalf("b'a 候选应包含 保安 或 备案，得到 %+v", comp2.Candidates)
	}
}

// 测试平翘舌声母模糊映射：单输入 z 能匹配 z 和 zh 开头的音节。
func TestRetroflexInitialMatching(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	// zg 既能出「中国」(zh+g)，也能出「整个」(zh+g)
	comp, _ := s.Input("zg")
	hasZhongguo := false
	for _, c := range comp.Candidates {
		if c.Text == "中国" {
			hasZhongguo = true
			break
		}
	}
	if !hasZhongguo {
		t.Fatalf("zg 候选应包含「中国」，得到 %+v", comp.Candidates)
	}
}

// 测试简拼与用户词典自学习：
// 用户选定某一简拼候选后，调频生效，下次该候选优先度进一步提升。
func TestInitialsUserLearning(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user_initials.json")
	d := mustCommonDict(t)

	s1 := NewSession(d, mustUserDict(t, udPath))
	comp, _ := s1.Input("zg")
	// 找到「这个」
	idx := -1
	for i, c := range comp.Candidates {
		if c.Text == "这个" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("zg 候选应包含「这个」，得到 %+v", comp.Candidates)
	}

	committed, err := s1.Select(idx)
	if err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	if committed != "这个" {
		t.Fatalf("上屏 = %q, 期望「这个」", committed)
	}

	// 新会话中，调频后的「这个」应被加权排到第一
	s2 := NewSession(d, mustUserDict(t, udPath))
	comp2, _ := s2.Input("zg")
	if comp2.Candidates[0].Text != "这个" {
		t.Fatalf("学习后 zg 第一候选应为「这个」，实际得到 %+v", comp2.Candidates)
	}
	if !comp2.Candidates[0].FromUser {
		t.Errorf("第一候选应带有 FromUser 标记")
	}
}

// 用户高频学过的单字不得把简拼候选挤成碎片：jsj 里可以出现用户学过的
// 「计/算/机」单字候选（用户词库是合法来源），但不得拼成「计算鸡」这类造词，
// 也不得把「完整消耗三段简拼」的词典词「计算机」挤出首选。
func TestInitialsUserDictSinglesDoNotFragment(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user_jsj.json")
	d := mustCommonDict(t)
	u := mustUserDict(t, udPath)

	// 模拟用户日常高频输入过单个汉字「计」「算」「机」
	for i := 0; i < 5; i++ {
		_ = u.BumpWord("计", []string{"ji"})
		_ = u.BumpWord("算", []string{"suan"})
		_ = u.BumpWord("机", []string{"ji"})
	}

	s := NewSession(d, u)
	comp, err := s.Input("jsj")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatalf("jsj 应出候选，实际无候选")
	}
	all := allCandidates(s, comp)
	assertAllCandidatesInDict(t, d, "jsj", all)
	// ① 不允许把学习过的单字拼成复合候选（造词禁令对用户数据同样生效）
	for _, cand := range all {
		if cand.Text == "计算鸡" || cand.Text == "计机算" || cand.Text == "算计机" {
			t.Fatalf("jsj 候选不应包含拼合造词 %q: %+v", cand.Text, all[:min(6, len(all))])
		}
	}
	// ② 首选必须是完整消耗三段简拼（3 音节）的词典词，而不是单字碎片
	if all[0].Text != "计算机" {
		t.Fatalf("jsj 首选应为「计算机」，实际 %+v（全部候选: %+v）", all[0], all[:min(6, len(all))])
	}
	if got := strings.Count(all[0].Syllables, "'") + 1; got != 3 {
		t.Fatalf("jsj 首选应完整消耗 3 段简拼，实际首选 %+v", all[0])
	}
}

// 测试用户短语匹配严格受音节槽位数约束：
// 用户短语「如果我」(3音节) 绝不应泄漏至 1 音节声母槽中，拼出「如果我没不」等 5 字乱码。
func TestInitialsUserDictPhraseLengthConstraint(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user_rmb.json")
	d := mustCommonDict(t)
	u := mustUserDict(t, udPath)

	// 模拟用户学习了 3 音节词组「如果我」
	_ = u.BumpPhrase("如果我", []string{"ru", "guo", "wo"})

	s := NewSession(d, u)
	comp, err := s.Input("rmb")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatalf("rmb 应出候选，实际无候选")
	}
	if comp.Candidates[0].Text != "人民币" {
		t.Fatalf("rmb 第一候选应为「人民币」，实际得到 %q (候选列表: %+v)",
			comp.Candidates[0].Text, comp.Candidates)
	}
	for _, c := range comp.Candidates {
		if c.Text == "如果我没不" {
			t.Fatalf("rmb 候选不应包含单字槽泄漏生成的用户短语碎字拼接: %q", c.Text)
		}
	}
}

// 测试 UserDict.QueryTokens 正确区分 exactMatches 与 prefixMatches。
func TestUserDictQueryTokensExactAndPrefix(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user_tokens.json")
	u := mustUserDict(t, udPath)

	_ = u.BumpPhrase("人民", []string{"ren", "min"})
	_ = u.BumpPhrase("人民币", []string{"ren", "min", "bi"})
	_ = u.BumpPhrase("人民日报", []string{"ren", "min", "ri", "bao"})

	tokens := []Token{
		{Text: "r", IsExact: false},
		{Text: "m", IsExact: false},
	}

	exacts, prefixes := u.QueryTokens(tokens, 10)
	if len(exacts) != 1 || exacts[0].Word != "人民" {
		t.Fatalf("exactMatches 期望包含且仅包含「人民」，实际: %+v", exacts)
	}
	if len(prefixes) != 2 {
		t.Fatalf("prefixMatches 期望包含 2 个词（人民币、人民日报），实际: %+v", prefixes)
	}
}

// TestSelectPrefixInitialsPartialConsume：纯声母输入选词时按声母逐音节消费，
// 只吃掉候选覆盖的声母，剩余声母继续组词（用户反馈：kkdz 选「看看」不应把
// d'z 一起吞掉）。
func TestSelectPrefixInitialsPartialConsume(t *testing.T) {
	d := mustCommonDict(t)
	cases := []struct{ input, word, wantRest string }{
		{"kkdz", "看看", "dz"},
		{"kkdz", "开阔地", "z"},
		{"wmd", "我们", "d"},
	}
	for _, c := range cases {
		s := NewSession(d, nil)
		if _, err := s.Input(c.input); err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		idx := findCandidate(t, s, c.word)
		rest, committed, err := s.SelectPrefix(idx)
		if err != nil || committed != c.word {
			t.Fatalf("%s: 渐进选「%s」失败: committed=%q err=%v", c.input, c.word, committed, err)
		}
		if rest.Input != c.wantRest {
			t.Errorf("%s: 选「%s」后剩余输入 = %q, 期望 %q", c.input, c.word, rest.Input, c.wantRest)
		}
		if c.wantRest != "" && len(rest.Candidates) == 0 {
			t.Errorf("%s: 剩余输入 %q 应继续出候选", c.input, c.wantRest)
		}
	}
}

// TestInitialsLongSingleCharFallback：长串纯声母（含撇号分隔）不是错拼；
// 没有整词覆盖整串时，首段单字全量可达，用户仍能逐字输入/自己造词。
func TestInitialsLongSingleCharFallback(t *testing.T) {
	d := mustCommonDict(t)
	for _, input := range []string{"k'k'd'z", "kkdz"} {
		s := NewSession(d, nil)
		comp, err := s.Input(input)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", input, err)
		}
		all := allCandidates(s, comp)
		if len(all) == 0 {
			t.Fatalf("%s: 应出候选，实际无候选", input)
		}
		singles := 0
		hasKao := false
		for _, c := range all {
			if len([]rune(c.Text)) == 1 {
				singles++
			}
			if c.Text == "烤" {
				hasKao = true
			}
		}
		if singles < 50 {
			t.Fatalf("%s: 首段单字全量兜底未生效（单字=%d，回退 top10 时约 10）", input, singles)
		}
		if !hasKao {
			t.Errorf("%s: 全量单字应包含生僻的「烤」，实际没有", input)
		}

		// 渐进消费：选「看看」只消耗 k'k，剩余 d'z 继续组词。
		s2 := NewSession(d, nil)
		if _, err := s2.Input(input); err != nil {
			t.Fatalf("%s: Input 失败: %v", input, err)
		}
		idx := findCandidate(t, s2, "看看")
		rest, committed, err := s2.SelectPrefix(idx)
		if err != nil || committed != "看看" {
			t.Fatalf("%s: 渐进选「看看」失败: committed=%q err=%v", input, committed, err)
		}
		wantRest := "dz"
		if strings.Contains(input, "'") {
			wantRest = "d'z"
		}
		if rest.Input != wantRest {
			t.Errorf("%s: 选「看看」后剩余输入 = %q, 期望 %q", input, rest.Input, wantRest)
		}
	}
}

// TestInitialsNoWordStillInputtable：词库没有整词覆盖时（zxcv），仍要能
// 逐字输入——首段单字全量可达，选一个字后剩余声母继续出候选。
func TestInitialsNoWordStillInputtable(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	comp, err := s.Input("zxcv")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatalf("zxcv 应至少给出首段单字候选，实际无候选")
	}
	all := allCandidates(s, comp)
	singles := 0
	for _, c := range all {
		if len([]rune(c.Text)) == 1 {
			singles++
		}
	}
	if singles < 50 {
		t.Fatalf("zxcv 首段单字全量兜底未生效（单字=%d）", singles)
	}

	s2 := NewSession(d, nil)
	if _, err := s2.Input("zxcv"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	idx := findCandidate(t, s2, "在") // z 声母单字（全量兜底可达）
	rest, committed, err := s2.SelectPrefix(idx)
	if err != nil || committed != "在" {
		t.Fatalf("zxcv 选首段单字「在」失败: committed=%q err=%v", committed, err)
	}
	if rest.Input != "xcv" || len(rest.Candidates) == 0 {
		t.Fatalf("zxcv 选「在」后剩余输入 = %q，应剩 xcv 并继续出候选", rest.Input)
	}
}

// TestShortInitialsAlwaysSingleFallback：两段简拼同样追加首段全量单字，
// 任何时候都能逐字挑选（不再只给 top10）。
func TestShortInitialsAlwaysSingleFallback(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	comp, err := s.Input("zg")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	want := len(d.QueryTokensExact([]Token{{Text: "z", IsExact: false}}, 500))
	if want > singleSyllableExactLimit {
		want = singleSyllableExactLimit
	}
	singles := 0
	for _, c := range allCandidates(s, comp) {
		if len([]rune(c.Text)) == 1 {
			singles++
		}
	}
	if singles < want {
		t.Fatalf("zg 首段单字全量兜底未生效（单字=%d，期望 >=%d）", singles, want)
	}
}

// TestUserWordPriorityForInitials：简拼输入下用户学过的词优先于默认词频。
// ysfa 的默认切分是 y's'fa（3 段），学过 4 音节的「原生方案」若按「音节数
// > token 数」判超段，会被压到默认短词后面；简拼的 token 数不是音节数，
// 不能这样判——用户输入（学过的词）必须排在默认字频词之前。
func TestUserWordPriorityForInitials(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user.json")
	d := mustCommonDict(t)
	u := mustUserDict(t, udPath)
	if err := u.BumpWord("原生方案", []string{"yuan", "sheng", "fang", "an"}); err != nil {
		t.Fatalf("学习「原生方案」失败: %v", err)
	}
	s := NewSession(d, u)
	comp := mustComp(s, "ysfa")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "原生方案" {
		t.Fatalf("ysfa 首选应是学过的「原生方案」（用户输入 > 默认字频），实际: %+v",
			firstTexts(allCandidates(s, comp), 6))
	}
}

// TestUserWordPriorityStillGuardsFullPinyinOverSegment：全拼输入的超段仲裁
// 不能被「用户词优先」破坏——学过「好家伙」后输入 hao，「好」仍是首选。
func TestUserWordPriorityStillGuardsFullPinyinOverSegment(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user.json")
	d := mustCommonDict(t)
	u := mustUserDict(t, udPath)
	if err := u.BumpWord("好家伙", []string{"hao", "jia", "huo"}); err != nil {
		t.Fatalf("学习「好家伙」失败: %v", err)
	}
	s := NewSession(d, u)
	comp := mustComp(s, "hao")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "好" {
		t.Fatalf("全拼 hao 的首选应仍是「好」（超段仲裁），实际: %+v",
			firstTexts(allCandidates(s, comp), 6))
	}
}

// TestUserWordPriorityForAmbiguousSegmentation：tuan 可切成 tuan 或 tu'an，
// 两种解析优先级相同；用户学过 tu'an 的「图案」后应排在 tuan 的「团」之前
// （用户输入 > 默认字频）。
func TestUserWordPriorityForAmbiguousSegmentation(t *testing.T) {
	d := mustCommonDict(t)

	s := NewSession(d, nil)
	allDefault := allCandidates(s, mustComp(s, "tuan"))
	hasTuAn := false
	for _, c := range allDefault {
		if c.Text == "图案" {
			hasTuAn = true
		}
	}
	if !hasTuAn {
		t.Fatalf("未学习时 tuan 候选里也应有 tu'an 的「图案」: %+v", firstTexts(allDefault, 6))
	}

	u := mustUserDict(t, filepath.Join(t.TempDir(), "user.json"))
	if err := u.BumpWord("图案", []string{"tu", "an"}); err != nil {
		t.Fatalf("学习「图案」失败: %v", err)
	}
	s2 := NewSession(d, u)
	comp2 := mustComp(s2, "tuan")
	if len(comp2.Candidates) == 0 || comp2.Candidates[0].Text != "图案" {
		t.Fatalf("学过「图案」后 tuan 首选应为「图案」，实际: %+v", firstTexts(allCandidates(s2, comp2), 6))
	}
}

// TestSingleInitialMoreSingles：单段声母（y）也放开到单字上限，旧 120 上限之外
// 的同声母单字必须可达（y 声母在词典里有 900+ 单字）。
func TestSingleInitialMoreSingles(t *testing.T) {
	d := mustCommonDict(t)
	entries := d.QueryTokensExact([]Token{{Text: "y", IsExact: false}}, 500)
	if len(entries) <= 120 {
		t.Fatalf("词典 y 声母单字仅 %d 个，无法验证 >120 场景", len(entries))
	}
	want := len(entries)
	if want > singleSyllableExactLimit {
		want = singleSyllableExactLimit
	}
	s := NewSession(d, nil)
	all := allCandidates(s, mustComp(s, "y"))
	singles := 0
	target := entries[250].Word
	found := false
	for _, c := range all {
		if len([]rune(c.Text)) == 1 {
			singles++
		}
		if c.Text == target {
			found = true
		}
	}
	if singles < want {
		t.Fatalf("y 单字候选 %d，期望 >=%d（单段声母仍被旧上限裁剪）", singles, want)
	}
	if !found {
		t.Fatalf("y 候选里找不到旧上限之外的字「%s」", target)
	}
}

// TestInitialsPageCountBounded：多段简拼的词候选 exact 上限保持 initialsExactLimit，
// 不能随单字上限一起放大（zg 会从 ~75 页膨胀到 ~84 页）。
func TestInitialsPageCountBounded(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp := mustComp(s, "zg")
	if comp.PageCount > 80 {
		t.Fatalf("zg 页数 %d 过多（多段简拼词候选上限被放大？）", comp.PageCount)
	}
}

// TestValidPinyinCrossBoundaryGN：gongneng 的 gn 是 gong+neng 跨音节边界，
// 不是反序手误（旧实现 hasCommonMistakeCluster 按原始串命中 gn → typo=true，
// 跳过常规路径与单字兜底，只剩 2 页词候选）。修复后必须出首段单字。
func TestValidPinyinCrossBoundaryGN(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	comp := mustComp(s, "gongneng")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "功能" {
		t.Fatalf("gongneng 首选应为「功能」，实际: %+v", firstTexts(allCandidates(s, comp), 6))
	}
	all := allCandidates(s, comp)
	singles := 0
	for _, c := range all {
		if len([]rune(c.Text)) == 1 {
			singles++
		}
	}
	if singles == 0 {
		t.Fatalf("gongneng 应有首段单字兜底（当前 %d 条候选全是词组）", len(all))
	}
	if comp.PageCount <= 2 {
		t.Fatalf("gongneng 应超过 2 页（单字兜底生效），实际 %d 页 / %d 条", comp.PageCount, len(all))
	}
}

// TestPureInitialsGNTrusted：gn 是合法简拼（gong+neng），不能被反序组合
// gn→ng 误判成手误、丢掉常规候选。
func TestPureInitialsGNTrusted(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	comp := mustComp(s, "gn")
	all := allCandidates(s, comp)
	found := false
	for _, c := range all {
		if c.Text == "功能" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("gn 简拼候选里应有「功能」，实际: %+v", firstTexts(all, 6))
	}
}

// TestTwoLetterInitialKeepsWords：sh/ch/zh 是多字母简拼（三后=sh、出会=ch），
// 不能按「单段输入只出单字」过滤掉词候选。
func TestTwoLetterInitialKeepsWords(t *testing.T) {
	d := mustCommonDict(t)
	for _, input := range []string{"sh", "ch", "zh"} {
		s := NewSession(d, nil)
		all := allCandidates(s, mustComp(s, input))
		words := 0
		for _, c := range all {
			if len([]rune(c.Text)) > 1 {
				words++
			}
		}
		if words == 0 {
			t.Fatalf("%s 是简拼，应保留多字词候选，实际全是单字", input)
		}
	}
}
