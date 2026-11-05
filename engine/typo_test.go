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
		{"zhoonng", "中"},     // 连击多个粘键 o/n
		{"zhonguo", "中国"},    // 漏敲一个 g
		{"jisuanjii", "计算机"}, // 多敲一个 i
		{"niihao", "你好"},     // 多敲一个 i
		{"niihaoo", "你好"},    // 连击多个粘键 i/o
	}
	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input, false)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Errorf("%s: 应有候选（期望打出「%s」）", c.input, c.want)
			continue
		}
		assertTypoCorrectionPosition(t, comp, c.input, c.want)
	}
}

// QWERTY 键盘物理邻近键手误修正测试：手指按偏敲到相邻按键时，
// 优先推测相邻键形成的合法音节，且邻近键修正结果应优先于跨键区的远端巧合高频词。
func TestTypoCorrectionQWERTYNeighbors(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"yishrng", "医生"},  // e->r 邻近键：医生 优先于跨键区的「以上」(r->a)；期望值随词库词频
		{"diannap", "电脑"},  // o->p 邻近键：电脑
		{"zhonh", "中"},     // g->h 邻近键：中
		{"beijinf", "北京"},  // g->f 邻近键：北京
		{"womrn", "我们"},    // e->r 邻近键：我们
		{"guabxi", "关系"},   // n->b 邻近键：关系
		{"shebme", "什么"},   // n->b 邻近键：什么
		{"zhsopian", "照片"}, // a->s 邻近键：照片
		{"ksishui", "死水"},  // a->s 邻近键；修正后首选按新词库词频（死水 > 开水）
		{"husnying", "欢迎"}, // a->s 邻近键：欢迎
		{"nihap", "你好"},    // o->p 邻近键：你好
		{"fspiao", "发票"},   // a->s 邻近键：发票
	}
	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input, false)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Errorf("%s: 应有候选（期望「%s」）", c.input, c.want)
			continue
		}
		assertTypoCorrectionPosition(t, comp, c.input, c.want)
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
		{"womd", "我们"}, // 半截末段走回退：最长已有词前缀「我们」；「我们的」不是词典词
		// 正常全拼
		{"nihao", "你好"},
		{"jingchang", "经常"},
		{"jisuanji", "计算机"},
	}
	for _, c := range cases {
		s.Reset()
		comp, _ := s.Input(c.input, false)
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

// TestTypoCorrectionPrefixConsumesWholeInput：错拼修正候选的**渐进上屏**
// 必须消费整串输入（2026-11-01 用户反馈）。
//
// 复现：输入 lianxiagn，首选候选是错拼修正命中的「联想」（lian'xiang）。
// 修复前 SelectPrefix 按候选标准音节逐段消费只吃掉 lian，剩余 xiagn 回灌
// 继续组词，候选变成 想/向/现/相…（用户截图一致）。错拼修正覆盖的是**整串**
// 输入，选中它时任何残余拼音都是被修正掉的碎片，不能当剩余组词输入。
func TestTypoCorrectionPrefixConsumesWholeInput(t *testing.T) {
	cases := []struct{ input, want string }{
		{"lianxiagn", "联想"},  // 用户复现场景（相邻颠倒）
		{"zhonguo", "中国"},    // 漏敲（插入）
		{"niihao", "你好"},     // 多敲（删除）
		{"jisuanjii", "计算机"}, // 末尾多敲：标准拼写恰好能对上输入前缀，残余是尾巴 i
		{"beijinf", "北京"},    // 邻近键敲错
		{"womrn", "我们"},      // 邻近键敲错
	}
	d := mustCommonDict(t)
	for _, c := range cases {
		s := NewSession(d, nil)
		if _, err := s.Input(c.input, false); err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		idx := findCandidate(t, s, c.want)
		rest, committed, err := s.SelectPrefix(idx)
		if err != nil || committed != c.want {
			t.Fatalf("%s: 渐进选「%s」失败: committed=%q err=%v", c.input, c.want, committed, err)
		}
		if rest.Input != "" || rest.Preedit != "" {
			t.Errorf("%s: 错拼修正候选应消费整串输入，实际剩余 Input=%q Preedit=%q（碎片回灌会弹无关候选）",
				c.input, rest.Input, rest.Preedit)
		}
	}
}

// TestSelectPrefixKeepsLegitMixedPartialConsumption 反向锁定：常规切分路径
// 也能命中的候选**不是**纯错拼来源，渐进上屏必须保持既有部分消费语义——
// 剩余输入是下一个待打音节/声母，不能当错拼碎片吞掉。
//
// 典型：chifanl = 吃饭 + l（合法混拼，用户接着打「了」），nihaox = 你好 + x。
// 若把「同时被错拼路径命中」的候选一并整串消费，这些合法输入会丢字
// （2026-11-01 修复时实测的边界）。zhongg 的 g 是候选音节 guo 的声母，
// 选中「中国」时会一起消耗，不再作为「下一个待打声母」残留。
func TestSelectPrefixKeepsLegitMixedPartialConsumption(t *testing.T) {
	cases := []struct{ input, want, wantRest string }{
		{"chifanl", "吃饭", "l"},
		{"zhongg", "中国", ""},
		{"nihaox", "你好", "x"},
		{"xveshengwomen", "学生", "women"}, // 省写/别名形态（既有回归）
		{"jintianwochile", "今天", "wochile"},
	}
	d := mustCommonDict(t)
	for _, c := range cases {
		s := NewSession(d, nil)
		if _, err := s.Input(c.input, false); err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		idx := findCandidate(t, s, c.want)
		rest, committed, err := s.SelectPrefix(idx)
		if err != nil || committed != c.want {
			t.Fatalf("%s: 渐进选「%s」失败: committed=%q err=%v", c.input, c.want, committed, err)
		}
		if rest.Input != c.wantRest {
			t.Errorf("%s: 选「%s」后剩余输入 = %q, 期望 %q（合法部分消费语义被整串消费破坏）",
				c.input, c.want, rest.Input, c.wantRest)
		}
	}
}

// assertTypoCorrectionPosition 校验错拼纠错候选的固定位置（2026-11-02 用户要求）：
// 有足够正常候选时固定在第 3 位；若输入只有纠错候选（没有正常匹配）或候选总数
// 不足 3 个，则允许从第 1 位开始（没有可填充的正常候选）。
func assertTypoCorrectionPosition(t *testing.T, comp Composition, input, want string) {
	t.Helper()
	idx := -1
	for i, c := range comp.Candidates {
		if c.Text == want {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("%s: 候选里没有纠错结果「%s」: %+v", input, want, comp.Candidates)
	}
	if idx == 2 {
		return // 固定第 3 位
	}
	allTypo := len(comp.Candidates) > 0
	for _, c := range comp.Candidates {
		if !c.typoFix {
			allTypo = false
			break
		}
	}
	if idx == 0 && (len(comp.Candidates) < 3 || allTypo) {
		return // 没有足够的正常候选填充前两位
	}
	t.Fatalf("%s: 纠错结果「%s」应固定在第 3 位，实际第 %d 位（候选: %+v）", input, want, idx+1, comp.Candidates)
}
