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
		comp, err := s.Input(c.input)
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
		{"niihao", "你好"},  // 粘键
		{"chifanl", "吃饭"}, // 合法混拼
		{"nihap", "你好"},   // 末位声母手误
	}
	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input)
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
