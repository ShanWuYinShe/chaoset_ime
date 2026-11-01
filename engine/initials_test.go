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
	// 正向：已有词「我们」可达。简拼形态无法逐音节消费（w/m/d 与 wo'men
	// 逐字对不上），SelectPrefix 按既有回退语义整串上屏、组词清空。
	idx := findCandidate(t, s, "我们")
	rest, committed, err := s.SelectPrefix(idx)
	if err != nil || committed != "我们" {
		t.Fatalf("渐进选「我们」失败: committed=%q err=%v", committed, err)
	}
	if rest.Input != "" || len(rest.Candidates) != 0 {
		t.Fatalf("简拼选「我们」应整串上屏，剩余组词应为空，实际 %+v", rest)
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

// 用户高频学过的单字不得把简拼候选挤成碎片：wmd 里可以出现用户学过的
// 「我/没/的」单字候选（用户词库是合法来源），但不得拼成「我没的」这类
// 造词，也不得把「完整消耗简拼序列」的词典词挤出首选。
func TestInitialsUserDictSinglesDoNotFragment(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user_wmd.json")
	d := mustCommonDict(t)
	u := mustUserDict(t, udPath)

	// 模拟用户日常高频输入过单个汉字「我」、「没」、「的」
	for i := 0; i < 5; i++ {
		_ = u.BumpWord("我", []string{"wo"})
		_ = u.BumpWord("没", []string{"mei"})
		_ = u.BumpWord("的", []string{"de"})
	}

	s := NewSession(d, u)
	comp, err := s.Input("wmd")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatalf("wmd 应出候选，实际无候选")
	}
	all := allCandidates(s, comp)
	assertAllCandidatesInDict(t, d, "wmd", all)
	// ① 不允许把学习过的单字拼成复合候选（造词禁令对用户数据同样生效）
	for _, cand := range all {
		if cand.Text == "我没的" || cand.Text == "我们的" {
			t.Fatalf("wmd 候选不应包含拼合造词 %q: %+v", cand.Text, all[:min(6, len(all))])
		}
	}
	// ② 首选必须是完整消耗三段简拼（3 音节）的词典词，而不是单字碎片
	if got := strings.Count(all[0].Syllables, "'") + 1; got != 3 {
		t.Fatalf("wmd 首选应完整消耗 3 段简拼，实际首选 %+v（全部候选: %+v）", all[0], all[:min(6, len(all))])
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
