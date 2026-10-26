package engine

import "testing"

// 错拼修正回归测试。
//
// 用户手滑打错字母时（相邻颠倒 / 漏敲 / 多敲 / 敲错键），旧版要么给出
// 一堆碎片假词（zhogn → 「中欧国内」），要么直接无候选。
// 现在对输入做「单次编辑」修正后重新查询，把目标词找回来。
func TestTypoCorrectionFixesCommonMistakes(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	// 只覆盖「单次编辑」能修正的场景——这是 typoCorrections 的能力边界。
	// 需要两处改动的错拼（如 zhonggi → zhongguo 还要额外插入 u）不在范围内。
	cases := []struct{ input, want string }{
		{"zhogn", "中"},       // 相邻颠倒 g/o
		{"zhonng", "中"},      // 多敲一个 n
		{"zhonguo", "中国"},    // 漏敲一个 g
		{"jisuanjii", "计算机"}, // 多敲一个 i
		{"niihao", "你好"},     // 多敲一个 i
	}
	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Errorf("%s: 应有候选（期望打出「%s」）", c.input, c.want)
			continue
		}
		if got := comp.Candidates[0].Text; got != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望「%s」（候选: %+v）", c.input, got, c.want, comp.Candidates)
		}
	}
}

// QWERTY 键盘物理邻近键手误修正测试：手指按偏敲到相邻按键时，
// 优先推测相邻键形成的合法音节，且邻近键修正结果应优先于跨键区的远端巧合高频词。
func TestTypoCorrectionQWERTYNeighbors(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"yishrng", "一声"},  // e->r 邻近键：一声/医生 优先于跨键区的「以上」(r->a)
		{"diannap", "电脑"},  // o->p 邻近键：电脑
		{"zhonh", "中"},     // g->h 邻近键：中
		{"beijinf", "北京"},  // g->f 邻近键：北京
		{"womrn", "我们"},    // e->r 邻近键：我们
		{"guabxi", "关系"},   // n->b 邻近键：关系
		{"shebme", "什么"},   // n->b 邻近键：什么
		{"zhsopian", "照片"}, // a->s 邻近键：照片
		{"ksishui", "开水"},  // a->s 邻近键：开水
		{"husnying", "欢迎"}, // a->s 邻近键：欢迎
		{"fspiao", "发票"},   // a->s 邻近键：发票
	}
	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Errorf("%s: 应有候选（期望「%s」）", c.input, c.want)
			continue
		}
		if got := comp.Candidates[0].Text; got != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望「%s」（候选: %+v）", c.input, got, c.want, comp.Candidates)
		}
	}
}

// 关键的反向约束：错拼修正**绝不能**污染正常输入。
//
// 合法简拼（zg / bj / cs）、合法混拼（zhongg / womd / chifanl）与手误
// 在串形态上高度相似（nihap 与 chifanl 几乎是同一种形状），一旦判据放宽，
// 纠错候选就会挤掉正常结果——实测会让混拼首选率从 34.32% 掉到 20.75%，
// 且 zhongg → 中国 直接失败。
func TestTypoCorrectionDoesNotDisturbNormalInputs(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		// 简拼
		{"zg", "中国"},
		{"bj", "北京"},
		{"jsj", "计算机"},
		// 混拼（完整音节 + 末段声母）
		{"zhongg", "中国"},
		{"chifanl", "吃饭"},
		{"womd", "我们的"},
		// 正常全拼
		{"nihao", "你好"},
		{"jingchang", "经常"},
		{"jisuanji", "计算机"},
	}
	for _, c := range cases {
		s.Reset()
		comp, _ := s.Input(c.input)
		if len(comp.Candidates) == 0 {
			t.Fatalf("%s: 应有候选", c.input)
		}
		if got := comp.Candidates[0].Text; got != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望「%s」（纠错候选不得挤掉正常结果）", c.input, got, c.want)
		}
	}
}

// misspelledFromPatterns 的判据边界：混拼形态必须被判为「正常」，
// 彻底切不动的长串必须被判为「错拼」。
func TestMisspelledPrefilterBoundary(t *testing.T) {
	typo := []string{"zhogn", "zhonng", "zhonguo", "jisuanjii", "niihao"}
	for _, in := range typo {
		if !mayBeMisspelled(in) {
			t.Errorf("%q 应被判为错拼", in)
		}
	}
	normal := []string{"zhong", "nihao", "jingchang", "zhongg", "chifanl", "zg", "bj", "jsj", "yiw", "shangxj"}
	for _, in := range normal {
		if mayBeMisspelled(in) {
			t.Errorf("%q 不应被判为错拼（正常简拼/混拼输入）", in)
		}
	}
}
