package engine

import (
	"path/filepath"
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
		{"womd", "我们的"},
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

	// chifanl 混拼中应同时包含已完成的「吃饭」与续接的「吃饭了」
	s.Reset()
	comp, err := s.Input("chifanl")
	if err != nil {
		t.Fatalf("Input chifanl 失败: %v", err)
	}
	hasChifan, hasChifanle := false, false
	for _, cand := range comp.Candidates {
		if cand.Text == "吃饭" {
			hasChifan = true
		}
		if cand.Text == "吃饭了" {
			hasChifanle = true
		}
	}
	if !hasChifan || !hasChifanle {
		t.Fatalf("chifanl 候选应同时包含「吃饭」与「吃饭了」，实际候选: %+v", comp.Candidates)
	}
}

// 测试简拼通过动态规划生成复合整句（如 wmd -> 我们的）。
func TestInitialsSentenceComposition(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	comp, err := s.Input("wmd")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatalf("wmd 应出候选，实际无候选")
	}
	if comp.Candidates[0].Text != "我们的" {
		t.Fatalf("wmd 第一候选 = %q, 期望「我们的」（候选列表: %+v）", comp.Candidates[0].Text, comp.Candidates)
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

// 测试用户词库中单字频繁使用时，简拼复合句依然优先输出高频多字词组（如 wmd 优先输出「我们的」，而非碎字「我没的」）。
func TestInitialsUserDictSingleCharSuppression(t *testing.T) {
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
	if comp.Candidates[0].Text != "我们的" {
		t.Fatalf("存在高频单字学习记录时，wmd 第一候选仍应为「我们的」，实际得到 %q (候选列表: %+v)",
			comp.Candidates[0].Text, comp.Candidates)
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
