package engine

import "testing"

// 输入预测（末音节续接）回归测试。
//
// 用户痛点：打「中guo」打到一半（zhonggu）、「bei jing」打到 beijin、
// 「da xue」打到 daxu 时，旧版候选全是「中古 / 被进 / 大需」这类巧合碎词，
// 真正想打的词完全出不来——在真人打字里这是最高频的形态（每个音节都必然
// 经过「打了一半」的状态）。修复后这些输入的首选必须是对应的完整词。
func TestContinuationPredictsTruncatedLastSyllable(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"zhonggu", "中国"},  // gu → guo
		{"beijin", "北京"},   // jin → jing
		{"daxu", "大学"},     // xu → xue
		{"bianchen", "变成"}, // chen → cheng
		{"liangha", "良好"},  // ha → hao
		{"zhiyo", "只有"},    // yo → you
		{"lilia", "力量"},    // lia → liang
		{"quanmia", "全面"},  // mia → mian（末段本身不是合法音节）
		{"shangmia", "上面"}, // mia → mian
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
			t.Errorf("%s: 第一候选 = %q, 期望「%s」（全部候选: %+v）", c.input, got, c.want, comp.Candidates)
		}
	}
}

// 续接预测不得反噬完整输入：输入本身已经是完整音节序列时（yisha = yi'sha
// 就是「一霎」），精确匹配必须排在所有预测候选之前。这是上一版把续接
// 无条件置于首位的直接教训（一霎 被「以上」压掉）。
func TestContinuationDoesNotOutrankExactMatch(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"yisha", "一霎"},
		{"zhonglian", "中联"},
		{"erdu", "二度"},
		{"fanggu", "仿古"},
		{"changxi", "唱戏"},
		{"daxian", "大限"},
		{"xuncha", "巡查"},
		{"shouna", "收纳"},
	}
	for _, c := range cases {
		s.Reset()
		comp, _ := s.Input(c.input)
		if len(comp.Candidates) == 0 {
			t.Fatalf("%s: 应有候选", c.input)
		}
		if got := comp.Candidates[0].Text; got != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望「%s」（续接预测不应压过精确匹配）", c.input, got, c.want)
		}
	}
}

// 完整输入的常用词首选不能被续接逻辑影响（长时间回归守门）。
func TestContinuationKeepsCommonInputs(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"nihao", "你好"},
		{"jingchang", "经常"},
		{"zhongguo", "中国"},
		{"jisuanji", "计算机"},
		{"ba", "把"},
		{"jing", "经"},
		{"xian", "现"}, // 2026-10-29 词库换用 Jun Da 字频 + jieba 融合：现(#70) 比 先(#188) 常用
		{"chifanle", "吃饭了"},
		{"kanyixia", "看一下"},
		{"diannaoshang", "电脑上"},
	}
	for _, c := range cases {
		s.Reset()
		comp, _ := s.Input(c.input)
		if len(comp.Candidates) == 0 {
			t.Fatalf("%s: 应有候选", c.input)
		}
		if got := comp.Candidates[0].Text; got != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望「%s」（候选: %+v）", c.input, got, c.want, comp.Candidates)
		}
	}
}
