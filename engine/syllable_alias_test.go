package engine

import "testing"

// 用户按键盘习惯敲 u 代替 ü 时（lue / nue），必须能打出词库里以 v
// 记写的音节（略 lve / 虐 nve）。修复前 lue 只能切出 lu + e，
// 首选是「路鄂」；nue 只能切 nu + e，首选是「怒鄂」。
func TestSyllableAliasUE(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	cases := []struct{ input, want string }{
		{"lue", "略"},
		{"nue", "虐"},
		{"lve", "略"}, // 标准写法同样可用
		{"nve", "虐"},
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

// 别名映射不得波及标准拼写：jue/que/xue/yue 本来就以 u 记写，
// 不能被映射成 jve/qve/xve/yve 这类非法音节。
func TestSyllableAliasDoesNotBreakUFinals(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	cases := []struct{ input, want string }{
		{"jue", "觉"},
		{"que", "却"},
		{"xue", "学"},
		{"yue", "月"},
	}
	for _, c := range cases {
		s.Reset()
		comp, _ := s.Input(c.input)
		if len(comp.Candidates) == 0 {
			t.Fatalf("%s: 应有候选", c.input)
		}
		if got := comp.Candidates[0].Text; got != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望「%s」", c.input, got, c.want)
		}
	}
	// canonicalSyllable 对不在别名表里的串必须原样返回
	for _, raw := range []string{"jue", "zhong", "zzz", ""} {
		if got := canonicalSyllable(raw); got != raw {
			t.Errorf("canonicalSyllable(%q) = %q, 期望原样返回", raw, got)
		}
	}
}

// 测试常见拼音省略习惯（uei->ui, iou->iu, uen->un）、撮口呼 v 键输入与后鼻音漏 g 的自动规范化与整词解析。
func TestSyllableAliasesPhoneticAndKeyboardConvention(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	cases := []struct{ input, want string }{
		// uei -> ui 省写习惯
		{"shueijiao", "睡觉"},
		{"dueibuqi", "对不起"},
		{"gueiding", "规定"},
		{"tueijian", "推荐"},
		// iou -> iu 省写习惯
		{"niourou", "牛肉"},
		{"jiouyue", "九月"},
		{"xiouli", "秀丽"},
		// uen -> un 省写习惯
		{"chuentian", "春天"},
		{"zhuenbei", "准备"},
		{"luenwen", "论文"},
		// j/q/x/y + v 撮口呼输入习惯
		{"jvece", "决策"},
		{"xvesheng", "学生"},
		{"yveliang", "月亮"},
		{"qvefa", "缺乏"},
		{"yvyan", "语言"},
		// 常见后鼻音漏 g (xion/jion/qion -> xiong/jiong/qiong)
		{"xionmao", "熊猫"},
		{"qionren", "穷人"},
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
