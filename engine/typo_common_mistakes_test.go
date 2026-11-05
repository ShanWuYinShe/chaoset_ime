package engine

import (
	"strings"
	"testing"
)

// 常见手误：把双字母组合打反（gn→ng / hz→zh / hc→ch / hs→sh）。
// 2026-11-01 用户反馈：经常把 ding 打成 dign；要求把这类容易打错的组合都
// 加入纠错。gn/hz/hc/hs 在普通话拼音里几乎不出现，属最高置信度手误，
// 走独立的高优先级归位路径（不受常规候选词频门槛约束）。

func TestCommonMistakeBigramsSingleSyllable(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"dign", "定"}, // ng → gn（用户反馈的典型）
		{"dagn", "当"},
		{"zhogn", "中"},
		{"chagn", "常"},
		{"shagn", "上"},
		{"hzong", "中"}, // zh → hz
		{"hcang", "常"}, // ch → hc
		{"hsang", "上"}, // sh → hs
		{"hsuo", "说"},
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

func TestCommonMistakeBigramsInWords(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"dignxiang", "丁香"}, // 也可出「定向」，只要求纠错后的词可达
		{"zhognguo", "中国"},
		{"beijign", "北京"},
		{"shagnhai", "上海"},
		{"xignfu", "幸福"},
		{"degndai", "等待"},
		{"pegnyou", "朋友"},
		{"jignchang", "经常"},
		{"zhognwen", "中文"},
		{"hzongguo", "中国"},
	}
	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input, false)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		all := allCandidates(s, comp)
		found := -1
		for i, cand := range all {
			if cand.Text == c.want {
				found = i
				break
			}
		}
		if found < 0 {
			t.Errorf("%s: 纠错后应能出「%s」，实际候选 %+v", c.input, c.want, all[:min(8, len(all))])
		}
	}
}

// TestCommonMistakeFixesMultiple：同一输入里多处反序组合要能一起翻回。
func TestCommonMistakeFixesMultiple(t *testing.T) {
	fixes := commonMistakeFixes("dignhzi")
	set := make(map[string]bool, len(fixes))
	for _, f := range fixes {
		set[f] = true
	}
	for _, want := range []string{"dinghzi", "dignzhi", "dingzhi"} {
		if !set[want] {
			t.Errorf("commonMistakeFixes(dignhzi) 缺少 %q，实际 %v", want, fixes)
		}
	}
	// 不含反序组合的输入不产生修正。
	if fixes := commonMistakeFixes("zhongguo"); len(fixes) != 0 {
		t.Errorf("zhongguo 不应产生反序修正，实际 %v", fixes)
	}
}

// TestCommonMistakeDoesNotDisturbNormalInputs：正常输入（含合法混拼/相邻互换
// 类词）的首选不能因新增反序纠错而改变。
func TestCommonMistakeDoesNotDisturbNormalInputs(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"zhongguo", "中国"},
		{"shanghai", "上海"},
		{"diannao", "电脑"},
		{"chifanl", "吃饭"}, // 合法混拼
	}
	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input, false)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Errorf("%s: 应有候选", c.input)
			continue
		}
		if got := comp.Candidates[0].Text; got != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望「%s」", c.input, got, c.want)
		}
	}
	// 手误类输入（粘键/末位声母）：纠错结果固定第 3 位，不改变正常输入排序。
	for _, c := range []struct{ input, want string }{
		{"niihao", "你好"},
		{"nihap", "你好"},
	} {
		s.Reset()
		comp, err := s.Input(c.input, false)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		assertTypoCorrectionPosition(t, comp, c.input, c.want)
	}
}

// TestCommonMistakeSegmentsInDict：反序修正出的候选必须是词典/用户词库里的词
// （不造词）——与总闸门同一约束。
func TestCommonMistakeSegmentsInDict(t *testing.T) {
	d := mustCommonDict(t)
	for _, input := range []string{"dign", "zhogn", "hzong", "hcang", "hsang", "zhognguo"} {
		all := collectAllCandidates(t, NewSession(d, nil), input)
		assertAllCandidatesInDict(t, d, input, all)
	}
	_ = strings.TrimSpace
}

// TestCommonMistakeSoftBigrams：反序形态在拼音音节里不出现、但可能与合法混拼
// 同形的韵母级组合（ao/oa、ua/au、ue/eu、ve/ev）：在纠错路径里作为最高优先级
// 修正，但不强制触发、不无条件提升；正常输入首选不变。
func TestCommonMistakeSoftBigrams(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"hoa", "好"}, // ao 打反
		{"doa", "大"},
		{"hau", "化"}, // ua 打反（hua 的首选是「化」）
		{"xeu", "学"}, // ue 打反
		{"lev", "略"}, // ve 打反（v 记 ü：lve）
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
	// 正常输入（含相同韵母的正确拼写）首选不能被软纠错改变。
	normal := []struct{ input, want string }{
		{"hao", "好"}, {"hua", "化"}, {"xue", "学"}, {"lve", "略"}, {"que", "却"},
	}
	for _, c := range normal {
		s.Reset()
		comp, err := s.Input(c.input, false)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if got := comp.Candidates[0].Text; got != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望「%s」（正常输入不应被软纠错影响）", c.input, got, c.want)
		}
	}
}

// TestCommonMistakeFixesContainsSoftPairs：软纠错组合也必须进入修正串列表。
func TestCommonMistakeFixesContainsSoftPairs(t *testing.T) {
	for input, want := range map[string]string{
		"hoa": "hao", "hau": "hua", "xeu": "xue", "lev": "lve",
	} {
		fixes := commonMistakeFixes(input)
		found := false
		for _, f := range fixes {
			if f == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("commonMistakeFixes(%q) 应包含 %q，实际 %v", input, want, fixes)
		}
	}
}
